package transport

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/services/shipment/internal/domain"
	"shopee/backend/services/shipment/internal/usecase"
)

// InternalHandler serves the service-authenticated routes Order calls to
// quote, create and cancel shipments.
type InternalHandler struct {
	shipments *usecase.ShipmentUseCase
	log       zerolog.Logger
}

func NewInternalHandler(shipments *usecase.ShipmentUseCase, log zerolog.Logger) *InternalHandler {
	return &InternalHandler{shipments: shipments, log: log}
}

func (h *InternalHandler) CreateShipment(c *gin.Context) {
	var req internalCreateShipmentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	in := usecase.CreateShipmentInput{
		VendorOrderID: req.VendorOrderID, VendorID: req.VendorID, BuyerID: req.BuyerID,
		PackageWeightGrams: req.PackageWeightGrams,
		RecipientName:      req.RecipientName, Phone: req.Phone, Province: req.Province,
		District: req.District, Ward: req.Ward, StreetAddress: req.StreetAddress,
	}
	if req.Quote != nil {
		in.Quote = &domain.QuotedFee{FeeAmount: req.Quote.FeeAmount, CarrierID: req.Quote.CarrierID, ZoneID: req.Quote.ZoneID, FeeRuleID: req.Quote.FeeRuleID}
	}
	shipment, err := h.shipments.CreateAuto(c.Request.Context(), in)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusCreated, toInternalShipmentResponse(shipment))
}

type quoteRequest struct {
	VendorID           string `json:"vendor_id" binding:"required,uuid"`
	Province           string `json:"province" binding:"required,max=100"`
	PackageWeightGrams int64  `json:"package_weight_grams" binding:"min=0"`
}

// a 400 from Quote means shipping is unavailable (no method, zone, fee
// rule or weight), never a zero fee.

type quoteResponse struct {
	VendorID           string    `json:"vendor_id"`
	FeeAmount          int64     `json:"fee_amount"`
	Currency           string    `json:"currency"`
	CarrierID          string    `json:"carrier_id"`
	ZoneID             string    `json:"zone_id"`
	ZoneName           string    `json:"zone_name"`
	FeeRuleID          string    `json:"fee_rule_id"`
	FeeRuleVersion     int       `json:"fee_rule_version"`
	PackageWeightGrams int64     `json:"package_weight_grams"`
	QuotedAt           time.Time `json:"quoted_at"`
	ExpiresAt          time.Time `json:"expires_at"`
}

// Quote: POST /internal/shipments/quotes. Prices one vendor's package to a
// destination without creating anything. 400 means shipping is not
// available (no method, zone or fee rule), never a zero fee.
func (h *InternalHandler) Quote(c *gin.Context) {
	var req quoteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "vendor_id, province and a non-negative package_weight_grams are required")
		return
	}
	q, err := h.shipments.Quote(c.Request.Context(), req.VendorID, req.Province, req.PackageWeightGrams)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, quoteResponse{
		VendorID: q.VendorID, FeeAmount: q.FeeAmount, Currency: q.Currency, CarrierID: q.CarrierID, ZoneID: q.ZoneID,
		ZoneName: q.ZoneName, FeeRuleID: q.FeeRuleID, FeeRuleVersion: q.FeeRuleVersion,
		PackageWeightGrams: q.PackageWeightGrams, QuotedAt: q.QuotedAt, ExpiresAt: q.ExpiresAt,
	})
}

type stopFulfillmentRequest struct {
	OperationID string `json:"operation_id" binding:"required,max=100"`
}

// StopFulfillment answers stopped / handed_over / delivered (AF-03).
func (h *InternalHandler) StopFulfillment(c *gin.Context) {
	var req stopFulfillmentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "operation_id is required")
		return
	}
	if _, err := uuid.Parse(c.Param("id")); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "Invalid vendor order id")
		return
	}
	result, err := h.shipments.StopFulfillment(c.Request.Context(), c.Param("id"), req.OperationID)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"vendor_order_id": c.Param("id"), "operation_id": req.OperationID, "result": result})
}

func (h *InternalHandler) CancelForVendorOrder(c *gin.Context) {
	if err := h.shipments.CancelForVendorOrder(c.Request.Context(), c.Param("id")); err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"cancelled": true})
}
