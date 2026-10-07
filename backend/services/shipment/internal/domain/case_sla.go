package domain

import casesla "shopee/backend/pkg/casesla/deadline"

func (s Shipment) SLAStage() casesla.StageInput {
	in := casesla.StageInput{ResourceType: "interception", ResourceID: s.ID, At: s.UpdatedAt, CreatedAt: s.CreatedAt,
		URL: "/admin/fulfillment?shipment_id=" + s.ID, WaitingOn: "operator", Duration: casesla.StopDelivery}
	if s.Status == StatusInterceptionRequested {
		in.Stage = "stop_delivery"
		// The case begins with the interception, not package creation.
		if s.InterceptRequestedAt != nil {
			in.At = *s.InterceptRequestedAt
			in.CreatedAt = *s.InterceptRequestedAt
		} else {
			in.CreatedAt = s.UpdatedAt
		}
	}
	return in
}
