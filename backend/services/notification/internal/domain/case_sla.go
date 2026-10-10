package domain

import (
	"time"

	casesla "shopee/backend/pkg/casesla/deadline"
)

// SLAStage (PW-045): a shop notice nobody may receive, or one Vendor never
// answered, is the marketplace's to follow up within the support
// acknowledgement window (AF-07); a retry or a resolution ends it. The
// stage is entered at `at`.
func (a VendorAction) SLAStage(at time.Time) casesla.StageInput {
	in := casesla.StageInput{ResourceType: "vendor_action", ResourceID: a.ID, At: at, CreatedAt: a.CreatedAt,
		URL: "/admin/notifications", WaitingOn: "admin", Duration: casesla.SupportAcknowledgement}
	if a.NeedsReview() {
		in.Stage = "vendor_action_" + string(a.Status)
	}
	return in
}
