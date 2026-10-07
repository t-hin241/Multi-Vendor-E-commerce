package domain

import (
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"shopee/backend/pkg/apperror"
)

// SupportCaseStatus is where a support case stands. A case never moves
// money or stock itself: a resolution that needs either links a refund or a
// return and waits for its outcome (resolution_pending).
type SupportCaseStatus string

const (
	CaseOpen              SupportCaseStatus = "open"
	CaseInProgress        SupportCaseStatus = "in_progress"
	CaseWaitingBuyer      SupportCaseStatus = "waiting_buyer"
	CaseWaitingVendor     SupportCaseStatus = "waiting_vendor"
	CaseResolutionPending SupportCaseStatus = "resolution_pending"
	CaseResolved          SupportCaseStatus = "resolved"
	CaseClosed            SupportCaseStatus = "closed"
)

// SupportCaseStatuses lists every status, for filter validation.
var SupportCaseStatuses = []SupportCaseStatus{CaseOpen, CaseInProgress, CaseWaitingBuyer, CaseWaitingVendor,
	CaseResolutionPending, CaseResolved, CaseClosed}

// caseTransitions is the only place a case's lifecycle is defined. A case
// is picked up (assigned) before anything else happens to it; only a
// refund/return outcome leaves resolution_pending; only a buyer reopen or
// a close leaves resolved.
var caseTransitions = map[SupportCaseStatus][]SupportCaseStatus{
	CaseOpen:              {CaseInProgress},
	CaseInProgress:        {CaseWaitingBuyer, CaseWaitingVendor, CaseResolutionPending, CaseResolved},
	CaseWaitingBuyer:      {CaseInProgress, CaseWaitingVendor, CaseResolutionPending, CaseResolved},
	CaseWaitingVendor:     {CaseInProgress, CaseWaitingBuyer, CaseResolutionPending, CaseResolved},
	CaseResolutionPending: {CaseResolved, CaseInProgress},
	CaseResolved:          {CaseInProgress, CaseClosed},
}

func CanTransitionCase(from, to SupportCaseStatus) bool {
	for _, allowed := range caseTransitions[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

// SupportCategory is what the buyer reports.
type SupportCategory string

const (
	CategoryNotReceived  SupportCategory = "not_received"
	CategoryMissingItems SupportCategory = "missing_items"
	CategoryWrongItems   SupportCategory = "wrong_items"
	CategoryDamaged      SupportCategory = "damaged"
	CategoryPaymentIssue SupportCategory = "payment_issue"
	CategoryOther        SupportCategory = "other"
)

// AffectsMoney reports whether a case of this category may end in a refund,
// so the vendor order's payout is held while the case is not closed.
func (c SupportCategory) AffectsMoney() bool {
	return c != CategoryOther
}

func ParseSupportCategory(s string) (SupportCategory, error) {
	switch c := SupportCategory(s); c {
	case CategoryNotReceived, CategoryMissingItems, CategoryWrongItems, CategoryDamaged, CategoryPaymentIssue, CategoryOther:
		return c, nil
	}
	return "", apperror.Validation("category must be not_received, missing_items, wrong_items, damaged, payment_issue or other")
}

// CheckCaseEligibility: a payment question may be raised on any order; the
// other categories are about goods the buyer paid for.
func CheckCaseEligibility(category SupportCategory, vendorOrderStatus Status) error {
	if category == CategoryPaymentIssue || vendorOrderStatus.PaidOrFurther() {
		return nil
	}
	return apperror.Conflict("Only a paid order can have this kind of support request; choose payment_issue for a payment question")
}

// Resolution kinds an admin may record.
const (
	ResolutionNoAction = "no_action"
	ResolutionRefund   = "refund"
	ResolutionReturn   = "return"
)

// Message visibilities. internal is admin-only.
const (
	VisibilityPublic   = "public"
	VisibilityInternal = "internal"
)

type SupportCase struct {
	ActionDueAt    *time.Time
	WaitingOn      string
	ID             string
	OrderID        string
	VendorOrderID  string
	VendorID       string
	BuyerID        string
	Category       SupportCategory
	Status         SupportCaseStatus
	AssigneeID     *string
	PolicyVersion  string
	DueAt          *time.Time
	FinancialHold  bool
	ResolutionKind *string
	ResolutionRef  *string
	ResolutionNote *string
	ResolvedAt     *time.Time
	ClosedAt       *time.Time
	RelatedCaseID  *string
	IdempotencyKey *string
	RequestHash    *string
	Version        int64
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// SupportMessage is immutable once written.
type SupportMessage struct {
	ID             string
	CaseID         string
	AuthorID       string
	AuthorRole     string
	Visibility     string
	Text           string
	IdempotencyKey *string
	RequestHash    *string
	CreatedAt      time.Time
	Attachments    []*CaseAttachment
}

// Attachment states.
const (
	AttachmentUploaded = "uploaded"
	AttachmentAttached = "attached"
	AttachmentDeleted  = "deleted"
)

// CaseAttachment is an evidence image in the private bucket. Its object key
// never leaves Order; clients read it through the case API.
type CaseAttachment struct {
	ID          string
	OwnerID     string
	CaseID      *string
	MessageID   *string
	ObjectKey   string
	ContentType string
	SizeBytes   int64
	State       string
	CreatedAt   time.Time
}

// SupportCaseEvent is one step of a case's timeline.
type SupportCaseEvent struct {
	ID         string
	CaseID     string
	ActorID    *string
	ActorRole  string
	Action     string
	FromStatus *string
	ToStatus   string
	Note       *string
	CreatedAt  time.Time
}

// SupportPolicy is the support rule in force when a case is opened; its
// version is copied onto the case.
type SupportPolicy struct {
	Version string
	// ResponseTime is how long the marketplace side has to act on a case
	// that waits for it (open, in progress, waiting for the vendor).
	ResponseTime time.Duration
	// ReopenWindow is how long after resolution the buyer may reopen; the
	// case is then closed and a new case must link to it.
	ReopenWindow       time.Duration
	MaxMessageChars    int
	MaxAttachments     int
	MaxAttachmentBytes int64
	MaxImagePixels     int
}

func DefaultSupportPolicy() SupportPolicy {
	return SupportPolicy{Version: "support-v1", ResponseTime: 48 * time.Hour, ReopenWindow: 7 * 24 * time.Hour,
		MaxMessageChars: 4000, MaxAttachments: 5, MaxAttachmentBytes: 5 << 20, MaxImagePixels: 40_000_000}
}

// DueAt is the deadline for the next step in status: set while the
// marketplace side must act, none while waiting for the buyer or for a
// refund/return outcome, or once resolved.
func (p SupportPolicy) DueAt(status SupportCaseStatus, now time.Time) *time.Time {
	switch status {
	case CaseOpen, CaseInProgress, CaseWaitingVendor:
		due := now.Add(p.ResponseTime).UTC()
		return &due
	}
	return nil
}

// CanReopen: a resolved case may be reopened by its buyer within the window.
func (p SupportPolicy) CanReopen(c *SupportCase, now time.Time) error {
	if c.Status != CaseResolved || c.ResolvedAt == nil {
		return apperror.Conflict("Only a resolved case can be reopened")
	}
	if now.After(c.ResolvedAt.Add(p.ReopenWindow)) {
		return apperror.Conflict("The reopen window for this case has ended; open a new case linked to it")
	}
	return nil
}

// ReopenExpired reports whether a resolved case can no longer be reopened
// and may be closed.
func (p SupportPolicy) ReopenExpired(c *SupportCase, now time.Time) bool {
	return c.Status == CaseResolved && c.ResolvedAt != nil && now.After(c.ResolvedAt.Add(p.ReopenWindow))
}

// markupPattern finds an HTML tag, comment or declaration start ("<p",
// "</", "<!", "<?"); a lone "<" in prose ("< 5 days") is fine.
var markupPattern = regexp.MustCompile(`<[A-Za-z/!?]`)

// ValidateSupportText trims a message and refuses empty, over-long, markup
// or control-character text. Text is stored and shown as plain text.
func (p SupportPolicy) ValidateSupportText(text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", apperror.Validation("message is required")
	}
	if !utf8.ValidString(text) || utf8.RuneCountInString(text) > p.MaxMessageChars {
		return "", apperror.Validation("message must be at most 4000 characters")
	}
	if markupPattern.MatchString(text) {
		return "", apperror.Validation("message must be plain text; HTML is not accepted")
	}
	for _, r := range text {
		if unicode.IsControl(r) && r != '\n' && r != '\t' && r != '\r' {
			return "", apperror.Validation("message contains invalid characters")
		}
	}
	return text, nil
}

// Error codes of the support case API.
const (
	CodeCaseAlreadyOpen         apperror.Code = "case_already_open"
	CodeVersionConflict         apperror.Code = "version_conflict"
	CodeSupportDisabled         apperror.Code = "support_cases_disabled"
	CodeUnsupportedAttachment   apperror.Code = "unsupported_attachment"
	CodeAttachmentsUnavailable  apperror.Code = "attachments_unavailable"
	CodeSupportKeyReused        apperror.Code = "idempotency_key_reused"
	CodeSupportOperationInvalid apperror.Code = "invalid_linked_operation"
)

// CaseAlreadyOpen: the buyer already has a case not yet closed for this
// vendor order and category.
func CaseAlreadyOpen() *apperror.Error {
	return coded(CodeCaseAlreadyOpen, http.StatusConflict, "You already have an open support case for this shop's order and topic")
}

// CaseVersionConflict: the case changed since the caller read it.
func CaseVersionConflict() *apperror.Error {
	return coded(CodeVersionConflict, http.StatusConflict, "This case changed since you loaded it; reload and try again")
}

// SupportDisabled: new cases are not accepted (feature off or shop not in
// the pilot). Existing cases stay readable and answerable.
func SupportDisabled() *apperror.Error {
	return coded(CodeSupportDisabled, http.StatusForbidden, "Support requests are not available for this order yet")
}

// UnsupportedAttachment: the file is not a JPEG or PNG image within limits.
func UnsupportedAttachment(message string) *apperror.Error {
	return coded(CodeUnsupportedAttachment, http.StatusUnprocessableEntity, message)
}

// AttachmentsUnavailable: private storage is not configured or not reachable.
func AttachmentsUnavailable() *apperror.Error {
	return coded(CodeAttachmentsUnavailable, http.StatusServiceUnavailable, "Attachments are not available right now")
}

// SupportKeyReused: the Idempotency-Key was used for a different request.
func SupportKeyReused() *apperror.Error {
	return coded(CodeSupportKeyReused, http.StatusConflict, "This Idempotency-Key was already used for a different request")
}

// InvalidLinkedOperation: the refund/return does not belong to this case's
// vendor order or can no longer resolve it.
func InvalidLinkedOperation(message string) *apperror.Error {
	return coded(CodeSupportOperationInvalid, http.StatusUnprocessableEntity, message)
}
