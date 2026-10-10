package repository

import (
	"context"
	"time"

	"shopee/backend/services/order/internal/domain"
)

// AttachReceiptEvidence (PW-038) links the owner's still-unattached
// uploads to a receipt reference (a return or a delivery exception),
// locking them; it returns how many were linked, so the caller refuses ids
// that are not the owner's, missing or already used.
func (r *SupportCaseRepository) AttachReceiptEvidence(ctx context.Context, ids []string, ownerID, receiptType, ref string) (int64, error) {
	tag, err := connection(ctx, r.pool).Exec(ctx, `
		UPDATE case_attachments SET state = 'attached', receipt_type = $3, receipt_ref = $4, attached_at = now()
		WHERE id::text = ANY($1) AND owner_id = $2 AND state = 'uploaded'`, ids, ownerID, receiptType, ref)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ExpiredReceiptEvidence returns receipt evidence attached before t.
func (r *SupportCaseRepository) ExpiredReceiptEvidence(ctx context.Context, attachedBefore time.Time, limit int) ([]*domain.CaseAttachment, error) {
	return r.listAttachments(ctx, `WHERE receipt_ref IS NOT NULL AND state = 'attached' AND attached_at < $1 ORDER BY attached_at, id LIMIT $2`, attachedBefore, limit)
}

// ReceiptEvidence lists the evidence attached to a receipt reference.
func (r *SupportCaseRepository) ReceiptEvidence(ctx context.Context, receiptType, ref string) ([]*domain.CaseAttachment, error) {
	return r.listAttachments(ctx, `WHERE receipt_type = $1 AND receipt_ref = $2 AND state = 'attached' ORDER BY created_at, id`, receiptType, ref)
}
