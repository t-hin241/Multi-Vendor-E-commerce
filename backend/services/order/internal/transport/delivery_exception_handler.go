package transport

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/usecase"
)

// DeliveryExceptionHandler serves AF-04: failed deliveries and returned
// goods. The buyer follows the case and answers a redelivery offer, the
// shop records what came back, admins decide; Shipment reports the facts.
type DeliveryExceptionHandler struct {
	orders *usecase.OrderUseCase
	log    zerolog.Logger
}

func NewDeliveryExceptionHandler(orders *usecase.OrderUseCase, log zerolog.Logger) *DeliveryExceptionHandler {
	return &DeliveryExceptionHandler{orders: orders, log: log}
}

type receiptLineResponse struct {
	OrderItemID string `json:"order_item_id"`
	Condition   string `json:"condition"`
	Quantity    int64  `json:"quantity"`
}

type receiptResponse struct {
	Version   int                   `json:"version"`
	ActorRole string                `json:"actor_role"`
	Note      *string               `json:"note,omitempty"`
	Lines     []receiptLineResponse `json:"lines"`
	CreatedAt time.Time             `json:"created_at"`
}

type deliveryExceptionResponse struct {
	ID                    string              `json:"id"`
	OrderID               string              `json:"order_id"`
	VendorOrderID         string              `json:"vendor_order_id"`
	VendorID              string              `json:"vendor_id"`
	ShipmentID            string              `json:"shipment_id"`
	ExceptionType         string              `json:"exception_type"`
	CurrentShipmentID     string              `json:"current_shipment_id"`
	AttemptNo             int                 `json:"attempt_no"`
	CarrierOutcome        string              `json:"carrier_outcome"`
	FailedAttempts        int                 `json:"failed_attempts"`
	DetectionReason       *string             `json:"detection_reason,omitempty"`
	Status                string              `json:"status"`
	Resolution            *string             `json:"resolution,omitempty"`
	PolicyVersion         string              `json:"policy_version"`
	RedeliveryLimit       int                 `json:"redelivery_limit"`
	HoldStatus            *string             `json:"hold_status,omitempty"`
	HoldNote              *string             `json:"hold_note,omitempty"`
	RedeliveryCount       int                 `json:"redelivery_count"`
	ReplacementShipmentID *string             `json:"replacement_shipment_id,omitempty"`
	RedeliveryAddress     *domain.Destination `json:"redelivery_address,omitempty"`
	ConsentedAt           *time.Time          `json:"consented_at,omitempty"`
	RefundID              *string             `json:"refund_id,omitempty"`
	StockRecovered        bool                `json:"stock_recovered"`
	DecidedAt             *time.Time          `json:"decided_at,omitempty"`
	DecisionReason        *string             `json:"decision_reason,omitempty"`
	ReviewReason          *string             `json:"review_reason,omitempty"`
	LateDeliveryAt        *time.Time          `json:"late_delivery_at,omitempty"`
	ResolvedAt            *time.Time          `json:"resolved_at,omitempty"`
	Receipt               *receiptResponse    `json:"receipt,omitempty"`
	ActionDueAt           *time.Time          `json:"action_due_at,omitempty"`
	WaitingOn             string              `json:"waiting_on,omitempty"`
	Version               int64               `json:"version"`
	CreatedAt             time.Time           `json:"created_at"`
	UpdatedAt             time.Time           `json:"updated_at"`
}

func toDeliveryException(d *domain.DeliveryException) deliveryExceptionResponse {
	out := deliveryExceptionResponse{ID: d.ID, OrderID: d.OrderID, VendorOrderID: d.VendorOrderID, VendorID: d.VendorID, ShipmentID: d.ShipmentID,
		ExceptionType: d.ExceptionType, CurrentShipmentID: d.CurrentShipmentID, AttemptNo: d.AttemptNo, CarrierOutcome: d.CarrierOutcome,
		FailedAttempts: d.FailedAttempts, DetectionReason: d.DetectionReason, Status: string(d.Status), Resolution: d.Resolution,
		PolicyVersion: d.Policy.Version, RedeliveryLimit: d.Policy.RedeliveryLimit, HoldStatus: d.HoldStatus, HoldNote: d.HoldNote,
		RedeliveryCount: d.RedeliveryCount, ReplacementShipmentID: d.ReplacementShipmentID, RedeliveryAddress: d.RedeliveryAddress,
		ConsentedAt: d.ConsentedAt, RefundID: d.RefundID, StockRecovered: d.RecoveryRef != nil, DecidedAt: d.DecidedAt,
		DecisionReason: d.DecisionReason, ReviewReason: d.ReviewReason, LateDeliveryAt: d.LateDeliveryAt, ResolvedAt: d.ResolvedAt,
		ActionDueAt: d.ActionDueAt, WaitingOn: d.WaitingOn, Version: d.Version, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt}
	if r := d.Receipt; r != nil {
		rr := &receiptResponse{Version: r.Version, ActorRole: r.ActorRole, Note: r.Note, CreatedAt: r.CreatedAt, Lines: []receiptLineResponse{}}
		for _, l := range r.Lines {
			rr.Lines = append(rr.Lines, receiptLineResponse{OrderItemID: l.OrderItemID, Condition: l.Condition, Quantity: l.Quantity})
		}
		out.Receipt = rr
	}
	return out
}

func toDeliveryExceptions(items []*domain.DeliveryException) []deliveryExceptionResponse {
	out := make([]deliveryExceptionResponse, 0, len(items))
	for _, d := range items {
		out = append(out, toDeliveryException(d))
	}
	return out
}

type deliveryEventResponse struct {
	ActorID    *string   `json:"actor_id,omitempty"`
	ActorRole  string    `json:"actor_role"`
	Action     string    `json:"action"`
	FromStatus *string   `json:"from_status"`
	ToStatus   string    `json:"to_status"`
	Note       *string   `json:"note,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

func (h *DeliveryExceptionHandler) Get(c *gin.Context) {
	id := c.Param("exceptionID")
	if !validID(c, id) {
		return
	}
	detail, err := h.orders.GetDeliveryException(c.Request.Context(), actorOf(c), id)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	events := make([]deliveryEventResponse, 0, len(detail.Events))
	for _, e := range detail.Events {
		events = append(events, deliveryEventResponse{ActorID: e.ActorID, ActorRole: e.ActorRole, Action: e.Action, FromStatus: e.FromStatus,
			ToStatus: e.ToStatus, Note: e.Note, CreatedAt: e.CreatedAt})
	}
	c.Header("Cache-Control", "no-store")
	httpresponse.OK(c, http.StatusOK, gin.H{"exception": toDeliveryException(detail.Exception), "events": events})
}

// OrderList: the buyer's cases on one of their orders.
func (h *DeliveryExceptionHandler) OrderList(c *gin.Context) {
	if !validID(c, c.Param("id")) {
		return
	}
	items, err := h.orders.ListOrderDeliveryExceptions(c.Request.Context(), middleware.GetUserID(c), c.Param("id"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	httpresponse.OK(c, http.StatusOK, toDeliveryExceptions(items))
}

func validDeliveryFilter(status string) bool {
	switch status {
	case "", "open", "investigating", "awaiting_goods", "awaiting_buyer", "redelivery_pending", "refund_pending", "needs_review", "resolved":
		return true
	}
	return false
}

func (h *DeliveryExceptionHandler) VendorList(c *gin.Context) {
	if !validDeliveryFilter(c.Query("status")) {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "Unknown status filter")
		return
	}
	limit, offset := pageParams(c)
	items, err := h.orders.ListVendorDeliveryExceptions(c.Request.Context(), middleware.GetUserID(c), c.Query("vendor_id"), c.Query("status"), limit, offset)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	httpresponse.OK(c, http.StatusOK, toDeliveryExceptions(items))
}

func (h *DeliveryExceptionHandler) AdminList(c *gin.Context) {
	status := c.DefaultQuery("status", "open")
	if !validDeliveryFilter(status) {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "Unknown status filter")
		return
	}
	limit, offset := pageParams(c)
	items, err := h.orders.ListDeliveryExceptions(c.Request.Context(), status, limit, offset)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	httpresponse.OK(c, http.StatusOK, toDeliveryExceptions(items))
}

type receiptLineRequest struct {
	ItemID    string `json:"item_id" binding:"required,uuid"`
	Quantity  int64  `json:"quantity" binding:"required,min=1"`
	Condition string `json:"condition" binding:"required,oneof=sellable damaged missing"`
}

type receiptRequest struct {
	ReceivedLines   []receiptLineRequest `json:"received_lines" binding:"required,min=1,max=300,dive"`
	Note            string               `json:"note" binding:"max=1000"`
	ExpectedVersion int64                `json:"expected_version" binding:"required,min=1"`
	// PW-038: images of the step (uploaded first like support evidence).
	EvidenceIDs []string `json:"evidence_ids" binding:"omitempty,max=5,dive,uuid"`
}

// Receipt: the shop (returns.handle) records what came back; an admin may
// correct it with a note.
func (h *DeliveryExceptionHandler) Receipt(c *gin.Context) {
	id := c.Param("exceptionID")
	if !validID(c, id) {
		return
	}
	var req receiptRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error",
			"received_lines (item_id, quantity, condition sellable|damaged|missing) and expected_version are required")
		return
	}
	lines := make([]domain.ReceiptLine, 0, len(req.ReceivedLines))
	for _, l := range req.ReceivedLines {
		lines = append(lines, domain.ReceiptLine{OrderItemID: l.ItemID, Condition: l.Condition, Quantity: l.Quantity})
	}
	out, err := h.orders.RecordGoodsReceipt(c.Request.Context(), actorOf(c), id,
		usecase.ReceiptInput{Lines: lines, Note: req.Note, ExpectedVersion: req.ExpectedVersion, EvidenceIDs: req.EvidenceIDs})
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toDeliveryException(out))
}

type deliveryDecisionRequest struct {
	Resolution      string `json:"resolution" binding:"required,oneof=redeliver refund close retry_refund"`
	Reason          string `json:"reason" binding:"required,max=500"`
	ExpectedVersion int64  `json:"expected_version" binding:"required,min=1"`
}

func (h *DeliveryExceptionHandler) Decide(c *gin.Context) {
	id := c.Param("exceptionID")
	if !validID(c, id) {
		return
	}
	var req deliveryDecisionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error",
			"resolution (redeliver|refund|close|retry_refund), reason and expected_version are required")
		return
	}
	out, err := h.orders.DecideDeliveryException(c.Request.Context(), middleware.GetUserID(c), id,
		usecase.DeliveryDecision{Resolution: req.Resolution, Reason: req.Reason, ExpectedVersion: req.ExpectedVersion})
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toDeliveryException(out))
}

type consentRequest struct {
	Accept          *bool  `json:"accept" binding:"required"`
	AddressID       string `json:"address_id" binding:"omitempty,uuid"`
	ExpectedVersion int64  `json:"expected_version" binding:"required,min=1"`
}

// Consent: the buyer accepts the redelivery (to the order's address or a
// saved address_id) or declines it.
func (h *DeliveryExceptionHandler) Consent(c *gin.Context) {
	id := c.Param("exceptionID")
	if !validID(c, id) {
		return
	}
	var req consentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "accept and expected_version are required; address_id must be an id")
		return
	}
	out, err := h.orders.ConsentRedelivery(c.Request.Context(), middleware.GetUserID(c), id,
		usecase.ConsentInput{Accept: *req.Accept, AddressID: req.AddressID, ExpectedVersion: req.ExpectedVersion})
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toDeliveryException(out))
}

type shipmentExceptionRequest struct {
	EventID        string    `json:"event_id" binding:"required,uuid"`
	ShipmentID     string    `json:"shipment_id" binding:"required,uuid"`
	VendorOrderID  string    `json:"vendor_order_id" binding:"required,uuid"`
	ExceptionType  string    `json:"exception_type" binding:"required,oneof=attempts_exhausted returned lost"`
	AttemptNo      int       `json:"attempt_no" binding:"min=0,max=10"`
	FailedAttempts int       `json:"failed_attempts" binding:"min=0"`
	Reason         string    `json:"reason" binding:"max=500"`
	OccurredAt     time.Time `json:"occurred_at" binding:"required"`
}

// ShipmentException receives Shipment's exception facts over HTTP
// (rollback mode without the event bus).
func (h *DeliveryExceptionHandler) ShipmentException(c *gin.Context) {
	var req shipmentExceptionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "Invalid shipment exception")
		return
	}
	err := h.orders.ApplyShipmentException(c.Request.Context(), usecase.ShipmentExceptionFact{EventID: req.EventID, ShipmentID: req.ShipmentID,
		VendorOrderID: req.VendorOrderID, Type: req.ExceptionType, AttemptNo: req.AttemptNo, FailedAttempts: req.FailedAttempts,
		Reason: req.Reason, OccurredAt: req.OccurredAt})
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"received": true})
}
