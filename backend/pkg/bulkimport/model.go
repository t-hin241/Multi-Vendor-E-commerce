// Package bulkimport runs AF-16 vendor CSV imports and exports inside the
// service that owns the data (Catalog for products/prices, Inventory for
// stock receipts). This package only holds the job lifecycle: upload,
// parse, dry-run preview, confirm, per-row apply, cancel, error report and
// export. Every business rule and every mutation stays in the owner's use
// cases, reached through the Kind interface; jobs never write another
// service's data.
//
// Lifecycle: uploaded -> validating -> preview_ready -> confirmed ->
// applying -> completed. A file the parser refuses ends in file_invalid;
// cancel stops rows not yet applied (cancelling while an applied-or-not row
// is resolved) and never undoes applied rows.
package bulkimport

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"shopee/backend/pkg/apperror"
)

// Job statuses.
const (
	StatusUploaded     = "uploaded"
	StatusValidating   = "validating"
	StatusPreviewReady = "preview_ready"
	StatusFileInvalid  = "file_invalid"
	StatusFailed       = "failed"
	StatusConfirmed    = "confirmed"
	StatusApplying     = "applying"
	StatusCancelling   = "cancelling"
	StatusCancelled    = "cancelled"
	StatusCompleted    = "completed"
)

// Row statuses. invalid/unchanged come from the preview; valid rows wait
// for confirm; applying rows hold a lease; the rest are terminal.
const (
	RowInvalid       = "invalid"
	RowUnchanged     = "unchanged"
	RowValid         = "valid"
	RowApplying      = "applying"
	RowSucceeded     = "succeeded"
	RowNeedsApproval = "needs_approval"
	RowFailed        = "failed"
	RowCancelled     = "cancelled"
)

const (
	// PreviewTTL bounds how old a dry-run may be when confirmed.
	PreviewTTL = 24 * time.Hour
	// FileRetention keeps the uploaded original after processing.
	FileRetention = 7 * 24 * time.Hour
	// ReportRetention keeps error reports and exports.
	ReportRetention = 24 * time.Hour
	// DownloadTTL is the lifetime of a signed download link.
	DownloadTTL = 60 * time.Second
	// MaxApplyAttempts bounds retries of a row after transient failures.
	MaxApplyAttempts = 5
	// MaxExportRows bounds one export file.
	MaxExportRows = 50000
)

// Column is one template column. ReadOnly columns are written by exports
// for reference and ignored on import.
type Column struct {
	Name        string `json:"name"`
	Required    bool   `json:"required"`
	ReadOnly    bool   `json:"read_only,omitempty"`
	Description string `json:"description"`
	Example     string `json:"example,omitempty"`
}

// Template is the versioned contract of one kind.
type Template struct {
	Kind    string   `json:"kind"`
	Version int      `json:"version"`
	Columns []Column `json:"columns"`
	// DynamicPrefix allows extra columns named <prefix><code> (Catalog
	// attributes); empty means no dynamic columns.
	DynamicPrefix  string   `json:"dynamic_prefix,omitempty"`
	Notes          []string `json:"notes"`
	Exportable     bool     `json:"exportable"`
	Enabled        bool     `json:"enabled"`
	DisabledReason string   `json:"disabled_reason,omitempty"`
	MaxRows        int      `json:"max_rows"`
	MaxBytes       int64    `json:"max_bytes"`
}

// Record is one parsed data row. RowNo is 1-based (the header is line 1,
// so the row is on line RowNo+1).
type Record struct {
	RowNo  int
	Values map[string]string
}

// RowError is a row-level problem safe to show the vendor (no SQL, no
// internal identifiers of other shops).
type RowError struct {
	Code    string `json:"code"`
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
}

// Plan is the owner's dry-run of one record. A Plan with Error is invalid;
// Unchanged rows are shown but never applied.
type Plan struct {
	TargetKey       string
	Payload         any
	ExpectedVersion *int64
	Old             map[string]any
	New             map[string]any
	Unchanged       bool
	Error           *RowError
}

// Row is one stored row as the apply step and the API see it.
type Row struct {
	JobID           string          `json:"job_id"`
	RowNo           int             `json:"row_no"`
	Status          string          `json:"status"`
	TargetKey       string          `json:"target_key,omitempty"`
	Values          []string        `json:"values"`
	Payload         json.RawMessage `json:"-"`
	ExpectedVersion *int64          `json:"expected_version,omitempty"`
	Old             map[string]any  `json:"old_values,omitempty"`
	New             map[string]any  `json:"new_values,omitempty"`
	OperationRef    string          `json:"operation_ref"`
	ResultRef       string          `json:"result_ref,omitempty"`
	Error           *RowError       `json:"error,omitempty"`
	Attempts        int             `json:"attempts"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

// Summary counts rows by status; Total always equals their sum.
type Summary struct {
	Total         int `json:"total"`
	Invalid       int `json:"invalid"`
	Unchanged     int `json:"unchanged"`
	Pending       int `json:"pending"`
	Succeeded     int `json:"succeeded"`
	NeedsApproval int `json:"needs_approval"`
	Failed        int `json:"failed"`
	Cancelled     int `json:"cancelled"`
}

// Job is one import of one kind into one shop.
type Job struct {
	ID              string     `json:"id"`
	VendorID        string     `json:"vendor_id"`
	Kind            string     `json:"kind"`
	TemplateVersion int        `json:"template_version"`
	Status          string     `json:"status"`
	Outcome         string     `json:"outcome,omitempty"`
	FileName        string     `json:"file_name"`
	FileHash        string     `json:"file_hash"`
	FileBytes       int64      `json:"file_bytes"`
	Header          []string   `json:"header"`
	PreviewHash     string     `json:"preview_hash,omitempty"`
	PreviewAt       *time.Time `json:"preview_at,omitempty"`
	DuplicateOf     string     `json:"duplicate_of,omitempty"`
	ErrorCode       string     `json:"error_code,omitempty"`
	ErrorMessage    string     `json:"error_message,omitempty"`
	Version         int64      `json:"version"`
	RequestedBy     string     `json:"requested_by"`
	ConfirmedBy     string     `json:"confirmed_by,omitempty"`
	Summary         Summary    `json:"summary"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
	StatusURL       string     `json:"status_url"`

	objectKey      string
	idempotencyKey string
	attempts       int
}

// Export is one CSV export of one kind for one shop.
type Export struct {
	ID          string     `json:"id"`
	VendorID    string     `json:"vendor_id"`
	Kind        string     `json:"kind"`
	Status      string     `json:"status"`
	RowCount    int        `json:"row_count"`
	RequestedBy string     `json:"requested_by"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	StatusURL   string     `json:"status_url"`

	objectKey      string
	idempotencyKey string
	attempts       int
}

// Page is a cursor page.
type Page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// Result is what applying one row produced in the owner.
type Result struct {
	Ref           string
	NeedsApproval bool
}

// Kind is the owner's implementation of one import kind.
type Kind interface {
	Template() Template
	// Available reports whether new jobs of this kind may start for the
	// shop (owner feature flag, shop policy); reason is shown when not.
	Available(ctx context.Context, vendor string) (ok bool, reason string, err error)
	// Plan dry-runs the records without mutating anything. Returning an
	// error means the dry-run could not run (retried); row problems go in
	// Plan.Error.
	Plan(ctx context.Context, vendor string, records []Record) ([]Plan, error)
	// Apply performs one row through the owner's use case. It runs inside
	// the transaction that also records the row result, so a crash can
	// never apply a row twice. A 4xx apperror fails the row; 403 stops the
	// job; anything else is retried.
	Apply(ctx context.Context, actor, vendor string, row Row) (Result, error)
	// Export writes the shop's current data in the template format, or is
	// nil-safe to return ErrNoExport.
	Export(ctx context.Context, vendor string, write func([]string) error) error
}

// Problem builds a client-facing error with a stable code.
func Problem(status int, code, message string) *apperror.Error {
	return &apperror.Error{Code: apperror.Code(code), Status: status, Message: message}
}

// ErrNoExport is returned by kinds that cannot be exported.
var ErrNoExport = Problem(http.StatusUnprocessableEntity, "export_unsupported", "This template has no export")

func unavailable(err error) error {
	return &apperror.Error{Code: "bulk_operations_unavailable", Status: http.StatusServiceUnavailable, Message: "Bulk operations are temporarily unavailable", Err: err}
}
