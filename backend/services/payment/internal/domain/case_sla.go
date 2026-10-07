package domain

import casesla "shopee/backend/pkg/casesla/deadline"

func (r Refund) SLAStage() casesla.StageInput {
	in := casesla.StageInput{ResourceType: "refund", ResourceID: r.ID, At: r.CreatedAt, CreatedAt: r.CreatedAt,
		URL: "/admin/refunds?refund_id=" + r.ID, WaitingOn: "admin", Duration: casesla.ManualRefundReady}
	// Existing refunds await an operator/provider receipt. AF-06 will add
	// ready/submitted; this stage does not claim that a transfer was submitted.
	if r.Status == RefundAwaitingProvider {
		in.Stage = "awaiting_refund_receipt"
	}
	if r.ResolvedAt != nil {
		in.At = *r.ResolvedAt
	}
	return in
}
