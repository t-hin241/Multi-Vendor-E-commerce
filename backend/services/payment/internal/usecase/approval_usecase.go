package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/adminaccess"
	"shopee/backend/pkg/apperror"
	"shopee/backend/services/payment/internal/domain"
	"shopee/backend/services/payment/internal/repository"
)

// ApprovalStore stores maker-checker requests.
type ApprovalStore interface {
	Create(ctx context.Context, a *domain.ApprovalRequest) error
	Find(ctx context.Context, id string) (*domain.ApprovalRequest, error)
	List(ctx context.Context, status string, limit, offset int) ([]*domain.ApprovalRequest, error)
	Transition(ctx context.Context, c repository.ApprovalChange) (*domain.ApprovalRequest, error)
}

// AdminAuthority answers admin permission questions and spends
// reauthentication proofs (Identity).
type AdminAuthority interface {
	Require(ctx context.Context, userID, permission string) (int64, error)
	ConsumeProof(ctx context.Context, proof, userID, purpose, operationHash string) error
}

// ApprovalUseCase runs maker-checker for manual money actions (AF-19):
// one admin prepares an immutable request and confirms the password; a
// different admin with finance.approve confirms the password and approves;
// the action executes in the approval's transaction with the snapshot
// re-checked. Nothing here calls a provider.
type ApprovalUseCase struct {
	Store       ApprovalStore
	Refunds     *RefundUseCase
	Settlement  *SettlementUseCase
	Payouts     PayoutRepositoryPort
	RefundsRepo RefundRepositoryPort
	Audit       AuditRepositoryPort
	Tx          Transactor
	Admins      AdminAuthority
	// Enabled is FEATURE_ADMIN_SCOPED_PERMISSIONS_ENABLED in Payment.
	Enabled bool
	Now     func() time.Time
	Log     zerolog.Logger
}

func (uc *ApprovalUseCase) now() time.Time {
	if uc.Now != nil {
		return uc.Now().UTC()
	}
	return time.Now().UTC()
}

func approvalError(status int, code apperror.Code, message string) *apperror.Error {
	return &apperror.Error{Code: code, Message: message, Status: status}
}

var (
	errApprovalsOff   = approvalError(http.StatusNotFound, "feature_disabled", "Approval requests are not enabled")
	errSelfApproval   = approvalError(http.StatusConflict, "self_approval", "The admin who prepared a request cannot approve it")
	errStaleSnapshot  = approvalError(http.StatusConflict, "stale_snapshot", "The target changed after the request was prepared; cancel it and prepare a new one")
	errStaleRequest   = approvalError(http.StatusConflict, "stale_request", "This request changed meanwhile; reload and try again")
	errApprovalExpiry = approvalError(http.StatusConflict, "approval_expired", "This request expired; prepare a new one")
	errOpenRequest    = approvalError(http.StatusConflict, "approval_open", "An open request already exists for this target")
	errMakerRevoked   = approvalError(http.StatusConflict, "maker_permission_revoked", "The admin who prepared this request no longer holds finance.prepare")
)

// DraftInput is a maker's prepared request.
type DraftInput struct {
	Kind              domain.ApprovalKind
	TargetID          string
	Payload           json.RawMessage
	Reason            string
	PermissionVersion int64
}

// Draft stores an immutable request and returns it with the payload hash
// the maker and checker confirm their passwords against.
func (uc *ApprovalUseCase) Draft(ctx context.Context, makerID string, in DraftInput) (*domain.ApprovalRequest, error) {
	if !uc.Enabled {
		return nil, errApprovalsOff
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if in.Reason == "" || len(in.Reason) > 500 {
		return nil, apperror.Validation("A reason of 1-500 characters is required")
	}
	if _, err := uuid.Parse(in.TargetID); err != nil {
		return nil, apperror.Validation("Invalid target")
	}
	payload, err := domain.NormalizeApprovalPayload(in.Kind, in.Payload)
	if err != nil {
		return nil, err
	}
	snapshot, err := uc.snapshot(ctx, in.Kind, in.TargetID)
	if err != nil {
		return nil, err
	}
	a := &domain.ApprovalRequest{Kind: in.Kind, TargetID: in.TargetID, Payload: payload, PayloadHash: domain.ApprovalPayloadHash(in.Kind, in.TargetID, payload),
		Snapshot: snapshot, MakerID: makerID, MakerPermissionVersion: in.PermissionVersion, Reason: in.Reason, ExpiresAt: uc.now().Add(domain.ApprovalTTL)}
	err = uc.Tx.Run(ctx, func(ctx context.Context) error {
		if err := uc.Store.Create(ctx, a); err != nil {
			return err
		}
		return uc.Audit.Record(ctx, makerID, "approval_drafted", "approval_request", a.ID, string(a.Kind)+" "+a.TargetID+": "+a.Reason)
	})
	if errors.Is(err, repository.ErrApprovalOpen) {
		return nil, errOpenRequest
	}
	if err != nil {
		return nil, asAppError(err)
	}
	return a, nil
}

// snapshot is what the checker approves about the target now.
func (uc *ApprovalUseCase) snapshot(ctx context.Context, kind domain.ApprovalKind, targetID string) (json.RawMessage, error) {
	switch kind {
	case domain.ApprovalRefundResolution:
		r, err := uc.RefundsRepo.FindByID(ctx, targetID)
		if err != nil {
			return nil, asAppError(err)
		}
		if !r.Status.Open() {
			return nil, apperror.Conflict("This refund is already resolved")
		}
		return json.Marshal(domain.SnapshotRefund(r))
	case domain.ApprovalPayoutItemResolution:
		var item *domain.PayoutItem
		err := uc.Tx.Run(ctx, func(ctx context.Context) error {
			var err error
			item, err = uc.Payouts.LockItem(ctx, targetID)
			return err
		})
		if err != nil {
			return nil, asAppError(err)
		}
		if item.Status != domain.PayoutItemPending {
			return nil, apperror.Conflict("This payout item is already resolved")
		}
		return json.Marshal(domain.SnapshotPayoutItem(item))
	case domain.ApprovalSettlementAdjustment:
		return json.Marshal(map[string]string{"vendor_id": targetID})
	}
	return nil, apperror.Validation("Unknown operation kind")
}

// Submit sends the maker's draft to the checkers, after the maker
// confirmed the password for exactly this payload.
func (uc *ApprovalUseCase) Submit(ctx context.Context, makerID, id, proof string, expectedVersion int64) (*domain.ApprovalRequest, error) {
	if !uc.Enabled {
		return nil, errApprovalsOff
	}
	current, err := uc.Store.Find(ctx, id)
	if err != nil {
		return nil, asApprovalError(err)
	}
	if current.MakerID != makerID {
		return nil, adminaccess.Missing(adminaccess.FinancePrepare)
	}
	if err := uc.Admins.ConsumeProof(ctx, proof, makerID, domain.ProofPurposeSubmit, current.PayloadHash); err != nil {
		return nil, err
	}
	var out *domain.ApprovalRequest
	expired := false
	err = uc.Tx.Run(ctx, func(ctx context.Context) error {
		a, err := uc.Store.Find(ctx, id)
		if err != nil {
			return err
		}
		if expired, err = uc.expireIfDue(ctx, a); err != nil || expired {
			return err
		}
		if a.Status != domain.ApprovalDraft || a.Version != expectedVersion || a.PayloadHash != current.PayloadHash {
			return errStaleRequest
		}
		out, err = uc.Store.Transition(ctx, repository.ApprovalChange{ID: id, ExpectedVersion: expectedVersion, From: domain.ApprovalDraft,
			To: domain.ApprovalPending, At: uc.now()})
		if err != nil {
			return err
		}
		return uc.Audit.Record(ctx, makerID, "approval_submitted", "approval_request", id, string(a.Kind)+" "+a.TargetID)
	})
	if err == nil && expired {
		err = errApprovalExpiry
	}
	if err != nil {
		return nil, asApprovalError(err)
	}
	return out, nil
}

// expireIfDue marks an open request past its time as expired in the
// caller's transaction (which then commits and reports the expiry).
func (uc *ApprovalUseCase) expireIfDue(ctx context.Context, a *domain.ApprovalRequest) (bool, error) {
	if !a.Expired(uc.now()) {
		return false, nil
	}
	if _, err := uc.Store.Transition(ctx, repository.ApprovalChange{ID: a.ID, ExpectedVersion: a.Version, From: a.Status,
		To: domain.ApprovalExpired, At: uc.now()}); err != nil {
		return false, err
	}
	return true, uc.Audit.Record(ctx, a.MakerID, "approval_expired", "approval_request", a.ID, "expired after 24h")
}

// DecisionInput is a checker's decision.
type DecisionInput struct {
	Approve           bool
	Proof             string
	ExpectedVersion   int64
	Reason            string
	PermissionVersion int64
}

// Decide approves (and executes) or rejects a pending request. The
// checker is never the maker, the maker must still hold finance.prepare,
// and the target must still match the snapshot.
func (uc *ApprovalUseCase) Decide(ctx context.Context, checkerID, id string, in DecisionInput) (*domain.ApprovalRequest, error) {
	if !uc.Enabled {
		return nil, errApprovalsOff
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if in.Reason == "" || len(in.Reason) > 500 {
		return nil, apperror.Validation("A reason of 1-500 characters is required")
	}
	current, err := uc.Store.Find(ctx, id)
	if err != nil {
		return nil, asApprovalError(err)
	}
	if current.MakerID == checkerID {
		return nil, errSelfApproval
	}
	if in.Approve {
		if _, err := uc.Admins.Require(ctx, current.MakerID, adminaccess.FinancePrepare); err != nil {
			var app *apperror.Error
			if errors.As(err, &app) && app.Code == adminaccess.CodeMissingPermission {
				return nil, errMakerRevoked
			}
			return nil, err
		}
	}
	if err := uc.Admins.ConsumeProof(ctx, in.Proof, checkerID, domain.ProofPurposeDecide, current.PayloadHash); err != nil {
		return nil, err
	}
	var out *domain.ApprovalRequest
	expired := false
	err = uc.Tx.Run(ctx, func(ctx context.Context) error {
		a, err := uc.Store.Find(ctx, id)
		if err != nil {
			return err
		}
		if expired, err = uc.expireIfDue(ctx, a); err != nil || expired {
			return err
		}
		if a.Status != domain.ApprovalPending || a.Version != in.ExpectedVersion || a.PayloadHash != current.PayloadHash {
			return errStaleRequest
		}
		change := repository.ApprovalChange{ID: id, ExpectedVersion: a.Version, From: domain.ApprovalPending, At: uc.now(),
			CheckerID: &checkerID, CheckerPermissionVersion: &in.PermissionVersion, DecisionReason: &in.Reason}
		if !in.Approve {
			change.To = domain.ApprovalRejected
			if out, err = uc.Store.Transition(ctx, change); err != nil {
				return err
			}
			return uc.Audit.Record(ctx, checkerID, "approval_rejected", "approval_request", id, in.Reason)
		}
		ref, err := uc.execute(ctx, a, checkerID)
		if err != nil {
			return err
		}
		change.To, change.ExecutionRef = domain.ApprovalApproved, &ref
		if out, err = uc.Store.Transition(ctx, change); err != nil {
			return err
		}
		return uc.Audit.Record(ctx, checkerID, "approval_approved", "approval_request", id,
			fmt.Sprintf("%s %s by maker %s (v%d), checker v%d: %s", a.Kind, a.TargetID, a.MakerID, a.MakerPermissionVersion, in.PermissionVersion, in.Reason))
	})
	if err == nil && expired {
		err = errApprovalExpiry
	}
	if err != nil {
		return nil, asApprovalError(err)
	}
	uc.Log.Info().Str("approval_id", id).Str("operation_kind", string(out.Kind)).Str("status", string(out.Status)).Msg("payment_approval_decided")
	return out, nil
}

// execute runs the approved operation in the approval's transaction after
// re-checking the target against the snapshot.
func (uc *ApprovalUseCase) execute(ctx context.Context, a *domain.ApprovalRequest, checkerID string) (string, error) {
	switch a.Kind {
	case domain.ApprovalRefundResolution:
		var p domain.ResolutionPayload
		if err := json.Unmarshal(a.Payload, &p); err != nil {
			return "", err
		}
		var want domain.RefundSnapshot
		if err := json.Unmarshal(a.Snapshot, &want); err != nil {
			return "", err
		}
		r, err := uc.Refunds.resolve(ctx, checkerID, a.TargetID, domain.RefundResolution{Outcome: domain.RefundStatus(p.Outcome),
			EvidenceReference: p.EvidenceReference, Note: p.Note}, func(r *domain.Refund) error {
			if domain.SnapshotRefund(r) != want {
				return errStaleSnapshot
			}
			return nil
		})
		if err != nil {
			return "", err
		}
		return "refund:" + r.ID, nil
	case domain.ApprovalPayoutItemResolution:
		var p domain.ResolutionPayload
		if err := json.Unmarshal(a.Payload, &p); err != nil {
			return "", err
		}
		var want domain.PayoutItemSnapshot
		if err := json.Unmarshal(a.Snapshot, &want); err != nil {
			return "", err
		}
		item, err := uc.Settlement.resolvePayoutItem(ctx, checkerID, a.TargetID, domain.PayoutResolution{Outcome: domain.PayoutItemStatus(p.Outcome),
			EvidenceReference: p.EvidenceReference, Note: p.Note}, func(i *domain.PayoutItem) error {
			if domain.SnapshotPayoutItem(i) != want {
				return errStaleSnapshot
			}
			return nil
		})
		if err != nil {
			return "", err
		}
		return "payout_item:" + item.ID, nil
	case domain.ApprovalSettlementAdjustment:
		var p domain.AdjustmentPayload
		if err := json.Unmarshal(a.Payload, &p); err != nil {
			return "", err
		}
		entry, err := uc.Settlement.adjust(ctx, checkerID, a.TargetID, p.Amount, p.Currency, p.Reason)
		if err != nil {
			return "", err
		}
		return entry.SourceRef, nil
	}
	return "", apperror.Validation("Unknown operation kind")
}

// Cancel withdraws the maker's own open request.
func (uc *ApprovalUseCase) Cancel(ctx context.Context, makerID, id string, expectedVersion int64) (*domain.ApprovalRequest, error) {
	var out *domain.ApprovalRequest
	err := uc.Tx.Run(ctx, func(ctx context.Context) error {
		a, err := uc.Store.Find(ctx, id)
		if err != nil {
			return err
		}
		if a.MakerID != makerID {
			return adminaccess.Missing(adminaccess.FinancePrepare)
		}
		if !a.Open() || a.Version != expectedVersion {
			return errStaleRequest
		}
		if out, err = uc.Store.Transition(ctx, repository.ApprovalChange{ID: id, ExpectedVersion: a.Version, From: a.Status,
			To: domain.ApprovalCancelled, At: uc.now()}); err != nil {
			return err
		}
		return uc.Audit.Record(ctx, makerID, "approval_cancelled", "approval_request", id, string(a.Kind)+" "+a.TargetID)
	})
	if err != nil {
		return nil, asApprovalError(err)
	}
	return out, nil
}

// List lists requests for the finance queue; an open request past its
// time is shown as expired.
func (uc *ApprovalUseCase) List(ctx context.Context, status string, limit, offset int) ([]*domain.ApprovalRequest, error) {
	switch domain.ApprovalStatus(status) {
	case "", domain.ApprovalDraft, domain.ApprovalPending, domain.ApprovalApproved, domain.ApprovalRejected, domain.ApprovalExpired, domain.ApprovalCancelled:
	default:
		return nil, apperror.Validation("Invalid status")
	}
	if limit < 1 || limit > 100 || offset < 0 {
		return nil, apperror.Validation("Invalid pagination")
	}
	items, err := uc.Store.List(ctx, status, limit, offset)
	if err != nil {
		return nil, asAppError(err)
	}
	now := uc.now()
	for _, a := range items {
		if a.Expired(now) {
			a.Status = domain.ApprovalExpired
		}
	}
	return items, nil
}

// Get reads one request.
func (uc *ApprovalUseCase) Get(ctx context.Context, id string) (*domain.ApprovalRequest, error) {
	a, err := uc.Store.Find(ctx, id)
	if err != nil {
		return nil, asApprovalError(err)
	}
	if a.Expired(uc.now()) {
		a.Status = domain.ApprovalExpired
	}
	return a, nil
}

func asApprovalError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, repository.ErrApprovalNotFound):
		return apperror.NotFound("Approval request not found")
	case errors.Is(err, repository.ErrStaleState):
		return errStaleRequest
	}
	return asAppError(err)
}
