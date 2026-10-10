package usecase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/adminaccess"
	"shopee/backend/pkg/apperror"
	"shopee/backend/services/payment/internal/domain"
	"shopee/backend/services/payment/internal/repository"
)

// ManualRefundStore persists AF-06 destinations, attempts and evidence.
type ManualRefundStore interface {
	LockRefund(ctx context.Context, id string) (*domain.Refund, error)
	RefundBuyer(ctx context.Context, refundID string) (string, error)
	BuyerRefunds(ctx context.Context, buyerID string, orderID *string) ([]*domain.Refund, error)
	CurrentDestination(ctx context.Context, refundID string) (*domain.RefundDestination, error)
	LatestDestination(ctx context.Context, refundID string) (*domain.RefundDestination, error)
	Destinations(ctx context.Context, refundID string) ([]*domain.RefundDestination, error)
	AddDestination(ctx context.Context, d *domain.RefundDestination) error
	DecideDestination(ctx context.Context, d *domain.RefundDestination) error
	ActiveAttempt(ctx context.Context, refundID string) (*domain.ManualRefundAttempt, error)
	HasActiveAttempt(ctx context.Context, refundID string) (bool, error)
	FindAttempt(ctx context.Context, id string) (*domain.ManualRefundAttempt, error)
	Attempts(ctx context.Context, refundID string) ([]*domain.ManualRefundAttempt, error)
	CreateAttempt(ctx context.Context, a *domain.ManualRefundAttempt) error
	SaveAttempt(ctx context.Context, a *domain.ManualRefundAttempt, expected int, at time.Time) error
	ExpiredClaims(ctx context.Context, now time.Time, limit int) ([]*domain.ManualRefundAttempt, error)
	AddEvidence(ctx context.Context, e *domain.RefundEvidence) error
	SetEvidenceState(ctx context.Context, id, from, to string) error
	AttachEvidence(ctx context.Context, attemptID string, ids []string) (int, error)
	Evidence(ctx context.Context, attemptID string) ([]*domain.RefundEvidence, error)
	FindEvidence(ctx context.Context, id string) (*domain.RefundEvidence, error)
	OrphanEvidence(ctx context.Context, before time.Time, limit int) ([]*domain.RefundEvidence, error)
	Counts(ctx context.Context, now time.Time, overdue time.Duration) (repository.ManualRefundCounts, error)
	RestageLegacySLA(ctx context.Context, limit int) (int, error)
}

// DestinationCipher seals refund destinations (adapter.DestinationCipher).
type DestinationCipher interface {
	Encrypt(plain []byte, aad string) ([]byte, int, error)
	Decrypt(ciphertext []byte, keyVersion int, aad string) ([]byte, error)
}

// EvidenceStore keeps transfer receipts in a private bucket.
type EvidenceStore interface {
	Put(ctx context.Context, key string, data []byte, contentType string) error
	Open(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
}

// BuyerNoticeQueue queues refund facts for the buyer (PW-009) in the
// caller's transaction (repository.BuyerNotices).
type BuyerNoticeQueue interface {
	DestinationRejected(ctx context.Context, refundID string, version int) error
}

var errEvidenceUnavailable = &apperror.Error{Code: "evidence_storage_unavailable", Status: 503, Message: "Evidence storage is unavailable; try again"}

// ManualRefundUseCase runs AF-06: the buyer gives a destination, finance
// verifies it, an operator claims an attempt, transfers outside the app
// and records the bank reference, and a reviewer confirms. Only the
// confirmation marks the refund succeeded, in one transaction with the
// ledger, Order's outbox and the audit.
type ManualRefundUseCase struct {
	Store    ManualRefundStore
	Refunds  *RefundUseCase
	Audit    AuditRepositoryPort
	Tx       Transactor
	Admins   AdminAuthority
	Cipher   DestinationCipher // nil: destinations cannot be stored or read
	Evidence EvidenceStore     // nil: no evidence uploads
	// Notices, when set, tells the buyer a destination was rejected.
	Notices BuyerNoticeQueue
	// Enabled is FEATURE_MANUAL_REFUND_WORKFLOW_ENABLED: off stops new
	// destinations and attempts; open attempts are still finished.
	Enabled bool
	// StepUp and TwoPerson follow FEATURE_ADMIN_SCOPED_PERMISSIONS_ENABLED
	// (AF-19): password proofs for sensitive steps, and a reviewer other
	// than the executor.
	StepUp    bool
	TwoPerson bool
	Lease     time.Duration
	Now       func() time.Time
	Log       zerolog.Logger
}

func (uc *ManualRefundUseCase) now() time.Time {
	if uc.Now != nil {
		return uc.Now().UTC()
	}
	return time.Now().UTC()
}

func (uc *ManualRefundUseCase) lease() time.Duration {
	if uc.Lease > 0 {
		return uc.Lease
	}
	return 30 * time.Minute
}

func (uc *ManualRefundUseCase) proof(ctx context.Context, proof, actor, purpose, operation string) error {
	if !uc.StepUp {
		return nil
	}
	return uc.Admins.ConsumeProof(ctx, proof, actor, purpose, operation)
}

func manualAppError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, repository.ErrDestinationNotFound):
		return apperror.NotFound("Refund destination not found")
	case errors.Is(err, repository.ErrAttemptNotFound):
		return apperror.NotFound("Transfer attempt not found")
	case errors.Is(err, repository.ErrEvidenceNotFound):
		return apperror.NotFound("Evidence not found")
	case errors.Is(err, repository.ErrDuplicateBankReference):
		return domain.ErrDuplicateBankReference
	case errors.Is(err, repository.ErrAttemptActiveExists):
		return domain.ErrAttemptActive
	case errors.Is(err, repository.ErrStaleState):
		return domain.ErrAttemptChanged
	}
	return asAppError(err)
}

// GuardLegacyResolution keeps the old one-step resolution from bypassing
// the workflow: never while a transfer attempt is open (even with the flag
// rolled back), and never as "succeeded" while the workflow is on. It runs
// in the transaction that locked the refund.
func (uc *ManualRefundUseCase) GuardLegacyResolution(ctx context.Context, r *domain.Refund, res domain.RefundResolution) error {
	active, err := uc.Store.HasActiveAttempt(ctx, r.ID)
	if err != nil {
		return err
	}
	if active {
		return domain.ErrAttemptActive
	}
	if uc.Enabled && res.Outcome == domain.RefundSucceeded {
		return domain.ErrManualWorkflowRequired
	}
	return nil
}

// RefundView is a refund with where it stands in the manual workflow.
type RefundView struct {
	Refund      *domain.Refund
	Destination *domain.RefundDestination
	Attempt     *domain.ManualRefundAttempt
	Stage       string
	BuyerStage  string
	// CanSubmitDestination: the buyer may give (or change) the destination.
	CanSubmitDestination bool
}

func (uc *ManualRefundUseCase) view(ctx context.Context, r *domain.Refund) (*RefundView, error) {
	dest, err := uc.Store.LatestDestination(ctx, r.ID)
	if err != nil && !errors.Is(err, repository.ErrDestinationNotFound) {
		return nil, err
	}
	if dest != nil && dest.Status == domain.DestinationSuperseded {
		dest = nil
	}
	attempt, err := uc.Store.ActiveAttempt(ctx, r.ID)
	if err != nil && !errors.Is(err, repository.ErrAttemptNotFound) {
		return nil, err
	}
	v := &RefundView{Refund: r, Destination: dest, Attempt: attempt, Stage: domain.ManualStage(r, dest, attempt), BuyerStage: domain.BuyerStage(r, dest, attempt)}
	v.CanSubmitDestination = uc.Enabled && uc.Cipher != nil && r.Status.Open() &&
		(attempt == nil || attempt.Stage == domain.AttemptReady)
	return v, nil
}

// BuyerRefunds lists the buyer's refunds (of one order when given).
func (uc *ManualRefundUseCase) BuyerRefunds(ctx context.Context, buyerID string, orderID *string) ([]*RefundView, error) {
	refunds, err := uc.Store.BuyerRefunds(ctx, buyerID, orderID)
	if err != nil {
		return nil, manualAppError(err)
	}
	out := make([]*RefundView, 0, len(refunds))
	for _, r := range refunds {
		v, err := uc.view(ctx, r)
		if err != nil {
			return nil, manualAppError(err)
		}
		out = append(out, v)
	}
	return out, nil
}

// BuyerRefund is one of the buyer's refunds; another buyer's is not found.
func (uc *ManualRefundUseCase) BuyerRefund(ctx context.Context, buyerID, refundID string) (*RefundView, error) {
	owner, err := uc.Store.RefundBuyer(ctx, refundID)
	if err != nil {
		return nil, manualAppError(err)
	}
	if owner != buyerID {
		return nil, apperror.NotFound("Refund not found")
	}
	r, err := uc.Refunds.refunds.FindByID(ctx, refundID)
	if err != nil {
		return nil, manualAppError(err)
	}
	v, err := uc.view(ctx, r)
	return v, manualAppError(err)
}

// SubmitDestination stores the buyer's destination as a new version that
// finance must verify. expectedVersion is the version the buyer saw (0
// for none). A ready attempt on the old destination is voided; while money
// may be moving the destination cannot change.
func (uc *ManualRefundUseCase) SubmitDestination(ctx context.Context, buyerID, refundID string, b domain.Beneficiary, expectedVersion int) (*RefundView, error) {
	if !uc.Enabled {
		return nil, domain.ErrManualWorkflowOff
	}
	if uc.Cipher == nil {
		return nil, domain.ErrDestinationKeyMissing
	}
	b, err := domain.NormalizeBeneficiary(b)
	if err != nil {
		return nil, err
	}
	plain, err := json.Marshal(b)
	if err != nil {
		return nil, apperror.Internal(err)
	}
	var view *RefundView
	err = uc.Tx.Run(ctx, func(ctx context.Context) error {
		owner, err := uc.Store.RefundBuyer(ctx, refundID)
		if err != nil {
			return err
		}
		if owner != buyerID {
			return apperror.NotFound("Refund not found")
		}
		r, err := uc.Store.LockRefund(ctx, refundID)
		if err != nil {
			return err
		}
		if !r.Status.Open() {
			return apperror.Conflict("This refund is already " + string(r.Status))
		}
		current, err := uc.Store.LatestDestination(ctx, refundID)
		seen := 0
		switch {
		case err == nil:
			seen = current.Version
		case !errors.Is(err, repository.ErrDestinationNotFound):
			return err
		}
		if seen != expectedVersion {
			return domain.ErrDestinationChanged
		}
		attempt, err := uc.Store.ActiveAttempt(ctx, refundID)
		switch {
		case errors.Is(err, repository.ErrAttemptNotFound):
		case err != nil:
			return err
		case attempt.Stage != domain.AttemptReady:
			return domain.ErrAttemptActive
		default:
			expected := attempt.Version
			if err := attempt.Cancel(buyerID, "Buyer changed the refund destination", uc.now()); err != nil {
				return err
			}
			if err := uc.Store.SaveAttempt(ctx, attempt, expected, uc.now()); err != nil {
				return err
			}
			if err := uc.Audit.Record(ctx, buyerID, "manual_refund_attempt_voided", "payment_refund", refundID, "attempt "+attempt.ID+": destination changed by the buyer"); err != nil {
				return err
			}
		}
		d := &domain.RefundDestination{RefundID: refundID, BankCode: b.BankCode, AccountLast4: b.Last4(), Status: domain.DestinationPending,
			SubmittedBy: buyerID, SubmittedAt: uc.now()}
		// The version is only known inside AddDestination; encrypt for the
		// next one, which the refund lock guarantees.
		d.Version = seen + 1
		if d.Ciphertext, d.KeyVersion, err = uc.Cipher.Encrypt(plain, domain.DestinationAAD(refundID, d.Version)); err != nil {
			return err
		}
		if err := uc.Store.AddDestination(ctx, d); err != nil {
			return err
		}
		if d.Version != seen+1 {
			return domain.ErrDestinationChanged
		}
		if err := uc.Audit.Record(ctx, buyerID, "refund_destination_submitted", "payment_refund", refundID,
			fmt.Sprintf("v%d %s", d.Version, d.Masked())); err != nil {
			return err
		}
		view, err = uc.view(ctx, r)
		return err
	})
	if err != nil {
		return nil, manualAppError(err)
	}
	uc.Log.Info().Str("payment_refund_id", refundID).Int("destination_version", view.Destination.Version).Msg("refund_destination_submitted")
	return view, nil
}

// ManualDetail is the admin's view of a refund in the manual workflow.
type ManualDetail struct {
	View         *RefundView
	Destinations []*domain.RefundDestination
	Attempts     []*domain.ManualRefundAttempt
	Evidence     map[string][]*domain.RefundEvidence
	Audit        []repository.AuditEntry
}

func (uc *ManualRefundUseCase) Detail(ctx context.Context, refundID string) (*ManualDetail, error) {
	r, err := uc.Refunds.refunds.FindByID(ctx, refundID)
	if err != nil {
		return nil, manualAppError(err)
	}
	d := &ManualDetail{Evidence: map[string][]*domain.RefundEvidence{}}
	if d.View, err = uc.view(ctx, r); err != nil {
		return nil, manualAppError(err)
	}
	if d.Destinations, err = uc.Store.Destinations(ctx, refundID); err != nil {
		return nil, manualAppError(err)
	}
	if d.Attempts, err = uc.Store.Attempts(ctx, refundID); err != nil {
		return nil, manualAppError(err)
	}
	for _, a := range d.Attempts {
		if d.Evidence[a.ID], err = uc.Store.Evidence(ctx, a.ID); err != nil {
			return nil, manualAppError(err)
		}
	}
	if d.Audit, err = uc.Audit.ForTarget(ctx, "payment_refund", refundID); err != nil {
		return nil, manualAppError(err)
	}
	return d, nil
}

// DestinationDecision is finance verifying or rejecting one version.
type DestinationDecision struct {
	Version int
	Verify  bool
	Reason  string
	Proof   string
}

// DecideDestination verifies or rejects the pending destination version.
func (uc *ManualRefundUseCase) DecideDestination(ctx context.Context, actor, refundID string, in DestinationDecision) (*RefundView, error) {
	reason, err := domain.ValidateReason(in.Reason)
	if err != nil {
		return nil, err
	}
	if err := uc.proof(ctx, in.Proof, actor, domain.ProofPurposeDestinationDecide, domain.DestinationDecisionRef(refundID, in.Version, in.Verify)); err != nil {
		return nil, err
	}
	var view *RefundView
	err = uc.Tx.Run(ctx, func(ctx context.Context) error {
		r, err := uc.Store.LockRefund(ctx, refundID)
		if err != nil {
			return err
		}
		d, err := uc.Store.CurrentDestination(ctx, refundID)
		if errors.Is(err, repository.ErrDestinationNotFound) {
			return domain.ErrDestinationChanged
		}
		if err != nil {
			return err
		}
		if d.Version != in.Version || d.Status != domain.DestinationPending {
			return domain.ErrDestinationChanged
		}
		now := uc.now()
		d.Status, d.DecidedBy, d.DecidedAt, d.DecisionReason = domain.DestinationRejected, &actor, &now, &reason
		action := "refund_destination_rejected"
		if in.Verify {
			d.Status, action = domain.DestinationVerified, "refund_destination_verified"
		}
		if err := uc.Store.DecideDestination(ctx, d); err != nil {
			return err
		}
		if err := uc.Audit.Record(ctx, actor, action, "payment_refund", refundID, fmt.Sprintf("v%d %s: %s", d.Version, d.Masked(), reason)); err != nil {
			return err
		}
		// PW-009: the buyer gives another account; the reason stays in the app.
		if !in.Verify && uc.Notices != nil {
			if err := uc.Notices.DestinationRejected(ctx, refundID, d.Version); err != nil {
				return err
			}
		}
		view, err = uc.view(ctx, r)
		return err
	})
	if errors.Is(err, repository.ErrStaleState) {
		return nil, domain.ErrDestinationChanged
	}
	return view, manualAppError(err)
}

// RevealedDestination is a destination in clear, for the claimer who
// transfers or the verifier who checks it.
type RevealedDestination struct {
	Version     int
	Beneficiary domain.Beneficiary
}

// RevealDestination decrypts the destination in use. Only the claimer of
// the executing attempt or a finance.approve holder may, with a reason
// (and a password proof under AF-19); every read is audited.
func (uc *ManualRefundUseCase) RevealDestination(ctx context.Context, actor, refundID, reason, proof string) (*RevealedDestination, error) {
	reason, err := domain.ValidateReason(reason)
	if err != nil {
		return nil, err
	}
	if uc.Cipher == nil {
		return nil, domain.ErrDestinationKeyMissing
	}
	d, err := uc.Store.CurrentDestination(ctx, refundID)
	if err != nil {
		return nil, manualAppError(err)
	}
	claimer := false
	if a, err := uc.Store.ActiveAttempt(ctx, refundID); err == nil {
		claimer = a.Stage == domain.AttemptExecuting && a.ClaimedBy != nil && *a.ClaimedBy == actor && a.DestinationVersion == d.Version
	} else if !errors.Is(err, repository.ErrAttemptNotFound) {
		return nil, manualAppError(err)
	}
	if !claimer {
		if _, err := uc.Admins.Require(ctx, actor, adminaccess.FinanceApprove); err != nil {
			return nil, err
		}
	}
	if err := uc.proof(ctx, proof, actor, domain.ProofPurposeDestinationReveal, domain.DestinationRevealRef(refundID, d.Version)); err != nil {
		return nil, err
	}
	plain, err := uc.Cipher.Decrypt(d.Ciphertext, d.KeyVersion, domain.DestinationAAD(refundID, d.Version))
	if err != nil {
		uc.Log.Error().Str("payment_refund_id", refundID).Int("destination_version", d.Version).Int("key_version", d.KeyVersion).Msg("refund_destination_decrypt_failed")
		return nil, domain.ErrDestinationKeyMissing
	}
	var b domain.Beneficiary
	if err := json.Unmarshal(plain, &b); err != nil {
		return nil, apperror.Internal(errors.New("refund destination plaintext is malformed"))
	}
	if err := uc.Audit.Record(ctx, actor, "refund_destination_revealed", "payment_refund", refundID, fmt.Sprintf("v%d: %s", d.Version, reason)); err != nil {
		return nil, apperror.Internal(err)
	}
	uc.Log.Info().Str("payment_refund_id", refundID).Str("actor_id", actor).Int("destination_version", d.Version).Msg("refund_destination_revealed")
	return &RevealedDestination{Version: d.Version, Beneficiary: b}, nil
}

// PrepareAttempt opens a transfer attempt on the verified destination.
func (uc *ManualRefundUseCase) PrepareAttempt(ctx context.Context, actor, refundID string, destinationVersion int, reason string) (*domain.ManualRefundAttempt, error) {
	if !uc.Enabled {
		return nil, domain.ErrManualWorkflowOff
	}
	reason, err := domain.ValidateReason(reason)
	if err != nil {
		return nil, err
	}
	var a *domain.ManualRefundAttempt
	err = uc.Tx.Run(ctx, func(ctx context.Context) error {
		r, err := uc.Store.LockRefund(ctx, refundID)
		if err != nil {
			return err
		}
		if !r.Status.Open() {
			return apperror.Conflict("This refund is already " + string(r.Status))
		}
		d, err := uc.Store.CurrentDestination(ctx, refundID)
		if errors.Is(err, repository.ErrDestinationNotFound) {
			return domain.ErrDestinationNotVerified
		}
		if err != nil {
			return err
		}
		if d.Version != destinationVersion {
			return domain.ErrDestinationChanged
		}
		if d.Status != domain.DestinationVerified {
			return domain.ErrDestinationNotVerified
		}
		a = &domain.ManualRefundAttempt{RefundID: refundID, DestinationID: d.ID, DestinationVersion: d.Version, Amount: r.Amount, Currency: r.Currency,
			PreparedBy: actor, PrepareReason: reason, CreatedAt: uc.now()}
		if err := uc.Store.CreateAttempt(ctx, a); err != nil {
			return err
		}
		return uc.Audit.Record(ctx, actor, "manual_refund_attempt_prepared", "payment_refund", refundID,
			fmt.Sprintf("attempt %s, %d %s to v%d %s: %s", a.ID, a.Amount, a.Currency, d.Version, d.Masked(), reason))
	})
	if err != nil {
		return nil, manualAppError(err)
	}
	return a, nil
}

// change runs one attempt transition under the refund lock: load, check
// the version the client saw, apply, save, audit.
func (uc *ManualRefundUseCase) change(ctx context.Context, attemptID string, expected int, apply func(ctx context.Context, r *domain.Refund, a *domain.ManualRefundAttempt) (string, string, error)) (*domain.ManualRefundAttempt, error) {
	// Read the refund id outside the transaction, then lock refund before
	// attempt like every other manual step.
	peek, err := uc.Store.FindAttempt(ctx, attemptID)
	if err != nil {
		return nil, manualAppError(err)
	}
	var out *domain.ManualRefundAttempt
	err = uc.Tx.Run(ctx, func(ctx context.Context) error {
		r, err := uc.Store.LockRefund(ctx, peek.RefundID)
		if err != nil {
			return err
		}
		a, err := uc.Store.FindAttempt(ctx, attemptID)
		if err != nil {
			return err
		}
		if a.Version != expected {
			return domain.ErrAttemptChanged
		}
		actor, audit, err := apply(ctx, r, a)
		if err != nil {
			return err
		}
		if err := uc.Store.SaveAttempt(ctx, a, expected, uc.now()); err != nil {
			return err
		}
		out = a
		return uc.Audit.Record(ctx, actor, "manual_refund_attempt_"+string(a.Stage), "payment_refund", a.RefundID, "attempt "+a.ID+": "+audit)
	})
	if err != nil {
		return nil, manualAppError(err)
	}
	return out, nil
}

// Claim gives a ready attempt to the operator who will transfer it.
func (uc *ManualRefundUseCase) Claim(ctx context.Context, actor, attemptID string, expected int) (*domain.ManualRefundAttempt, error) {
	return uc.change(ctx, attemptID, expected, func(ctx context.Context, _ *domain.Refund, a *domain.ManualRefundAttempt) (string, string, error) {
		d, err := uc.Store.CurrentDestination(ctx, a.RefundID)
		if err != nil && !errors.Is(err, repository.ErrDestinationNotFound) {
			return "", "", err
		}
		if d == nil || d.ID != a.DestinationID || d.Status != domain.DestinationVerified {
			return "", "", domain.ErrDestinationChanged
		}
		if err := a.Claim(actor, uc.now(), uc.lease()); err != nil {
			return "", "", err
		}
		return actor, "claimed until " + a.LeaseExpiresAt.Format(time.RFC3339), nil
	})
}

// Cancel voids a ready attempt, or the claimer's executing one when no
// money was sent.
func (uc *ManualRefundUseCase) Cancel(ctx context.Context, actor, attemptID string, expected int, reason string) (*domain.ManualRefundAttempt, error) {
	reason, err := domain.ValidateReason(reason)
	if err != nil {
		return nil, err
	}
	return uc.change(ctx, attemptID, expected, func(_ context.Context, _ *domain.Refund, a *domain.ManualRefundAttempt) (string, string, error) {
		return actor, reason, a.Cancel(actor, reason, uc.now())
	})
}

// SubmitInput is the operator's record of a transfer.
type SubmitInput struct {
	ExpectedVersion int
	Submission      domain.Submission
	EvidenceIDs     []string
}

// Submit records the bank reference (and attaches uploaded evidence).
func (uc *ManualRefundUseCase) Submit(ctx context.Context, actor, attemptID string, in SubmitInput) (*domain.ManualRefundAttempt, error) {
	if len(in.EvidenceIDs) > 5 {
		return nil, apperror.Validation("At most 5 evidence files")
	}
	for _, id := range in.EvidenceIDs {
		if _, err := uuid.Parse(id); err != nil {
			return nil, apperror.Validation("Invalid evidence id")
		}
	}
	return uc.change(ctx, attemptID, in.ExpectedVersion, func(ctx context.Context, _ *domain.Refund, a *domain.ManualRefundAttempt) (string, string, error) {
		if err := a.Submit(actor, in.Submission, uc.now()); err != nil {
			return "", "", err
		}
		attached, err := uc.Store.AttachEvidence(ctx, a.ID, in.EvidenceIDs)
		if err != nil {
			return "", "", err
		}
		if attached != len(in.EvidenceIDs) {
			return "", "", apperror.Validation("Some evidence files are not uploaded for this transfer")
		}
		return actor, fmt.Sprintf("bank reference %s from %s at %s, %d evidence file(s)", *a.BankReference, *a.SourceAccount,
			a.ExecutedAt.Format(time.RFC3339), attached), nil
	})
}

// AttemptDecision is the reviewer's confirm/fail.
type AttemptDecision struct {
	ExpectedVersion int
	Confirm         bool
	Reason          string
	Proof           string
}

// Decide confirms a submitted transfer (the refund becomes succeeded with
// the ledger and Order's outbox in the same transaction) or marks a
// submitted/unknown one failed (the refund stays open for a new attempt).
func (uc *ManualRefundUseCase) Decide(ctx context.Context, actor, attemptID string, in AttemptDecision) (*domain.ManualRefundAttempt, error) {
	reason, err := domain.ValidateReason(in.Reason)
	if err != nil {
		return nil, err
	}
	if err := uc.proof(ctx, in.Proof, actor, domain.ProofPurposeAttemptDecide, domain.AttemptDecisionRef(attemptID, in.ExpectedVersion, in.Confirm)); err != nil {
		return nil, err
	}
	out, err := uc.change(ctx, attemptID, in.ExpectedVersion, func(ctx context.Context, r *domain.Refund, a *domain.ManualRefundAttempt) (string, string, error) {
		if err := a.Decide(actor, in.Confirm, reason, uc.now(), uc.TwoPerson); err != nil {
			return "", "", err
		}
		note := reason
		if !uc.TwoPerson && ((a.ClaimedBy != nil && *a.ClaimedBy == actor) || (a.SubmittedBy != nil && *a.SubmittedBy == actor)) {
			note = "single operator: " + reason
		}
		if !in.Confirm {
			return actor, note, nil
		}
		if r.Amount != a.Amount || r.Currency != a.Currency || !r.Status.Open() {
			return "", "", apperror.Conflict("The refund changed after this transfer was prepared")
		}
		if _, err := uc.Refunds.resolve(ctx, actor, r.ID, domain.RefundResolution{Outcome: domain.RefundSucceeded,
			EvidenceReference: a.EvidenceReference(), Note: "manual transfer attempt " + a.ID}, nil); err != nil {
			return "", "", err
		}
		return actor, note, nil
	})
	if err != nil {
		return nil, err
	}
	uc.Log.Info().Str("payment_refund_id", out.RefundID).Str("attempt_id", out.ID).Str("stage", string(out.Stage)).Str("actor_id", actor).Msg("manual_refund_decided")
	return out, nil
}

// UploadEvidence stores a transfer receipt for the claimer of an
// executing attempt, or anyone resolving an unknown one. The row is
// written first so a failed upload never leaves an object nobody knows.
func (uc *ManualRefundUseCase) UploadEvidence(ctx context.Context, actor, attemptID string, data []byte) (*domain.RefundEvidence, error) {
	if uc.Evidence == nil {
		return nil, domain.ErrEvidenceStorageDisabled
	}
	contentType, err := domain.EvidenceContentType(data)
	if err != nil {
		return nil, err
	}
	a, err := uc.Store.FindAttempt(ctx, attemptID)
	if err != nil {
		return nil, manualAppError(err)
	}
	switch {
	case a.Stage == domain.AttemptExecuting && (a.ClaimedBy == nil || *a.ClaimedBy != actor):
		return nil, domain.ErrNotClaimOwner
	case a.Stage != domain.AttemptExecuting && a.Stage != domain.AttemptUnknown:
		return nil, apperror.Conflict("Evidence can only be added before the transfer is submitted")
	}
	sum := sha256.Sum256(data)
	id := uuid.NewString()
	e := &domain.RefundEvidence{ID: id, AttemptID: attemptID, ObjectKey: "refund-evidence/" + attemptID + "/" + id, ContentType: contentType,
		SizeBytes: len(data), SHA256: hex.EncodeToString(sum[:]), UploadedBy: actor, State: "uploading", CreatedAt: uc.now()}
	if err := uc.Store.AddEvidence(ctx, e); err != nil {
		return nil, manualAppError(err)
	}
	if err := uc.Evidence.Put(ctx, e.ObjectKey, data, contentType); err != nil {
		uc.Log.Error().Err(err).Str("evidence_id", id).Msg("refund_evidence_upload_failed")
		return nil, errEvidenceUnavailable
	}
	if err := uc.Store.SetEvidenceState(ctx, id, "uploading", "uploaded"); err != nil {
		return nil, manualAppError(err)
	}
	e.State = "uploaded"
	return e, nil
}

// OpenEvidence streams a stored receipt to a finance reader.
func (uc *ManualRefundUseCase) OpenEvidence(ctx context.Context, actor, evidenceID string) (*domain.RefundEvidence, io.ReadCloser, error) {
	if uc.Evidence == nil {
		return nil, nil, domain.ErrEvidenceStorageDisabled
	}
	e, err := uc.Store.FindEvidence(ctx, evidenceID)
	if err != nil {
		return nil, nil, manualAppError(err)
	}
	if e.State != "uploaded" && e.State != "attached" {
		return nil, nil, apperror.NotFound("Evidence not found")
	}
	body, err := uc.Evidence.Open(ctx, e.ObjectKey)
	if err != nil {
		uc.Log.Error().Err(err).Str("evidence_id", e.ID).Msg("refund_evidence_open_failed")
		return nil, nil, errEvidenceUnavailable
	}
	uc.Log.Info().Str("evidence_id", e.ID).Str("attempt_id", e.AttemptID).Str("actor_id", actor).Msg("refund_evidence_opened")
	return e, body, nil
}

// ExpireClaims moves executing attempts whose lease ran out to unknown:
// the operator may have sent money, so nobody else may transfer until the
// statement is checked.
func (uc *ManualRefundUseCase) ExpireClaims(ctx context.Context, limit int) (int, error) {
	expired, err := uc.Store.ExpiredClaims(ctx, uc.now(), limit)
	if err != nil {
		return 0, err
	}
	moved := 0
	for _, a := range expired {
		err := uc.Tx.Run(ctx, func(ctx context.Context) error {
			if _, err := uc.Store.LockRefund(ctx, a.RefundID); err != nil {
				return err
			}
			current, err := uc.Store.FindAttempt(ctx, a.ID)
			if err != nil {
				return err
			}
			if !current.LeaseExpired(uc.now()) {
				return nil
			}
			expected := current.Version
			current.Stage = domain.AttemptUnknown
			if err := uc.Store.SaveAttempt(ctx, current, expected, uc.now()); err != nil {
				return err
			}
			moved++
			return uc.Audit.Record(ctx, *current.ClaimedBy, "manual_refund_attempt_unknown", "payment_refund", current.RefundID,
				"attempt "+current.ID+": claim expired without a submission; check the bank statement")
		})
		if err != nil {
			return moved, err
		}
		uc.Log.Warn().Str("payment_refund_id", a.RefundID).Str("attempt_id", a.ID).Msg("manual_refund_claim_expired")
	}
	return moved, nil
}

// CleanupEvidence deletes uploads never attached to a submission after a
// day's grace. Attached evidence is kept.
func (uc *ManualRefundUseCase) CleanupEvidence(ctx context.Context, limit int) (int, error) {
	if uc.Evidence == nil {
		return 0, nil
	}
	orphans, err := uc.Store.OrphanEvidence(ctx, uc.now().Add(-24*time.Hour), limit)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, e := range orphans {
		if err := uc.Evidence.Delete(ctx, e.ObjectKey); err != nil {
			return removed, err
		}
		if err := uc.Store.SetEvidenceState(ctx, e.ID, e.State, "deleted"); err != nil && !errors.Is(err, repository.ErrStaleState) {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

// Report logs what needs an operator: unknown transfers, submissions
// waiting for review, destinations waiting for verification.
func (uc *ManualRefundUseCase) Report(ctx context.Context) {
	c, err := uc.Store.Counts(ctx, uc.now(), 24*time.Hour)
	if err != nil {
		if ctx.Err() == nil {
			uc.Log.Error().Err(err).Msg("manual_refund_report_failed")
		}
		return
	}
	event := uc.Log.Info()
	if c.Unknown > 0 || c.SubmittedOverdue > 0 || c.ExecutingOverdue > 0 {
		event = uc.Log.Warn()
	}
	event.Int64("unknown", c.Unknown).Int64("submitted_over_24h", c.SubmittedOverdue).Int64("destinations_pending_over_24h", c.DestinationsOverdue).
		Int64("executing_past_lease", c.ExecutingOverdue).Int64("ready_over_24h", c.ReadyWithoutClaimant).Msg("manual_refund_report")
}

// Run expires claims, cleans orphan uploads, moves pre-workflow refund
// deadlines and reports every minute.
func (uc *ManualRefundUseCase) Run(ctx context.Context) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		if _, err := uc.ExpireClaims(ctx, 50); err != nil && ctx.Err() == nil {
			uc.Log.Error().Err(err).Msg("manual_refund_claim_expiry_failed")
		}
		if _, err := uc.CleanupEvidence(ctx, 50); err != nil && ctx.Err() == nil {
			uc.Log.Error().Err(err).Msg("refund_evidence_cleanup_failed")
		}
		// PW-017: refunds opened before the workflow move to its deadlines.
		if uc.Enabled {
			if _, err := uc.Store.RestageLegacySLA(ctx, 50); err != nil && ctx.Err() == nil {
				uc.Log.Error().Err(err).Msg("refund_deadline_restage_failed")
			}
		}
		uc.Report(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
