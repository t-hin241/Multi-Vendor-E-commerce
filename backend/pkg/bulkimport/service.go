package bulkimport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/middleware"
)

// Files is the owner's private bucket (never the public media bucket).
type Files interface {
	Put(ctx context.Context, key string, data []byte, contentType string) error
	PutStream(ctx context.Context, key string, data io.Reader, contentType string) error
	Open(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
	DownloadFileURL(ctx context.Context, key, filename string, ttl time.Duration) (string, error)
}

// Authorizer checks the shop permission of actor with the Vendor service:
// write=false for reading jobs/templates/exports, true to import.
type Authorizer func(ctx context.Context, actor, vendor string, write bool) error

// Service is the job lifecycle of one owner service.
type Service struct {
	// Owner is the URL segment of the owner ("catalog", "inventory").
	Owner     string
	Store     Store
	Files     Files
	Kinds     map[string]Kind
	Authorize Authorizer
	// Enabled is FEATURE_BULK_OPERATIONS_ENABLED of the owner. Off closes
	// new uploads, confirms and exports; accepted work drains.
	Enabled  bool
	MaxRows  int
	MaxBytes int64
	Log      zerolog.Logger
}

var keyPattern = regexp.MustCompile(`^[\x21-\x7e]{8,128}$`)

func notFound() error { return apperror.NotFound("Import job not found") }

// storeError keeps business errors and hides infrastructure ones.
func storeError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, errNotFound) {
		return notFound()
	}
	var app *apperror.Error
	if errors.As(err, &app) {
		return err
	}
	return unavailable(err)
}

func (s *Service) kind(name string) (Kind, error) {
	k, ok := s.Kinds[name]
	if !ok {
		return nil, Problem(http.StatusUnprocessableEntity, "unknown_kind", "Unknown import template")
	}
	return k, nil
}

func (s *Service) disabledReason(ctx context.Context, k Kind, vendor string) (string, error) {
	if !s.Enabled {
		return "Bulk operations are not enabled", nil
	}
	if s.Files == nil {
		return "Private file storage is not configured", nil
	}
	ok, reason, err := k.Available(ctx, vendor)
	if err != nil {
		return "", storeError(err)
	}
	if !ok {
		return reason, nil
	}
	return "", nil
}

// Template describes a kind for a shop, with whether it can be used now.
func (s *Service) Template(ctx context.Context, actor, vendor, kind string) (Template, error) {
	if _, err := uuid.Parse(vendor); err != nil {
		return Template{}, apperror.Validation("vendor_id must be a UUID")
	}
	k, err := s.kind(kind)
	if err != nil {
		return Template{}, err
	}
	if err = s.Authorize(ctx, actor, vendor, false); err != nil {
		return Template{}, err
	}
	t := k.Template()
	t.MaxRows, t.MaxBytes = s.MaxRows, s.MaxBytes
	reason, err := s.disabledReason(ctx, k, vendor)
	if err != nil {
		return Template{}, err
	}
	t.Enabled, t.DisabledReason = reason == "", reason
	return t, nil
}

// UploadInput is a multipart upload that creates one job.
type UploadInput struct {
	VendorID        string
	Kind            string
	TemplateVersion int
	FileName        string
	Data            []byte
}

func cleanFileName(name string) string {
	name = path.Base(strings.ReplaceAll(name, `\`, "/"))
	var b strings.Builder
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune(" ._-()", r) {
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(b.String())
	if len(out) > 120 {
		out = out[:120]
	}
	if out == "" || out == "." {
		return "import.csv"
	}
	return out
}

func sameUpload(j *Job, actor string, in UploadInput, hash string) error {
	if j.RequestedBy != actor || j.Kind != in.Kind || j.FileHash != hash || j.TemplateVersion != in.TemplateVersion {
		return Problem(http.StatusConflict, "idempotency_conflict", "Idempotency-Key was used with another file or template")
	}
	return nil
}

// Upload stores the original privately and queues its dry-run. The key is
// the actor's intent (kind + template) plus the file content hash; the file
// name is display only.
func (s *Service) Upload(ctx context.Context, actor, key string, in UploadInput) (*Job, error) {
	if !keyPattern.MatchString(key) {
		return nil, apperror.Validation("Idempotency-Key must be 8-128 printable characters")
	}
	if _, err := uuid.Parse(in.VendorID); err != nil {
		return nil, apperror.Validation("vendor_id must be a UUID")
	}
	k, err := s.kind(in.Kind)
	if err != nil {
		return nil, err
	}
	if err = s.Authorize(ctx, actor, in.VendorID, true); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(in.Data)
	hash := hex.EncodeToString(sum[:])
	existing, err := s.Store.JobByKey(ctx, in.VendorID, key)
	if err == nil {
		existing.StatusURL = s.statusURL(existing.ID)
		return existing, sameUpload(existing, actor, in, hash)
	}
	if !errors.Is(err, errNotFound) {
		return nil, storeError(err)
	}
	reason, err := s.disabledReason(ctx, k, in.VendorID)
	if err != nil {
		return nil, err
	}
	if reason != "" {
		return nil, Problem(http.StatusConflict, "bulk_operations_disabled", reason)
	}
	t := k.Template()
	if in.TemplateVersion != t.Version {
		return nil, Problem(http.StatusUnprocessableEntity, "template_version_mismatch", fmt.Sprintf("Download template %s version %d and upload it again", t.Kind, t.Version))
	}
	if len(in.Data) == 0 {
		return nil, FileInvalid("File is empty")
	}
	// Structural problems are reported now; row rules run in the dry-run.
	parsed, err := Parse(bytes.NewReader(in.Data), t, s.MaxRows, s.MaxBytes)
	if err != nil {
		return nil, err
	}
	j := &Job{ID: uuid.NewString(), VendorID: in.VendorID, Kind: in.Kind, TemplateVersion: t.Version, RequestedBy: actor,
		FileName: cleanFileName(in.FileName), FileHash: hash, FileBytes: int64(len(in.Data)), Header: parsed.Header, idempotencyKey: key}
	j.objectKey = fmt.Sprintf("imports/%s/%s.csv", in.VendorID, j.ID)
	if err = s.Files.Put(ctx, j.objectKey, in.Data, "text/csv"); err != nil {
		return nil, unavailable(err)
	}
	err = s.Store.Transact(ctx, func(ctx context.Context) error {
		if err := s.Store.CreateJob(ctx, j); err != nil {
			return err
		}
		return s.Store.Event(ctx, j.ID, j.VendorID, actor, "uploaded", "", middleware.RequestIDFromContext(ctx),
			map[string]any{"kind": j.Kind, "file_hash": hash, "rows": len(parsed.Records)})
	})
	if err != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if delErr := s.Files.Delete(cleanup, j.objectKey); delErr != nil {
			s.Log.Warn().Str("job_id", j.ID).Msg("import_orphan_object")
		}
		if errors.Is(err, errDuplicateKey) {
			existing, err := s.Store.JobByKey(ctx, in.VendorID, key)
			if err != nil {
				return nil, storeError(err)
			}
			existing.StatusURL = s.statusURL(existing.ID)
			return existing, sameUpload(existing, actor, in, hash)
		}
		return nil, storeError(err)
	}
	j.Summary = Summary{}
	j.StatusURL = s.statusURL(j.ID)
	return j, nil
}

func (s *Service) statusURL(id string) string {
	return "/api/" + s.Owner + "/vendor/import-jobs/" + id
}

func outcome(j *Job) string {
	// A job cancelled before confirm applied nothing by choice: no outcome.
	if (j.Status != StatusCompleted && j.Status != StatusCancelled) || j.ConfirmedBy == "" {
		return ""
	}
	done := j.Summary.Succeeded + j.Summary.NeedsApproval
	bad := j.Summary.Failed + j.Summary.Cancelled + j.Summary.Invalid
	switch {
	case done == 0:
		return "failed"
	case bad == 0:
		return "succeeded"
	default:
		return "partial"
	}
}

func (s *Service) present(j *Job) *Job {
	j.Outcome = outcome(j)
	j.StatusURL = s.statusURL(j.ID)
	return j
}

// job loads a job and checks the actor's permission on its shop: the shop
// comes from the job, never from the client. Jobs of a shop the actor
// cannot read are 404; missing write permission on a readable shop is 403.
func (s *Service) job(ctx context.Context, actor, id string, write bool) (*Job, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, notFound()
	}
	j, err := s.Store.Job(ctx, id)
	if err != nil {
		return nil, storeError(err)
	}
	if err = s.Authorize(ctx, actor, j.VendorID, false); err != nil {
		var app *apperror.Error
		if errors.As(err, &app) && app.Status == http.StatusForbidden {
			return nil, notFound()
		}
		return nil, err
	}
	if write {
		if err = s.Authorize(ctx, actor, j.VendorID, true); err != nil {
			return nil, err
		}
	}
	return j, nil
}

func (s *Service) Job(ctx context.Context, actor, id string) (*Job, error) {
	j, err := s.job(ctx, actor, id, false)
	if err != nil {
		return nil, err
	}
	return s.present(j), nil
}

// Jobs lists a shop's jobs, optionally of one kind.
func (s *Service) Jobs(ctx context.Context, actor, vendor, kind, cursor string, limit int) (Page[Job], error) {
	if _, err := uuid.Parse(vendor); err != nil {
		return Page[Job]{}, apperror.Validation("vendor_id must be a UUID")
	}
	if kind != "" {
		if _, err := s.kind(kind); err != nil {
			return Page[Job]{}, err
		}
	}
	if err := s.Authorize(ctx, actor, vendor, false); err != nil {
		return Page[Job]{}, err
	}
	return s.list(ctx, vendor, kind, "", cursor, limit)
}

func (s *Service) list(ctx context.Context, vendor, kind, status, cursor string, limit int) (Page[Job], error) {
	if limit < 1 || limit > 100 {
		return Page[Job]{}, apperror.Validation("limit must be 1-100")
	}
	p, err := s.Store.Jobs(ctx, vendor, kind, status, cursor, limit)
	if err != nil {
		return p, storeError(err)
	}
	for i := range p.Items {
		s.present(&p.Items[i])
	}
	return p, nil
}

// AdminJobs lists jobs of every shop (or one) for operators.
func (s *Service) AdminJobs(ctx context.Context, vendor, status, cursor string, limit int) (Page[Job], error) {
	if vendor != "" {
		if _, err := uuid.Parse(vendor); err != nil {
			return Page[Job]{}, apperror.Validation("vendor_id must be a UUID")
		}
	}
	switch status {
	case "", StatusUploaded, StatusValidating, StatusPreviewReady, StatusFileInvalid, StatusFailed, StatusConfirmed, StatusApplying, StatusCancelling, StatusCancelled, StatusCompleted:
	default:
		return Page[Job]{}, apperror.Validation("Unknown job status")
	}
	return s.list(ctx, vendor, "", status, cursor, limit)
}

var rowStatuses = map[string]bool{"": true, "pending": true, RowInvalid: true, RowUnchanged: true, RowValid: true, RowApplying: true,
	RowSucceeded: true, RowNeedsApproval: true, RowFailed: true, RowCancelled: true}

func (s *Service) Rows(ctx context.Context, actor, id, status, cursor string, limit int) (Page[Row], error) {
	if !rowStatuses[status] {
		return Page[Row]{}, apperror.Validation("Unknown row status")
	}
	if limit < 1 || limit > 100 {
		return Page[Row]{}, apperror.Validation("limit must be 1-100")
	}
	if _, err := s.job(ctx, actor, id, false); err != nil {
		return Page[Row]{}, err
	}
	p, err := s.Store.Rows(ctx, id, status, cursor, limit)
	return p, storeError(err)
}

// ConfirmInput pins what the vendor saw.
type ConfirmInput struct {
	PreviewHash          string `json:"preview_hash"`
	ExpectedVersion      int64  `json:"expected_version"`
	AcknowledgeDuplicate bool   `json:"acknowledge_duplicate_file"`
}

// Confirm starts applying the previewed rows. Replaying the same confirm
// returns the job; any other change since the preview is 409.
func (s *Service) Confirm(ctx context.Context, actor, id string, in ConfirmInput) (*Job, error) {
	j, err := s.job(ctx, actor, id, true)
	if err != nil {
		return nil, err
	}
	k, err := s.kind(j.Kind)
	if err != nil {
		return nil, err
	}
	var out *Job
	err = s.Store.Transact(ctx, func(ctx context.Context) error {
		j, err := s.Store.LockJob(ctx, id)
		if err != nil {
			return err
		}
		if j.ConfirmedBy != "" && j.PreviewHash == in.PreviewHash && j.ConfirmedBy == actor {
			out = j
			return nil
		}
		if j.Status != StatusPreviewReady {
			return Problem(http.StatusConflict, "state_conflict", "Only a previewed job can be confirmed")
		}
		if j.Version != in.ExpectedVersion || j.PreviewHash != in.PreviewHash {
			return Problem(http.StatusConflict, "preview_stale", "The job changed since you viewed it; reload the preview")
		}
		if j.PreviewAt == nil || time.Since(*j.PreviewAt) > PreviewTTL {
			return Problem(http.StatusConflict, "preview_stale", "The preview is older than 24 hours; upload the file again")
		}
		if j.Summary.Pending == 0 {
			return Problem(http.StatusConflict, "nothing_to_apply", "No valid rows to apply")
		}
		reason, err := s.disabledReason(ctx, k, j.VendorID)
		if err != nil {
			return err
		}
		if reason != "" {
			return Problem(http.StatusConflict, "bulk_operations_disabled", reason)
		}
		// A different job may have applied the same file since the preview.
		dup, err := s.Store.DuplicateFile(ctx, j.VendorID, j.Kind, j.FileHash, j.ID)
		if err != nil {
			return err
		}
		if dup != "" && !in.AcknowledgeDuplicate {
			return Problem(http.StatusConflict, "duplicate_file", "This file was already applied by job "+dup+"; confirm again only if it is intentional")
		}
		if err = s.Store.MarkConfirmed(ctx, id, actor); err != nil {
			return err
		}
		if err = s.Store.Event(ctx, id, j.VendorID, actor, "confirmed", "", middleware.RequestIDFromContext(ctx),
			map[string]any{"preview_hash": in.PreviewHash, "duplicate_of": dup, "rows": j.Summary.Pending}); err != nil {
			return err
		}
		out, err = s.Store.LockJob(ctx, id)
		return err
	})
	if err != nil {
		return nil, storeError(err)
	}
	return s.present(out), nil
}

// Cancel stops rows not applied yet. Applied rows stay applied.
func (s *Service) Cancel(ctx context.Context, actor, id, reason string) (*Job, error) {
	if _, err := s.job(ctx, actor, id, true); err != nil {
		return nil, err
	}
	return s.cancel(ctx, actor, id, "cancelled", strings.TrimSpace(reason), "Cancelled before this row was applied")
}

// AdminCancel is the operator's stop (rollback drain) with a reason.
func (s *Service) AdminCancel(ctx context.Context, actor, id, reason string) (*Job, error) {
	reason = strings.TrimSpace(reason)
	if len(reason) < 3 || len(reason) > 500 {
		return nil, apperror.Validation("reason must be 3-500 bytes")
	}
	if _, err := uuid.Parse(id); err != nil {
		return nil, notFound()
	}
	return s.cancel(ctx, actor, id, "admin_cancelled", reason, "Stopped by marketplace operations before this row was applied")
}

func (s *Service) cancel(ctx context.Context, actor, id, action, reason, rowMessage string) (*Job, error) {
	if len(reason) > 500 {
		return nil, apperror.Validation("reason must be at most 500 bytes")
	}
	var out *Job
	err := s.Store.Transact(ctx, func(ctx context.Context) error {
		j, err := s.Store.LockJob(ctx, id)
		if err != nil {
			return err
		}
		switch j.Status {
		case StatusCancelled, StatusCancelling:
			out = j
			return nil
		case StatusCompleted, StatusFileInvalid, StatusFailed:
			return Problem(http.StatusConflict, "state_conflict", "The job has already finished")
		}
		if err = s.Store.Cancel(ctx, id, actor, action, rowMessage); err != nil {
			return err
		}
		if err = s.Store.Event(ctx, id, j.VendorID, actor, action, reason, middleware.RequestIDFromContext(ctx), map[string]any{"from": j.Status}); err != nil {
			return err
		}
		out, err = s.Store.LockJob(ctx, id)
		return err
	})
	if err != nil {
		return nil, storeError(err)
	}
	return s.present(out), nil
}

// Download is a signed link to a private file.
type Download struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

// ReportDownload links the CSV of rows to fix (invalid, failed, cancelled)
// with the original columns plus the error, ready to edit into a new job.
func (s *Service) ReportDownload(ctx context.Context, actor, id string) (*Download, error) {
	j, err := s.job(ctx, actor, id, false)
	if err != nil {
		return nil, err
	}
	if j.Status != StatusPreviewReady && j.Status != StatusCompleted && j.Status != StatusCancelled {
		return nil, Problem(http.StatusConflict, "state_conflict", "The report is available after the preview and after the job finishes")
	}
	if s.Files == nil {
		return nil, unavailable(errors.New("private storage not configured"))
	}
	key, err := s.Store.Report(ctx, id, j.Version)
	if err != nil {
		return nil, storeError(err)
	}
	if key == "" {
		if key, err = s.writeReport(ctx, j); err != nil {
			return nil, err
		}
	}
	if err = s.Store.Event(ctx, id, j.VendorID, actor, "report_downloaded", "", middleware.RequestIDFromContext(ctx), nil); err != nil {
		return nil, storeError(err)
	}
	url, err := s.Files.DownloadFileURL(ctx, key, "import-errors-"+j.ID[:8]+".csv", DownloadTTL)
	if err != nil {
		return nil, unavailable(err)
	}
	return &Download{URL: url, ExpiresAt: time.Now().Add(DownloadTTL)}, nil
}

func (s *Service) writeReport(ctx context.Context, j *Job) (string, error) {
	rows, err := s.Store.ProblemRows(ctx, j.ID)
	if err != nil {
		return "", storeError(err)
	}
	var buf bytes.Buffer
	w, err := NewCSVWriter(&buf)
	if err != nil {
		return "", err
	}
	header := append(append([]string{"row_no"}, j.Header...), "status", "error_code", "error_field", "error_message")
	if err = w.Write(header); err != nil {
		return "", err
	}
	for _, r := range rows {
		cells := append([]string{fmt.Sprint(r.RowNo)}, r.Values...)
		e := r.Error
		if e == nil {
			e = &RowError{}
		}
		if err = w.Write(append(cells, r.Status, e.Code, e.Field, e.Message)); err != nil {
			return "", err
		}
	}
	if err = w.Flush(); err != nil {
		return "", err
	}
	key := fmt.Sprintf("reports/%s/%s/%d.csv", j.VendorID, j.ID, j.Version)
	if err = s.Files.Put(ctx, key, buf.Bytes(), "text/csv"); err != nil {
		return "", unavailable(err)
	}
	old, err := s.Store.SaveReport(ctx, j.ID, key, j.Version)
	if err != nil {
		return "", storeError(err)
	}
	if old != "" && old != key {
		if err := s.Files.Delete(ctx, old); err != nil {
			s.Log.Warn().Str("job_id", j.ID).Msg("import_report_cleanup_failed")
		}
	}
	return key, nil
}

func (s *Service) exportURL(id string) string {
	return "/api/" + s.Owner + "/vendor/export-jobs/" + id
}

// CreateExport queues an export of the shop's current data in the template
// format (with versions, so it can be edited and imported again).
func (s *Service) CreateExport(ctx context.Context, actor, vendor, kind, key string) (*Export, error) {
	if !keyPattern.MatchString(key) {
		return nil, apperror.Validation("Idempotency-Key must be 8-128 printable characters")
	}
	if _, err := uuid.Parse(vendor); err != nil {
		return nil, apperror.Validation("vendor_id must be a UUID")
	}
	k, err := s.kind(kind)
	if err != nil {
		return nil, err
	}
	if !k.Template().Exportable {
		return nil, ErrNoExport
	}
	if err = s.Authorize(ctx, actor, vendor, false); err != nil {
		return nil, err
	}
	existing, err := s.Store.ExportByKey(ctx, vendor, key)
	if err == nil {
		if existing.RequestedBy != actor || existing.Kind != kind {
			return nil, Problem(http.StatusConflict, "idempotency_conflict", "Idempotency-Key was used for another export")
		}
		existing.StatusURL = s.exportURL(existing.ID)
		return existing, nil
	}
	if !errors.Is(err, errNotFound) {
		return nil, storeError(err)
	}
	if !s.Enabled || s.Files == nil {
		return nil, Problem(http.StatusConflict, "bulk_operations_disabled", "Bulk operations are not enabled")
	}
	e := &Export{ID: uuid.NewString(), VendorID: vendor, Kind: kind, RequestedBy: actor, idempotencyKey: key}
	e.objectKey = fmt.Sprintf("exports/%s/%s.csv", vendor, e.ID)
	err = s.Store.Transact(ctx, func(ctx context.Context) error {
		if err := s.Store.CreateExport(ctx, e); err != nil {
			return err
		}
		return s.Store.Event(ctx, e.ID, vendor, actor, "export_requested", "", middleware.RequestIDFromContext(ctx), map[string]any{"kind": kind})
	})
	if errors.Is(err, errDuplicateKey) {
		return s.CreateExport(ctx, actor, vendor, kind, key)
	}
	if err != nil {
		return nil, storeError(err)
	}
	e.StatusURL = s.exportURL(e.ID)
	return e, nil
}

func (s *Service) export(ctx context.Context, actor, id string) (*Export, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, apperror.NotFound("Export not found")
	}
	e, err := s.Store.Export(ctx, id)
	if errors.Is(err, errNotFound) {
		return nil, apperror.NotFound("Export not found")
	}
	if err != nil {
		return nil, storeError(err)
	}
	if err = s.Authorize(ctx, actor, e.VendorID, false); err != nil {
		var app *apperror.Error
		if errors.As(err, &app) && app.Status == http.StatusForbidden {
			return nil, apperror.NotFound("Export not found")
		}
		return nil, err
	}
	if e.Status == "ready" && e.ExpiresAt != nil && !time.Now().Before(*e.ExpiresAt) {
		e.Status = "expired"
	}
	e.StatusURL = s.exportURL(e.ID)
	return e, nil
}

func (s *Service) Export(ctx context.Context, actor, id string) (*Export, error) {
	return s.export(ctx, actor, id)
}

func (s *Service) ExportDownload(ctx context.Context, actor, id string) (*Download, error) {
	e, err := s.export(ctx, actor, id)
	if err != nil {
		return nil, err
	}
	if e.Status != "ready" || e.ExpiresAt == nil {
		return nil, Problem(http.StatusConflict, "state_conflict", "Export is not ready or has expired")
	}
	if s.Files == nil {
		return nil, unavailable(errors.New("private storage not configured"))
	}
	ttl := min(DownloadTTL, time.Until(*e.ExpiresAt))
	if ttl < time.Second {
		return nil, Problem(http.StatusConflict, "state_conflict", "Export has expired")
	}
	if err = s.Store.Event(ctx, e.ID, e.VendorID, actor, "export_downloaded", "", middleware.RequestIDFromContext(ctx), nil); err != nil {
		return nil, storeError(err)
	}
	url, err := s.Files.DownloadFileURL(ctx, e.objectKey, e.Kind+"-"+time.Now().UTC().Format("20060102")+".csv", ttl)
	if err != nil {
		return nil, unavailable(err)
	}
	return &Download{URL: url, ExpiresAt: time.Now().Add(ttl)}, nil
}

// Run drives dry-runs, two row appliers, exports and retention cleanup
// until ctx ends.
func (s *Service) Run(ctx context.Context) {
	var wg sync.WaitGroup
	loop := func(name string, every time.Duration, step func(context.Context) (bool, error)) {
		defer wg.Done()
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			// Drain while there is work, then wait for the next tick.
			for range 200 {
				stepCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
				more, err := step(stepCtx)
				cancel()
				if err != nil {
					s.Log.Error().Err(err).Str("loop", name).Msg("bulk_import_step_failed")
					break
				}
				if !more || ctx.Err() != nil {
					break
				}
			}
		}
	}
	if s.Files != nil {
		wg.Add(5)
		go loop("validate", 2*time.Second, s.ValidateNext)
		go loop("apply-1", time.Second, s.ApplyNext)
		go loop("apply-2", time.Second, s.ApplyNext)
		go loop("export", 3*time.Second, s.ExportNext)
		go loop("cleanup", time.Minute, s.CleanupNext)
	}
	wg.Wait()
}

func backoff(attempt int) time.Duration {
	d := time.Duration(1<<min(attempt, 6)) * 5 * time.Second
	return d + time.Duration(rand.Int64N(int64(d/4)+1))
}

func isNoRows(err error) bool { return errors.Is(err, errNotFound) }

// ValidateNext runs the dry-run of one uploaded job.
func (s *Service) ValidateNext(ctx context.Context) (bool, error) {
	j, err := s.Store.ClaimValidation(ctx)
	if isNoRows(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	rows, header, failErr := s.plan(ctx, j)
	if failErr != nil {
		var app *apperror.Error
		if errors.As(failErr, &app) && app.Code == "file_invalid" {
			return true, s.Store.FailValidation(ctx, j, StatusFileInvalid, "file_invalid", app.Message, 0)
		}
		s.Log.Warn().Err(failErr).Str("job_id", j.ID).Int("attempt", j.attempts).Msg("import_validation_attempt_failed")
		if j.attempts >= MaxApplyAttempts {
			return true, s.Store.FailValidation(ctx, j, StatusFailed, "validation_unavailable", "The file could not be checked; upload it again later", 0)
		}
		return true, s.Store.FailValidation(ctx, j, "", "", "", backoff(j.attempts))
	}
	hash := previewHash(rows)
	dup, err := s.Store.DuplicateFile(ctx, j.VendorID, j.Kind, j.FileHash, j.ID)
	if err != nil {
		return true, err
	}
	err = s.Store.SavePreview(ctx, j, header, rows, hash, dup)
	if errors.Is(err, errLeaseLost) {
		return true, nil
	}
	return true, err
}

func (s *Service) plan(ctx context.Context, j *Job) ([]plannedRow, []string, error) {
	k, err := s.kind(j.Kind)
	if err != nil {
		return nil, nil, err
	}
	t := k.Template()
	if t.Version != j.TemplateVersion {
		return nil, nil, FileInvalid(fmt.Sprintf("Template %s changed to version %d; download it again", t.Kind, t.Version))
	}
	rc, err := s.Files.Open(ctx, j.objectKey)
	if err != nil {
		return nil, nil, err
	}
	parsed, err := Parse(rc, t, s.MaxRows, s.MaxBytes)
	_ = rc.Close()
	if err != nil {
		return nil, nil, err
	}
	plans := make([]Plan, len(parsed.Records))
	var checked []Record
	var index []int
	for i, rec := range parsed.Records {
		if e := CheckCells(rec, t); e != nil {
			plans[i] = Plan{Error: e}
			continue
		}
		checked = append(checked, rec)
		index = append(index, i)
	}
	if len(checked) > 0 {
		out, err := k.Plan(ctx, j.VendorID, checked)
		if err != nil {
			return nil, nil, err
		}
		if len(out) != len(checked) {
			return nil, nil, errors.New("bulkimport: plan count mismatch")
		}
		for n, p := range out {
			plans[index[n]] = p
		}
	}
	markDuplicates(parsed.Records, plans)
	rows := make([]plannedRow, len(plans))
	for i, p := range plans {
		rec := parsed.Records[i]
		r := plannedRow{RowNo: rec.RowNo, Raw: parsed.Raw[rec.RowNo], TargetKey: p.TargetKey, Old: p.Old, New: p.New,
			OperationRef: fmt.Sprintf("import:%s:%d", j.ID, rec.RowNo), Payload: json.RawMessage("null")}
		switch {
		case p.Error != nil:
			r.Status = RowInvalid
			r.ErrorCode, r.ErrorField, r.ErrorMessage = &p.Error.Code, &p.Error.Field, &p.Error.Message
		case p.Unchanged:
			r.Status = RowUnchanged
		default:
			r.Status = RowValid
			r.ExpectedVersion = p.ExpectedVersion
		}
		if p.Error == nil {
			b, err := json.Marshal(p.Payload)
			if err != nil {
				return nil, nil, err
			}
			sum := sha256.Sum256(b)
			r.Payload, r.PayloadHash = b, hex.EncodeToString(sum[:])
		}
		rows[i] = r
	}
	return rows, parsed.Header, nil
}

// markDuplicates fails every row whose target appears more than once, so
// the same SKU is never updated or restocked twice from one file.
func markDuplicates(records []Record, plans []Plan) {
	seen := map[string][]int{}
	for i, p := range plans {
		if p.Error == nil && p.TargetKey != "" {
			seen[p.TargetKey] = append(seen[p.TargetKey], i)
		}
	}
	for _, idx := range seen {
		if len(idx) < 2 {
			continue
		}
		rows := make([]string, len(idx))
		for n, i := range idx {
			rows[n] = fmt.Sprint(records[i].RowNo)
		}
		for _, i := range idx {
			plans[i] = Plan{TargetKey: plans[i].TargetKey, Error: &RowError{Code: "duplicate_target", Message: "The same product/SKU appears in rows " + strings.Join(rows, ", ") + "; keep one row per target"}}
		}
	}
}

func previewHash(rows []plannedRow) string {
	sorted := append([]plannedRow(nil), rows...)
	sort.Slice(sorted, func(a, b int) bool { return sorted[a].RowNo < sorted[b].RowNo })
	h := sha256.New()
	for _, r := range sorted {
		v := int64(0)
		if r.ExpectedVersion != nil {
			v = *r.ExpectedVersion
		}
		fmt.Fprintf(h, "%d|%s|%s|%s|%d\n", r.RowNo, r.Status, r.TargetKey, r.PayloadHash, v)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ApplyNext applies one row through the owner's use case, in the same
// transaction that records its result.
func (s *Service) ApplyNext(ctx context.Context) (bool, error) {
	c, err := s.Store.ClaimRow(ctx)
	if isNoRows(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	k, err := s.kind(c.Kind)
	if err != nil {
		return true, s.finish(ctx, c, RowFailed, "", &RowError{Code: "unknown_kind", Message: "Unknown import template"})
	}
	// The row's operation reference is the request id of the owner's audit.
	applyCtx := middleware.ContextWithRequestID(ctx, c.OperationRef)
	var res Result
	err = s.Store.Transact(applyCtx, func(ctx context.Context) error {
		if err := s.Store.LockRow(ctx, c); err != nil {
			return err
		}
		var err error
		res, err = k.Apply(ctx, c.Actor, c.VendorID, c.Row)
		if err != nil {
			return err
		}
		status := RowSucceeded
		if res.NeedsApproval {
			status = RowNeedsApproval
		}
		return s.Store.FinishRow(ctx, c, status, res.Ref, nil)
	})
	if errors.Is(err, errLeaseLost) {
		return true, nil
	}
	if err == nil {
		return true, s.Store.FinalizeJob(ctx, c.JobID)
	}
	var app *apperror.Error
	switch {
	case errors.As(err, &app) && app.Status == http.StatusForbidden:
		e := &RowError{Code: "permission_revoked", Message: "The confirming account can no longer change this shop's data, or the feature was turned off; remaining rows were stopped"}
		if err := s.finish(ctx, c, RowFailed, "", e); err != nil {
			return true, err
		}
		if err := s.Store.StopJob(ctx, c.JobID, e.Code, e.Message); err != nil {
			return true, err
		}
		if err := s.Store.Event(ctx, c.JobID, c.VendorID, "", "stopped", app.Message, c.OperationRef, map[string]any{"row_no": c.RowNo}); err != nil {
			return true, err
		}
		return true, s.Store.FinalizeJob(ctx, c.JobID)
	case errors.As(err, &app) && app.Status < 500:
		return true, s.finish(ctx, c, RowFailed, "", &RowError{Code: string(app.Code), Message: app.Message})
	case c.Attempts >= MaxApplyAttempts:
		s.Log.Error().Err(err).Str("job_id", c.JobID).Int("row_no", c.RowNo).Msg("import_row_parked")
		return true, s.finish(ctx, c, RowFailed, "", &RowError{Code: "apply_unavailable", Message: "The row could not be applied after several attempts; nothing was changed for it"})
	default:
		s.Log.Warn().Err(err).Str("job_id", c.JobID).Int("row_no", c.RowNo).Int("attempt", c.Attempts).Msg("import_row_retry")
		return true, s.Store.RetryRow(ctx, c, backoff(c.Attempts))
	}
}

func (s *Service) finish(ctx context.Context, c *claimedRow, status, ref string, e *RowError) error {
	if err := s.Store.FinishRow(ctx, c, status, ref, e); err != nil {
		return err
	}
	return s.Store.FinalizeJob(ctx, c.JobID)
}

// ExportNext writes one queued export to private storage.
func (s *Service) ExportNext(ctx context.Context) (bool, error) {
	e, err := s.Store.ClaimExport(ctx)
	if isNoRows(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	k, err := s.kind(e.Kind)
	if err != nil {
		return true, s.Store.FinishExport(ctx, e, 0, "unknown_kind", 0)
	}
	// Recheck the requester before reading the shop's data.
	if err = s.Authorize(ctx, e.RequestedBy, e.VendorID, false); err != nil {
		var app *apperror.Error
		if errors.As(err, &app) && app.Status < 500 {
			return true, s.Store.FinishExport(ctx, e, 0, "permission_revoked", 0)
		}
		return true, s.Store.FinishExport(ctx, e, 0, "export_unavailable", backoff(e.attempts))
	}
	n, err := s.writeExport(ctx, k, e)
	if err != nil {
		var app *apperror.Error
		if errors.As(err, &app) && app.Status < 500 {
			return true, s.Store.FinishExport(ctx, e, 0, string(app.Code), 0)
		}
		s.Log.Warn().Err(err).Str("export_id", e.ID).Msg("import_export_attempt_failed")
		return true, s.Store.FinishExport(ctx, e, 0, "export_unavailable", backoff(e.attempts))
	}
	return true, s.Store.FinishExport(ctx, e, n, "", 0)
}

func (s *Service) writeExport(ctx context.Context, k Kind, e *Export) (int, error) {
	t := k.Template()
	header := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		header[i] = c.Name
	}
	r, w := io.Pipe()
	done := make(chan error, 1)
	rows := 0
	go func() {
		err := func() error {
			cw, err := NewCSVWriter(w)
			if err != nil {
				return err
			}
			if err = cw.Write(header); err != nil {
				return err
			}
			err = k.Export(ctx, e.VendorID, func(cells []string) error {
				if rows++; rows > MaxExportRows {
					return Problem(http.StatusUnprocessableEntity, "export_too_large", fmt.Sprintf("The shop has more than %d rows", MaxExportRows))
				}
				return cw.Write(cells)
			})
			if err != nil {
				return err
			}
			return cw.Flush()
		}()
		_ = w.CloseWithError(err)
		done <- err
	}()
	err := s.Files.PutStream(ctx, e.objectKey, r, "text/csv")
	_ = r.CloseWithError(err)
	if streamErr := <-done; streamErr != nil {
		return 0, streamErr
	}
	return rows, err
}

// CleanupNext deletes originals after 7 days and reports/exports after 24
// hours. Job summaries and events stay.
func (s *Service) CleanupNext(ctx context.Context) (bool, error) {
	files, reports, err := s.Store.ExpiredFiles(ctx, 50)
	if err != nil {
		return false, err
	}
	exports, err := s.Store.ExpiredExports(ctx, 50)
	if err != nil {
		return false, err
	}
	for _, f := range files {
		if err = s.Files.Delete(ctx, f.Key); err != nil {
			return false, err
		}
		if err = s.Store.MarkFileDeleted(ctx, f.ID); err != nil {
			return false, err
		}
	}
	for _, f := range reports {
		if err = s.Files.Delete(ctx, f.Key); err != nil {
			return false, err
		}
		if err = s.Store.ClearReport(ctx, f.ID, f.Key); err != nil {
			return false, err
		}
	}
	for _, f := range exports {
		if err = s.Files.Delete(ctx, f.Key); err != nil {
			return false, err
		}
		if err = s.Store.MarkExportExpired(ctx, f.ID); err != nil {
			return false, err
		}
	}
	return len(files)+len(reports)+len(exports) == 50*3, nil
}
