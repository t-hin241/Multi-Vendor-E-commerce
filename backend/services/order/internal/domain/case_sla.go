package domain

import casesla "shopee/backend/pkg/casesla/deadline"

func (c SupportCase) SLAStage() casesla.StageInput {
	in := casesla.StageInput{ResourceType: "support", ResourceID: c.ID, At: c.UpdatedAt, CreatedAt: c.CreatedAt,
		AssigneeID: c.AssigneeID, URL: "/admin/support/" + c.ID, WaitingOn: "admin", Duration: casesla.SupportAcknowledgement}
	switch c.Status {
	case CaseOpen:
		in.Stage = "acknowledgement"
	case CaseInProgress:
		in.Stage = "operator_response"
	case CaseWaitingVendor:
		in.Stage = "vendor_response"
		in.WaitingOn = "vendor"
		in.Duration = casesla.VendorResponse
	case CaseWaitingBuyer:
		in.Stage = "operator_response"
		in.WaitingOn = "buyer"
		in.Pause = true
	case CaseResolutionPending:
		in.Stage = "resolution_followup"
		in.Duration = casesla.VendorResponse
	}
	return in
}

func (r ReturnRequest) SLAStage() casesla.StageInput {
	in := casesla.StageInput{ResourceType: "return", ResourceID: r.ID, At: r.UpdatedAt, CreatedAt: r.CreatedAt,
		URL: "/admin/returns?return_id=" + r.ID, WaitingOn: "admin", Duration: casesla.ReturnDecision}
	// Vendor confirmation is advisory; it does not restart the decision clock.
	if r.Status == ReturnRequested || r.Status == ReturnVendorConfirmed {
		in.Stage = "return_decision"
	}
	return in
}
