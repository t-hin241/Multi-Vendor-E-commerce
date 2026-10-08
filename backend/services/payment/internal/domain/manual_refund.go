package domain

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"shopee/backend/pkg/apperror"
)

// AF-06: a refund paid by a manual bank transfer. The buyer gives the
// destination, finance verifies it, an operator claims an attempt and
// transfers outside the app, records the bank reference, and a reviewer
// confirms. Only a confirmed attempt makes the refund succeeded.

type DestinationStatus string

const (
	DestinationPending    DestinationStatus = "pending_verification"
	DestinationVerified   DestinationStatus = "verified"
	DestinationRejected   DestinationStatus = "rejected"
	DestinationSuperseded DestinationStatus = "superseded"
)

// RefundDestination is one version of where a buyer wants the money. The
// account number and holder name exist only as ciphertext; BankCode and
// AccountLast4 are what screens show.
type RefundDestination struct {
	ID             string
	RefundID       string
	Version        int
	BankCode       string
	AccountLast4   string
	Ciphertext     []byte
	KeyVersion     int
	Status         DestinationStatus
	SubmittedBy    string
	SubmittedAt    time.Time
	DecidedBy      *string
	DecidedAt      *time.Time
	DecisionReason *string
}

// Masked is the destination as screens and notifications show it.
func (d *RefundDestination) Masked() string { return d.BankCode + " ••••" + d.AccountLast4 }

// Beneficiary is the plaintext a buyer submits; it is only ever encrypted
// or shown once through an audited sensitive access.
type Beneficiary struct {
	BankCode      string `json:"bank_code"`
	AccountNumber string `json:"account_number"`
	AccountName   string `json:"account_name"`
}

var (
	bankCodeFormat      = regexp.MustCompile(`^[A-Z0-9]{2,20}$`)
	accountNumberFormat = regexp.MustCompile(`^[0-9]{6,20}$`)
)

// NormalizeBeneficiary validates a submitted destination: bank code
// letters/digits, account number 6-20 digits (spaces and dashes dropped),
// holder name 2-100 letters, upper-cased the way banks print it.
func NormalizeBeneficiary(b Beneficiary) (Beneficiary, error) {
	b.BankCode = strings.ToUpper(strings.TrimSpace(b.BankCode))
	if !bankCodeFormat.MatchString(b.BankCode) {
		return Beneficiary{}, apperror.Validation("Bank code must be 2-20 letters or digits")
	}
	b.AccountNumber = strings.NewReplacer(" ", "", "-", "", ".", "").Replace(strings.TrimSpace(b.AccountNumber))
	if !accountNumberFormat.MatchString(b.AccountNumber) {
		return Beneficiary{}, apperror.Validation("Account number must be 6-20 digits")
	}
	name := strings.Join(strings.Fields(b.AccountName), " ")
	if n := len([]rune(name)); n < 2 || n > 100 {
		return Beneficiary{}, apperror.Validation("Account holder name must be 2-100 characters")
	}
	for _, r := range name {
		if !unicode.IsLetter(r) && r != ' ' {
			return Beneficiary{}, apperror.Validation("Account holder name may only contain letters and spaces")
		}
	}
	b.AccountName = strings.ToUpper(name)
	return b, nil
}

// Last4 is what is kept in clear for display.
func (b Beneficiary) Last4() string { return b.AccountNumber[len(b.AccountNumber)-4:] }

// DestinationAAD binds a ciphertext to its refund and version, so a
// ciphertext copied onto another refund or version fails to decrypt.
func DestinationAAD(refundID string, version int) string {
	return "refund_destination:" + refundID + ":v" + strconv.Itoa(version)
}

type AttemptStage string

const (
	AttemptReady     AttemptStage = "ready"
	AttemptExecuting AttemptStage = "executing"
	AttemptSubmitted AttemptStage = "submitted"
	AttemptConfirmed AttemptStage = "confirmed"
	AttemptFailed    AttemptStage = "failed"
	// AttemptUnknown: a claim ran out while the operator may have sent the
	// money. Nobody may start another transfer until the statement is
	// checked and the attempt submitted or failed.
	AttemptUnknown AttemptStage = "unknown"
	// AttemptVoided: closed before any money left.
	AttemptVoided AttemptStage = "voided"
)

// Active attempts block another one on the same refund.
func (s AttemptStage) Active() bool {
	return s == AttemptReady || s == AttemptExecuting || s == AttemptSubmitted || s == AttemptUnknown
}

// ManualRefundAttempt is one try at transferring a refund by hand.
type ManualRefundAttempt struct {
	ID                 string
	RefundID           string
	DestinationID      string
	DestinationVersion int
	Amount             int64
	Currency           string
	Stage              AttemptStage
	Version            int
	PreparedBy         string
	PrepareReason      string
	ClaimedBy          *string
	ClaimedAt          *time.Time
	LeaseExpiresAt     *time.Time
	SourceAccount      *string
	BankReference      *string
	BankReferenceKey   *string
	ExecutedAt         *time.Time
	SubmittedBy        *string
	SubmittedAt        *time.Time
	DecidedBy          *string
	DecidedAt          *time.Time
	DecisionReason     *string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

func manualError(status int, code apperror.Code, message string) *apperror.Error {
	return &apperror.Error{Code: code, Message: message, Status: status}
}

var (
	ErrAttemptActive           = manualError(http.StatusConflict, "attempt_active", "A manual transfer for this refund is already in progress")
	ErrDestinationChanged      = manualError(http.StatusConflict, "destination_changed", "The refund destination changed; reload and try again")
	ErrDestinationNotVerified  = manualError(http.StatusConflict, "destination_not_verified", "The refund destination is not verified yet")
	ErrAttemptChanged          = manualError(http.StatusConflict, "stale_attempt", "This transfer attempt changed meanwhile; reload and try again")
	ErrNotClaimOwner           = manualError(http.StatusForbidden, "not_claim_owner", "Only the admin who claimed this transfer can do this")
	ErrManualSelfApproval      = manualError(http.StatusConflict, "self_approval", "The admin who executed or submitted a transfer cannot confirm it")
	ErrDuplicateBankReference  = manualError(http.StatusConflict, "duplicate_bank_reference", "This bank reference is already recorded for another transfer; check the statement")
	ErrManualWorkflowRequired  = manualError(http.StatusConflict, "manual_workflow_required", "Record this refund through the manual transfer workflow")
	ErrManualWorkflowOff       = manualError(http.StatusNotFound, "feature_disabled", "The manual refund workflow is not enabled")
	ErrDestinationKeyMissing   = manualError(http.StatusServiceUnavailable, "destination_key_unavailable", "Refund destinations cannot be read or stored right now")
	ErrEvidenceStorageDisabled = manualError(http.StatusServiceUnavailable, "evidence_storage_unavailable", "Evidence uploads are not available")
)

// ValidateReason trims and checks an operator reason.
func ValidateReason(reason string) (string, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > 500 {
		return "", apperror.Validation("A reason of 1-500 characters is required")
	}
	return reason, nil
}

// Claim gives the attempt to one operator for the lease.
func (a *ManualRefundAttempt) Claim(actor string, now time.Time, lease time.Duration) error {
	if a.Stage != AttemptReady {
		return manualError(http.StatusConflict, "stale_attempt", "Only a ready transfer can be claimed; it is "+string(a.Stage))
	}
	expires := now.Add(lease)
	a.Stage, a.ClaimedBy, a.ClaimedAt, a.LeaseExpiresAt = AttemptExecuting, &actor, &now, &expires
	return nil
}

// LeaseExpired reports an executing attempt whose claim ran out.
func (a *ManualRefundAttempt) LeaseExpired(now time.Time) bool {
	return a.Stage == AttemptExecuting && a.LeaseExpiresAt != nil && !now.Before(*a.LeaseExpiresAt)
}

// Cancel closes an attempt before money left: a ready one by any
// preparer, an executing one only by its claimer, who states that nothing
// was sent.
func (a *ManualRefundAttempt) Cancel(actor, reason string, now time.Time) error {
	switch a.Stage {
	case AttemptReady:
	case AttemptExecuting:
		if a.ClaimedBy == nil || *a.ClaimedBy != actor {
			return ErrNotClaimOwner
		}
		if a.LeaseExpired(now) {
			return manualError(http.StatusConflict, "lease_expired", "The claim ran out; check the bank statement and record the transfer or mark it failed")
		}
	default:
		return manualError(http.StatusConflict, "stale_attempt", "A "+string(a.Stage)+" transfer cannot be cancelled")
	}
	a.Stage, a.DecisionReason = AttemptVoided, &reason
	return nil
}

// Submission is the operator's record of a transfer that left the bank.
type Submission struct {
	BankReference string
	SourceAccount string
	ExecutedAt    time.Time
}

var referenceKeyCleaner = strings.NewReplacer(" ", "", "-", "", ".", "", "/", "", "_", "")

// BankReferenceKey normalises a reference for the duplicate check.
func BankReferenceKey(ref string) string {
	return strings.ToUpper(referenceKeyCleaner.Replace(strings.TrimSpace(ref)))
}

// Submit records the transfer. The claimer submits an executing attempt;
// for an unknown one (claim ran out) any preparer who found the transfer
// on the statement may.
func (a *ManualRefundAttempt) Submit(actor string, s Submission, now time.Time) error {
	ref := strings.TrimSpace(s.BankReference)
	source := strings.TrimSpace(s.SourceAccount)
	key := BankReferenceKey(ref)
	if len(ref) < 3 || len(ref) > 100 || len(key) < 3 {
		return apperror.Validation("Bank reference must be 3-100 characters")
	}
	if len(source) < 2 || len(source) > 64 {
		return apperror.Validation("Source account must be 2-64 characters")
	}
	if s.ExecutedAt.IsZero() || s.ExecutedAt.After(now.Add(5*time.Minute)) || s.ExecutedAt.Before(a.CreatedAt.Add(-time.Minute)) {
		return apperror.Validation("Executed time must be after the attempt was prepared and not in the future")
	}
	switch a.Stage {
	case AttemptExecuting:
		if a.ClaimedBy == nil || *a.ClaimedBy != actor {
			return ErrNotClaimOwner
		}
	case AttemptUnknown:
	default:
		return manualError(http.StatusConflict, "stale_attempt", "A "+string(a.Stage)+" transfer cannot be submitted")
	}
	executed := s.ExecutedAt.UTC()
	a.Stage, a.BankReference, a.BankReferenceKey, a.SourceAccount, a.ExecutedAt = AttemptSubmitted, &ref, &key, &source, &executed
	a.SubmittedBy, a.SubmittedAt = &actor, &now
	return nil
}

// Decide confirms a submitted transfer or fails a submitted/unknown one.
// With twoPerson, the reviewer is neither the claimer nor the submitter.
func (a *ManualRefundAttempt) Decide(actor string, confirm bool, reason string, now time.Time, twoPerson bool) error {
	if twoPerson && ((a.ClaimedBy != nil && *a.ClaimedBy == actor) || (a.SubmittedBy != nil && *a.SubmittedBy == actor)) {
		return ErrManualSelfApproval
	}
	switch {
	case confirm && a.Stage != AttemptSubmitted:
		return manualError(http.StatusConflict, "stale_attempt", "Only a submitted transfer can be confirmed")
	case !confirm && a.Stage != AttemptSubmitted && a.Stage != AttemptUnknown:
		return manualError(http.StatusConflict, "stale_attempt", "A "+string(a.Stage)+" transfer cannot be marked failed")
	}
	a.Stage = AttemptFailed
	if confirm {
		a.Stage = AttemptConfirmed
	}
	a.DecidedBy, a.DecidedAt, a.DecisionReason = &actor, &now, &reason
	return nil
}

// EvidenceReference is what the succeeded refund records as its proof.
func (a *ManualRefundAttempt) EvidenceReference() string {
	return "bank:" + deref(a.SourceAccount) + ":" + deref(a.BankReference)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// Proof purposes and operations a manual refund step-up binds to (AF-19).
const (
	ProofPurposeDestinationDecide = "payment.refund_destination.decide"
	ProofPurposeDestinationReveal = "payment.refund_destination.reveal"
	ProofPurposeAttemptDecide     = "payment.refund_attempt.decide"
)

// DestinationDecisionRef names one verify/reject of one destination version.
func DestinationDecisionRef(refundID string, version int, verify bool) string {
	return "refund_destination:" + refundID + ":v" + strconv.Itoa(version) + ":" + pick(verify, "verify", "reject")
}

// DestinationRevealRef names reading one destination version in full.
func DestinationRevealRef(refundID string, version int) string {
	return "refund_destination:" + refundID + ":v" + strconv.Itoa(version) + ":reveal"
}

// AttemptDecisionRef names one confirm/fail of one attempt version.
func AttemptDecisionRef(attemptID string, version int, confirm bool) string {
	return "refund_attempt:" + attemptID + ":v" + strconv.Itoa(version) + ":" + pick(confirm, "confirm", "fail")
}

func pick(b bool, yes, no string) string {
	if b {
		return yes
	}
	return no
}

// ManualStage is where a refund stands in the manual workflow, for the
// admin queue.
func ManualStage(r *Refund, dest *RefundDestination, attempt *ManualRefundAttempt) string {
	switch {
	case r.Status == RefundSucceeded:
		return "confirmed"
	case r.Status == RefundFailed:
		return "failed"
	case attempt != nil && attempt.Stage.Active():
		return string(attempt.Stage)
	case dest == nil || dest.Status == DestinationRejected:
		return "awaiting_destination"
	case dest.Status == DestinationPending:
		return "verifying"
	}
	return "ready"
}

// BuyerStage is the same, in the words a buyer is shown: no promise that
// money is back before a reviewer confirmed the transfer.
func BuyerStage(r *Refund, dest *RefundDestination, attempt *ManualRefundAttempt) string {
	switch stage := ManualStage(r, dest, attempt); stage {
	case "confirmed":
		return "refunded"
	case "failed":
		return "failed"
	case "awaiting_destination":
		if dest != nil && dest.Status == DestinationRejected {
			return "destination_rejected"
		}
		return "awaiting_destination"
	case "verifying":
		return "verifying"
	}
	return "processing"
}

// EvidenceContentType sniffs an uploaded transfer receipt: JPEG, PNG or
// PDF by their magic bytes, whatever the client claimed.
func EvidenceContentType(data []byte) (string, error) {
	switch {
	case len(data) == 0:
		return "", apperror.Validation("The file is empty")
	case len(data) > MaxEvidenceBytes:
		return "", manualError(http.StatusRequestEntityTooLarge, "payload_too_large", "Evidence files are at most 5 MiB")
	case len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF:
		return "image/jpeg", nil
	case len(data) >= 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n":
		return "image/png", nil
	case len(data) >= 5 && string(data[:5]) == "%PDF-":
		return "application/pdf", nil
	}
	return "", manualError(http.StatusUnprocessableEntity, "unsupported_file", "Evidence must be a JPEG, PNG or PDF file")
}

// MaxEvidenceBytes bounds one evidence file.
const MaxEvidenceBytes = 5 << 20

// RefundEvidence is a transfer receipt stored in the private bucket.
type RefundEvidence struct {
	ID          string
	AttemptID   string
	ObjectKey   string
	ContentType string
	SizeBytes   int
	SHA256      string
	UploadedBy  string
	State       string
	CreatedAt   time.Time
}
