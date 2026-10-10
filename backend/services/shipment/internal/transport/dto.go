package transport

import (
	"time"

	"shopee/backend/services/shipment/internal/domain"
)

type createShipmentRequest struct {
	VendorOrderID string `json:"vendor_order_id" binding:"required"`
}

type advanceShipmentRequest struct {
	Status         string `json:"status" binding:"required,oneof=ready_to_ship shipped delivered cancelled"`
	TrackingNumber string `json:"tracking_number"`
}

// simulateCarrierDecisionRequest stands in for a real carrier's dispatcher
// callback — see ShipmentUseCase.SimulateCarrierDecision.
type simulateCarrierDecisionRequest struct {
	Accepted bool   `json:"accepted"`
	Reason   string `json:"reason"`
}

type shipmentResponse struct {
	ActionDueAt          *time.Time `json:"action_due_at"`
	WaitingOn            string     `json:"waiting_on"`
	ID                   string     `json:"id"`
	VendorOrderID        string     `json:"vendor_order_id"`
	Status               string     `json:"status"`
	Version              int64      `json:"version"`
	AttemptNo            int        `json:"attempt_no"`
	OriginalShipmentID   *string    `json:"original_shipment_id,omitempty"`
	LostAt               *time.Time `json:"lost_at,omitempty"`
	CarrierID            *string    `json:"carrier_id,omitempty"`
	TrackingNumber       *string    `json:"tracking_number,omitempty"`
	ZoneName             *string    `json:"zone_name,omitempty"`
	FeeAmount            int64      `json:"fee_amount"`
	PackageWeightGrams   *int64     `json:"package_weight_grams,omitempty"`
	RecipientName        *string    `json:"recipient_name,omitempty"`
	Phone                *string    `json:"phone,omitempty"`
	Province             *string    `json:"province,omitempty"`
	District             *string    `json:"district,omitempty"`
	Ward                 *string    `json:"ward,omitempty"`
	StreetAddress        *string    `json:"street_address,omitempty"`
	ShippedAt            *time.Time `json:"shipped_at,omitempty"`
	DeliveredAt          *time.Time `json:"delivered_at,omitempty"`
	ReturnedAt           *time.Time `json:"returned_at,omitempty"`
	CancelledAt          *time.Time `json:"cancelled_at,omitempty"`
	TrackingUpdatedAt    *time.Time `json:"tracking_updated_at,omitempty"`
	FailedAttempts       int        `json:"failed_attempts"`
	LastAttemptReason    *string    `json:"last_attempt_reason,omitempty"`
	AddressRedacted      bool       `json:"address_redacted"`
	InterceptRequestedAt *time.Time `json:"intercept_requested_at,omitempty"`
	InterceptResolvedAt  *time.Time `json:"intercept_resolved_at,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

func toShipmentResponse(s *domain.Shipment) shipmentResponse {
	return shipmentResponse{ActionDueAt: s.ActionDueAt, WaitingOn: s.WaitingOn,
		ID: s.ID, VendorOrderID: s.VendorOrderID, Status: string(s.Status),
		Version: s.Version, AttemptNo: s.AttemptNo, OriginalShipmentID: s.OriginalShipmentID, LostAt: s.LostAt,
		CarrierID: s.CarrierID, TrackingNumber: s.TrackingNumber,
		ZoneName: s.ZoneName, FeeAmount: s.FeeAmount, PackageWeightGrams: s.PackageWeightGrams,
		RecipientName: s.RecipientName, Phone: s.Phone, Province: s.Province,
		District: s.District, Ward: s.Ward, StreetAddress: s.StreetAddress,
		ShippedAt: s.ShippedAt, DeliveredAt: s.DeliveredAt, ReturnedAt: s.ReturnedAt, CancelledAt: s.CancelledAt,
		TrackingUpdatedAt: s.TrackingUpdatedAt, FailedAttempts: s.FailedAttempts, LastAttemptReason: s.LastAttemptReason,
		AddressRedacted:      s.AddressRedactedAt != nil,
		InterceptRequestedAt: s.InterceptRequestedAt, InterceptResolvedAt: s.InterceptResolvedAt,
		CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt,
	}
}

func toShipmentResponseList(shipments []*domain.Shipment) []shipmentResponse {
	out := make([]shipmentResponse, 0, len(shipments))
	for _, s := range shipments {
		out = append(out, toShipmentResponse(s))
	}
	return out
}

type trackingEventResponse struct {
	Status     string    `json:"status"`
	Note       *string   `json:"note,omitempty"`
	ActorRole  string    `json:"actor_role"`
	OccurredAt time.Time `json:"occurred_at"`
	CreatedAt  time.Time `json:"created_at"`
}

func toTrackingEventResponseList(events []*domain.TrackingEvent) []trackingEventResponse {
	out := make([]trackingEventResponse, 0, len(events))
	for _, e := range events {
		out = append(out, trackingEventResponse{Status: string(e.Status), Note: e.Note, ActorRole: string(e.ActorRole), OccurredAt: e.OccurredAt, CreatedAt: e.CreatedAt})
	}
	return out
}

type markShippedRequest struct {
	TrackingNumber string `json:"tracking_number" binding:"required,max=64"`
}

type noteRequest struct {
	Note string `json:"note" binding:"max=500"`
}

type reasonRequest struct {
	Reason string `json:"reason" binding:"required,max=500"`
}

type updateTrackingRequest struct {
	TrackingNumber string `json:"tracking_number" binding:"required,max=64"`
	Reason         string `json:"reason" binding:"required,max=500"`
}

// failureReportRequest: AF-04, delivery failed for good.
type failureReportRequest struct {
	Kind            string `json:"kind" binding:"required,oneof=returned lost"`
	Reason          string `json:"reason" binding:"required,max=500"`
	ExpectedVersion int64  `json:"expected_version" binding:"required,min=1"`
	// EvidenceIDs (PW-038): files uploaded for this shipment.
	EvidenceIDs []string `json:"evidence_ids" binding:"max=5"`
}

type interceptionDecisionRequest struct {
	Accepted bool   `json:"accepted"`
	Note     string `json:"note" binding:"required,max=500"`
}

// ---------- Admin: carriers, zones, fee rules ----------

type createCarrierRequest struct {
	Name string `json:"name" binding:"required"`
	Code string `json:"code" binding:"required"`
	Note string `json:"note" binding:"max=500"`
}

type setCarrierActiveRequest struct {
	IsActive bool   `json:"is_active"`
	Reason   string `json:"reason" binding:"required,max=500"`
}

type carrierResponse struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Code     string `json:"code"`
	IsActive bool   `json:"is_active"`
}

func toCarrierResponse(c *domain.Carrier) carrierResponse {
	return carrierResponse{ID: c.ID, Name: c.Name, Code: c.Code, IsActive: c.IsActive}
}

func toCarrierResponseList(carriers []*domain.Carrier) []carrierResponse {
	out := make([]carrierResponse, 0, len(carriers))
	for _, c := range carriers {
		out = append(out, toCarrierResponse(c))
	}
	return out
}

type createZoneRequest struct {
	Name string `json:"name" binding:"required"`
	Code string `json:"code" binding:"required"`
	Note string `json:"note" binding:"max=500"`
}

type addProvinceRequest struct {
	ProvinceCode string `json:"province_code" binding:"required"`
	Note         string `json:"note" binding:"max=500"`
}

type zoneResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Code string `json:"code"`
}

func toZoneResponse(z *domain.Zone) zoneResponse {
	return zoneResponse{ID: z.ID, Name: z.Name, Code: z.Code}
}

func toZoneResponseList(zones []*domain.Zone) []zoneResponse {
	out := make([]zoneResponse, 0, len(zones))
	for _, z := range zones {
		out = append(out, toZoneResponse(z))
	}
	return out
}

type setFeeRuleRequest struct {
	CarrierID       string `json:"carrier_id" binding:"required"`
	ZoneID          string `json:"zone_id" binding:"required"`
	BaseFeeAmount   int64  `json:"base_fee_amount"`
	FreeWeightGrams int64  `json:"free_weight_grams"`
	ExtraFeePerKg   int64  `json:"extra_fee_per_kg"`
	Reason          string `json:"reason" binding:"required,max=500"`
}

type feeRuleResponse struct {
	ID              string `json:"id"`
	CarrierID       string `json:"carrier_id"`
	ZoneID          string `json:"zone_id"`
	Version         int    `json:"version"`
	BaseFeeAmount   int64  `json:"base_fee_amount"`
	FreeWeightGrams int64  `json:"free_weight_grams"`
	ExtraFeePerKg   int64  `json:"extra_fee_per_kg"`
}

func toFeeRuleResponse(f *domain.FeeRule) feeRuleResponse {
	return feeRuleResponse{
		ID: f.ID, CarrierID: f.CarrierID, ZoneID: f.ZoneID, Version: f.Version,
		BaseFeeAmount: f.BaseFeeAmount, FreeWeightGrams: f.FreeWeightGrams, ExtraFeePerKg: f.ExtraFeePerKg,
	}
}

func toFeeRuleResponseList(rules []*domain.FeeRule) []feeRuleResponse {
	out := make([]feeRuleResponse, 0, len(rules))
	for _, f := range rules {
		out = append(out, toFeeRuleResponse(f))
	}
	return out
}

// ---------- Vendor shipping methods ----------

type enableShippingMethodRequest struct {
	VendorID  string `json:"vendor_id" binding:"required"`
	CarrierID string `json:"carrier_id" binding:"required"`
}

type setActiveRequest struct {
	IsActive bool `json:"is_active"`
}

type vendorShippingMethodResponse struct {
	ID        string `json:"id"`
	CarrierID string `json:"carrier_id"`
	IsDefault bool   `json:"is_default"`
	IsActive  bool   `json:"is_active"`
}

func toVendorShippingMethodResponse(m *domain.VendorShippingMethod) vendorShippingMethodResponse {
	return vendorShippingMethodResponse{ID: m.ID, CarrierID: m.CarrierID, IsDefault: m.IsDefault, IsActive: m.IsActive}
}

func toVendorShippingMethodResponseList(methods []*domain.VendorShippingMethod) []vendorShippingMethodResponse {
	out := make([]vendorShippingMethodResponse, 0, len(methods))
	for _, m := range methods {
		out = append(out, toVendorShippingMethodResponse(m))
	}
	return out
}

// ---------- Internal: Order -> Shipment ----------

type internalCreateShipmentRequest struct {
	VendorOrderID      string `json:"vendor_order_id" binding:"required"`
	VendorID           string `json:"vendor_id" binding:"required"`
	BuyerID            string `json:"buyer_id" binding:"required"`
	PackageWeightGrams int64  `json:"package_weight_grams"`
	RecipientName      string `json:"recipient_name" binding:"required"`
	Phone              string `json:"phone" binding:"required"`
	Province           string `json:"province" binding:"required"`
	District           string `json:"district"`
	Ward               string `json:"ward"`
	StreetAddress      string `json:"street_address" binding:"required"`
	// Quote is Order's checkout-time fee snapshot for this vendor order.
	Quote *internalQuotedFee `json:"quote"`
}

type internalQuotedFee struct {
	FeeAmount int64  `json:"fee_amount" binding:"min=0"`
	CarrierID string `json:"carrier_id" binding:"required"`
	ZoneID    string `json:"zone_id" binding:"required"`
	FeeRuleID string `json:"fee_rule_id" binding:"required"`
}

type internalShipmentResponse struct {
	ID        string `json:"id"`
	FeeAmount int64  `json:"fee_amount"`
}

func toInternalShipmentResponse(s *domain.Shipment) internalShipmentResponse {
	return internalShipmentResponse{ID: s.ID, FeeAmount: s.FeeAmount}
}
