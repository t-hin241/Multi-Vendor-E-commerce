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
	ID                   string     `json:"id"`
	VendorOrderID        string     `json:"vendor_order_id"`
	Status               string     `json:"status"`
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
	InterceptRequestedAt *time.Time `json:"intercept_requested_at,omitempty"`
	InterceptResolvedAt  *time.Time `json:"intercept_resolved_at,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

func toShipmentResponse(s *domain.Shipment) shipmentResponse {
	return shipmentResponse{
		ID: s.ID, VendorOrderID: s.VendorOrderID, Status: string(s.Status),
		CarrierID: s.CarrierID, TrackingNumber: s.TrackingNumber,
		ZoneName: s.ZoneName, FeeAmount: s.FeeAmount, PackageWeightGrams: s.PackageWeightGrams,
		RecipientName: s.RecipientName, Phone: s.Phone, Province: s.Province,
		District: s.District, Ward: s.Ward, StreetAddress: s.StreetAddress,
		ShippedAt: s.ShippedAt, DeliveredAt: s.DeliveredAt,
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
	Status    string    `json:"status"`
	Note      *string   `json:"note,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

func toTrackingEventResponseList(events []*domain.TrackingEvent) []trackingEventResponse {
	out := make([]trackingEventResponse, 0, len(events))
	for _, e := range events {
		out = append(out, trackingEventResponse{Status: string(e.Status), Note: e.Note, CreatedAt: e.CreatedAt})
	}
	return out
}

// ---------- Admin: carriers, zones, fee rules ----------

type createCarrierRequest struct {
	Name string `json:"name" binding:"required"`
	Code string `json:"code" binding:"required"`
}

type setCarrierActiveRequest struct {
	IsActive bool `json:"is_active"`
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
}

type addProvinceRequest struct {
	ProvinceCode string `json:"province_code" binding:"required"`
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
}

type internalShipmentResponse struct {
	ID        string `json:"id"`
	FeeAmount int64  `json:"fee_amount"`
}

func toInternalShipmentResponse(s *domain.Shipment) internalShipmentResponse {
	return internalShipmentResponse{ID: s.ID, FeeAmount: s.FeeAmount}
}
