package usecase

import (
	"context"
	"net/http"
	"slices"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/order/internal/domain"
)

// PW-038: evidence images of a goods receipt (return or failed delivery)
// and of a return parcel marked lost. They are uploaded like support
// evidence (sanitized, private bucket) and attached in the transaction of
// the step they prove.

// Receipt evidence kinds (case_attachments.receipt_type).
const (
	EvidenceReturn            = "return"
	EvidenceDeliveryException = "delivery_exception"
)

// MaxReceiptEvidence bounds the images of one step.
const MaxReceiptEvidence = 5

// ReceiptEvidenceRetention: receipt photos are kept at most 30 days after
// the step they prove, then removed with their object.
const ReceiptEvidenceRetention = 30 * 24 * time.Hour

// ReceiptEvidencePort links uploads to receipts (repository.SupportCaseRepository).
type ReceiptEvidencePort interface {
	AttachReceiptEvidence(ctx context.Context, ids []string, ownerID, receiptType, ref string) (int64, error)
	ReceiptEvidence(ctx context.Context, receiptType, ref string) ([]*domain.CaseAttachment, error)
	ExpiredReceiptEvidence(ctx context.Context, attachedBefore time.Time, limit int) ([]*domain.CaseAttachment, error)
}

// CodeEvidenceUnavailable: an evidence id is missing, someone else's or
// already used; the step is refused rather than saved without its proof.
const CodeEvidenceUnavailable apperror.Code = "evidence_unavailable"

// attachEvidence links the actor's uploads to the reference, in the
// caller's transaction; all or none.
func (uc *OrderUseCase) attachEvidence(ctx context.Context, actorID, receiptType, ref string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	ids = slices.Compact(slices.Sorted(slices.Values(ids)))
	if len(ids) > MaxReceiptEvidence {
		return apperror.Validation("At most 5 evidence images per step")
	}
	if uc.ReceiptEvidence == nil || uc.Attachments == nil {
		return domain.AttachmentsUnavailable()
	}
	n, err := uc.ReceiptEvidence.AttachReceiptEvidence(ctx, ids, actorID, receiptType, ref)
	if err != nil {
		return err
	}
	if n != int64(len(ids)) {
		return &apperror.Error{Code: CodeEvidenceUnavailable, Status: http.StatusConflict,
			Message: "An evidence image is missing, not yours or already used; upload it again"}
	}
	return nil
}

// ReceiptEvidenceFile is one evidence image opened for download.
type ReceiptEvidenceFile = SupportAttachmentFile

// ListReceiptEvidence lists a return's or delivery exception's evidence
// for its shop or an admin.
func (uc *OrderUseCase) ListReceiptEvidence(ctx context.Context, actor SupportActor, receiptType, ref string) ([]*domain.CaseAttachment, error) {
	if err := uc.authorizeEvidence(ctx, actor, receiptType, ref); err != nil {
		return nil, err
	}
	if uc.ReceiptEvidence == nil {
		return []*domain.CaseAttachment{}, nil
	}
	items, err := uc.ReceiptEvidence.ReceiptEvidence(ctx, receiptType, ref)
	if err != nil {
		return nil, appError(err)
	}
	return items, nil
}

// OpenReceiptEvidence streams one evidence image of the reference.
func (uc *OrderUseCase) OpenReceiptEvidence(ctx context.Context, actor SupportActor, receiptType, ref, attachmentID string) (*ReceiptEvidenceFile, error) {
	items, err := uc.ListReceiptEvidence(ctx, actor, receiptType, ref)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(items, func(a *domain.CaseAttachment) bool { return a.ID == attachmentID })
	if i < 0 || uc.Attachments == nil {
		return nil, apperror.NotFound("Evidence not found")
	}
	body, err := uc.Attachments.Open(ctx, items[i].ObjectKey)
	if err != nil {
		uc.Log.Error().Err(err).Str("attachment_id", attachmentID).Msg("order_receipt_evidence_open_failed")
		return nil, domain.AttachmentsUnavailable()
	}
	return &ReceiptEvidenceFile{Body: body, ContentType: items[i].ContentType, SizeBytes: items[i].SizeBytes}, nil
}

// authorizeEvidence: an admin, or a member of the shop with the
// permission the receipt needs.
func (uc *OrderUseCase) authorizeEvidence(ctx context.Context, actor SupportActor, receiptType, ref string) error {
	switch actor.Role {
	case "admin":
		return uc.requireAdmin(ctx, actor.ID)
	case "vendor":
		switch receiptType {
		case EvidenceReturn:
			_, err := uc.authorizeVendorForReturn(ctx, actor.ID, ref)
			return err
		case EvidenceDeliveryException:
			_, err := uc.GetDeliveryException(ctx, actor, ref)
			return err
		}
	}
	return apperror.NotFound("Evidence not found")
}
