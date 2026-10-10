package domain

import (
	"time"

	casesla "shopee/backend/pkg/casesla/deadline"
)

func (r Refund) SLAStage() casesla.StageInput {
	in := casesla.StageInput{ResourceType: "refund", ResourceID: r.ID, At: r.CreatedAt, CreatedAt: r.CreatedAt,
		URL: "/admin/refunds?refund_id=" + r.ID, WaitingOn: "admin", Duration: casesla.ManualRefundReady}
	// Without the manual workflow (AF-06) refunds await an operator/provider
	// receipt; this stage does not claim that a transfer was submitted.
	if r.Status == RefundAwaitingProvider {
		in.Stage = "awaiting_refund_receipt"
	}
	if r.ResolvedAt != nil {
		in.At = *r.ResolvedAt
	}
	return in
}

// ManualSLAStage (PW-017) is a refund's deadline under the manual workflow,
// entered at `at`: finance verifies a submitted account within the support
// acknowledgement window, then transfers within 72 hours of it being ready
// (claimed included). Waiting for the buyer's account is not the admin's
// time (no deadline; the buyer is reminded instead), and a submitted or
// unknown transfer is followed by its own review queues, not this clock.
// The deadline's outer limit counts from the first submitted account.
func ManualSLAStage(r Refund, dest *RefundDestination, attempt *ManualRefundAttempt, at time.Time) casesla.StageInput {
	in := casesla.StageInput{ResourceType: "refund", ResourceID: r.ID, At: at, CreatedAt: at,
		URL: "/admin/refunds?refund_id=" + r.ID, WaitingOn: "admin"}
	if dest != nil && dest.Status != DestinationSuperseded {
		in.CreatedAt = dest.SubmittedAt
	}
	switch ManualStage(&r, dest, attempt) {
	case "verifying":
		in.Stage, in.Duration = "refund_destination_verification", casesla.SupportAcknowledgement
	case "ready", string(AttemptExecuting):
		in.Stage, in.Duration = "manual_refund_ready", casesla.ManualRefundReady
	}
	return in
}
