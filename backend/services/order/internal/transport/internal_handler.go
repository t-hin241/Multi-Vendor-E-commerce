package transport

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/events"
	"shopee/backend/pkg/httpresponse"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/usecase"
)

// InternalHandler serves the service-authenticated contracts other
// services use: Payment (read an order, report captures/failures and
// refund outcomes), Shipment (vendor order + fulfillment gate), Inventory
// (status, expiry events), Catalog and Review read models.
type InternalHandler struct {
	orders *usecase.OrderUseCase
	log    zerolog.Logger
}

func NewInternalHandler(orders *usecase.OrderUseCase, log zerolog.Logger) *InternalHandler {
	return &InternalHandler{orders: orders, log: log}
}

// Get serves Payment before it opens an intent. An unpaid order that is
// still being prepared answers 409 order_not_ready.
func (h *InternalHandler) Get(c *gin.Context) {
	if !validID(c, c.Param("id")) {
		return
	}
	order, err := h.orders.GetForPayment(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	response := toInternalOrderResponse(order)
	if order.Status == domain.StatusPendingPayment {
		receipt, err := h.orders.Reservation(c.Request.Context(), order.ID)
		if err != nil {
			httpresponse.HandleError(c, h.log, err)
			return
		}
		response.InventoryStatus = receipt.Status
		response.ReservationExpiresAt = &receipt.ExpiresAt
	}
	httpresponse.OK(c, http.StatusOK, response)
}

// internalVendorOrderResponse carries the buyer's destination and package
// weight for Shipment, plus Order's fulfillment decision and the shipping
// quote snapshot, so Shipment never re-derives either.
type internalVendorOrderResponse struct {
	ID          string `json:"id"`
	OrderID     string `json:"order_id"`
	VendorID    string `json:"vendor_id"`
	Status      string `json:"status"`
	Fulfillable bool   `json:"fulfillable"`

	BuyerID            string `json:"buyer_id"`
	RecipientName      string `json:"recipient_name"`
	Phone              string `json:"phone"`
	Province           string `json:"province"`
	District           string `json:"district"`
	Ward               string `json:"ward"`
	StreetAddress      string `json:"street_address"`
	PackageWeightGrams int64  `json:"package_weight_grams"`

	ShippingFeeAmount *int64  `json:"shipping_fee_amount,omitempty"`
	ShippingCarrierID *string `json:"shipping_carrier_id,omitempty"`
	ShippingZoneID    *string `json:"shipping_zone_id,omitempty"`
	ShippingFeeRuleID *string `json:"shipping_fee_rule_id,omitempty"`
}

func (h *InternalHandler) GetVendorOrder(c *gin.Context) {
	if !validID(c, c.Param("id")) {
		return
	}
	v, err := h.orders.GetVendorOrderForInternal(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	vo, order := v.VendorOrder, v.Order
	resp := internalVendorOrderResponse{
		ID: vo.ID, OrderID: vo.OrderID, VendorID: vo.VendorID, Status: string(vo.Status), Fulfillable: v.Fulfillable,
		BuyerID: order.BuyerID, RecipientName: order.RecipientName, Phone: order.Phone,
		Province: order.Province, District: order.District, Ward: order.Ward, StreetAddress: order.StreetAddress,
		PackageWeightGrams: v.PackageWeightGrams,
	}
	if s := vo.Shipping; s != nil && s.CarrierID != "" && s.ZoneID != "" {
		resp.ShippingFeeAmount, resp.ShippingCarrierID, resp.ShippingZoneID, resp.ShippingFeeRuleID = &s.FeeAmount, &s.CarrierID, &s.ZoneID, &s.FeeRuleID
	}
	httpresponse.OK(c, http.StatusOK, resp)
}

// MarkPaid receives Payment's capture report. Order compares payment_id,
// amount and currency with its own snapshot; a capture that cannot pay the
// order is recorded and answered 409 for Payment's review queue.
func (h *InternalHandler) MarkPaid(c *gin.Context) {
	if !validID(c, c.Param("id")) {
		return
	}
	var req markPaidRequest
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			httpresponse.Error(c, http.StatusBadRequest, "validation_error", "payment_id, amount and currency must be valid")
			return
		}
	}
	var capture *domain.PaymentCapture
	switch {
	case req.PaymentID != "" && req.Amount > 0 && req.Currency != "":
		capture = &domain.PaymentCapture{PaymentID: req.PaymentID, Amount: req.Amount, Currency: strings.ToUpper(req.Currency)}
	case req.PaymentID != "" || req.Amount != 0 || req.Currency != "":
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "payment_id, amount and currency must be sent together")
		return
	default:
		h.log.Warn().Str("order_id", c.Param("id")).Msg("order_mark_paid_legacy_contract")
	}
	order, err := h.orders.MarkPaid(c.Request.Context(), c.Param("id"), capture)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toOrderResponse(order))
}

func (h *InternalHandler) MarkPaymentFailed(c *gin.Context) {
	if !validID(c, c.Param("id")) {
		return
	}
	var req markPaymentFailedRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "reason is required")
		return
	}
	order, err := h.orders.MarkPaymentFailed(c.Request.Context(), c.Param("id"), req.Reason)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toOrderResponse(order))
}

type shipmentEventRequest struct {
	EventID       string    `json:"event_id" binding:"required,uuid"`
	ShipmentID    string    `json:"shipment_id" binding:"required,uuid"`
	VendorOrderID string    `json:"vendor_order_id" binding:"required,uuid"`
	Type          string    `json:"type" binding:"required,oneof=shipped delivered returned"`
	OccurredAt    time.Time `json:"occurred_at" binding:"required"`
}

// ShipmentEvent receives Shipment's fulfillment facts; Order decides the
// vendor order's status from them.
func (h *InternalHandler) ShipmentEvent(c *gin.Context) {
	var req shipmentEventRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "Invalid shipment event")
		return
	}
	err := h.orders.ApplyShipmentEvent(c.Request.Context(), usecase.ShipmentEvent{EventID: req.EventID, ShipmentID: req.ShipmentID,
		VendorOrderID: req.VendorOrderID, Type: req.Type, OccurredAt: req.OccurredAt})
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"received": true})
}

type settlementHoldsRequest struct {
	VendorOrderIDs []string `json:"vendor_order_ids" binding:"required,max=500,dive,uuid"`
}

type settlementHold struct {
	VendorOrderID string `json:"vendor_order_id"`
	Reason        string `json:"reason"`
}

// SettlementHolds tells Payment which vendor orders have an open return or
// refund and must not be paid out yet.
func (h *InternalHandler) SettlementHolds(c *gin.Context) {
	var req settlementHoldsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "vendor_order_ids must be at most 500 ids")
		return
	}
	held, err := h.orders.SettlementHolds(c.Request.Context(), req.VendorOrderIDs)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	out := make([]settlementHold, 0, len(held))
	for id, reason := range held {
		out = append(out, settlementHold{VendorOrderID: id, Reason: reason})
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"held": out})
}

// RefundEvent receives Payment's confirmed refund outcome.
func (h *InternalHandler) RefundEvent(c *gin.Context) {
	var req refundEventRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "Invalid refund event")
		return
	}
	err := h.orders.ApplyRefundOutcome(c.Request.Context(), domain.RefundOutcome{
		RefundID: req.RefundID, PaymentRefundID: req.PaymentRefundID, Status: domain.RefundStatus(req.Status),
		Amount: req.Amount, Currency: strings.ToUpper(req.Currency), FailureReason: req.FailureReason,
	})
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"received": true})
}

type quantitySoldResponse struct {
	ProductID    string `json:"product_id"`
	QuantitySold int64  `json:"quantity_sold"`
}

type reviewEligibilityResponse struct {
	OrderItemID   string  `json:"order_item_id"`
	VendorOrderID string  `json:"vendor_order_id"`
	ProductID     string  `json:"product_id"`
	VendorID      string  `json:"vendor_id"`
	ProductName   string  `json:"product_name"`
	VariantLabel  *string `json:"variant_label,omitempty"`
	CompletedAt   string  `json:"completed_at"`
}

func (h *InternalHandler) ListReviewEligibility(c *gin.Context) {
	buyerID := strings.TrimSpace(c.Query("buyer_id"))
	if !validID(c, buyerID) {
		return
	}
	productID := c.Query("product_id")
	if productID != "" && !validID(c, productID) {
		return
	}
	items, err := h.orders.ListReviewEligibility(c.Request.Context(), buyerID, productID)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	out := make([]reviewEligibilityResponse, 0, len(items))
	for _, item := range items {
		out = append(out, reviewEligibilityResponse{OrderItemID: item.OrderItemID, VendorOrderID: item.VendorOrderID, ProductID: item.ProductID, VendorID: item.VendorID, ProductName: item.ProductName, VariantLabel: item.VariantLabel, CompletedAt: item.CompletedAt.UTC().Format(time.RFC3339)})
	}
	httpresponse.OK(c, http.StatusOK, out)
}

// QuantitySoldByProductIDs lets Catalog resolve units-sold for a batch of
// product ids in one call.
func (h *InternalHandler) QuantitySoldByProductIDs(c *gin.Context) {
	var productIDs []string
	for _, id := range strings.Split(c.Query("product_ids"), ",") {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			productIDs = append(productIDs, trimmed)
		}
	}
	if len(productIDs) > 100 {
		httpresponse.HandleError(c, h.log, apperror.Validation("Maximum 100 IDs"))
		return
	}
	for _, id := range productIDs {
		if _, err := uuid.Parse(id); err != nil {
			httpresponse.HandleError(c, h.log, apperror.Validation("Invalid ID"))
			return
		}
	}
	quantities, err := h.orders.QuantitySoldByProductIDs(c.Request.Context(), productIDs)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	out := make([]quantitySoldResponse, 0, len(quantities))
	for productID, quantity := range quantities {
		out = append(out, quantitySoldResponse{ProductID: productID, QuantitySold: quantity})
	}
	httpresponse.OK(c, http.StatusOK, out)
}

func (h *InternalHandler) InventoryStatus(c *gin.Context) {
	if !validID(c, c.Param("id")) {
		return
	}
	o, err := h.orders.OrderStatusForInventory(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"order_id": o.ID, "status": o.Status})
}

func (h *InternalHandler) InventoryEvent(c *gin.Context) {
	var event struct {
		ID      string `json:"id" binding:"required,uuid"`
		OrderID string `json:"order_id" binding:"required,uuid"`
		Type    string `json:"type" binding:"required,eq=ReservationExpired"`
	}
	if err := c.ShouldBindJSON(&event); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "Invalid inventory event")
		return
	}
	if err := h.orders.ReservationExpired(c.Request.Context(), event.OrderID); err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"received": true})
}

// PolicyRuleReadiness tells Vendor whether Order enforces a rule version
// a policy cites (AF-02). It never says ready for a rule it does not run.
func (h *InternalHandler) PolicyRuleReadiness(c *gin.Context) {
	key, value := c.Query("key"), c.Query("value")
	if key == "" || value == "" || len(key) > 100 || len(value) > 100 {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "key and value are required")
		return
	}
	ready, hash, reason := h.orders.PolicyRuleReadiness(key, value)
	httpresponse.OK(c, http.StatusOK, gin.H{"ready": ready, "rule_hash": hash, "reason": reason})
}

// PolicyPublished receives a publication over HTTP (Vendor in
// EVENT_PUBLISHING=http, rollback only); same use case as the event.
func (h *InternalHandler) PolicyPublished(c *gin.Context) {
	var p events.PolicyPublication
	if err := c.ShouldBindJSON(&p); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "Invalid policy publication")
		return
	}
	if err := h.orders.ApplyPolicyPublished(c.Request.Context(), policyVersionOf(p)); err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"received": true})
}
