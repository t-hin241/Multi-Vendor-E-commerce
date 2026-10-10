package transport

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/usecase"
)

// CancellationHandler serves AF-03: a buyer or vendor asks to cancel a
// paid package before handover, admins decide, and Shipment claims the
// handover grant.
type CancellationHandler struct {
	orders *usecase.OrderUseCase
	log    zerolog.Logger
}

func NewCancellationHandler(orders *usecase.OrderUseCase, log zerolog.Logger) *CancellationHandler {
	return &CancellationHandler{orders: orders, log: log}
}

type cancellationResponse struct {
	ID             string     `json:"id"`
	OrderID        string     `json:"order_id"`
	VendorOrderID  string     `json:"vendor_order_id"`
	VendorID       string     `json:"vendor_id"`
	BuyerID        string     `json:"buyer_id,omitempty"`
	Origin         string     `json:"origin"`
	ReasonCode     string     `json:"reason_code"`
	Reason         string     `json:"reason"`
	Status         string     `json:"status"`
	PolicyVersion  string     `json:"policy_version"`
	HoldStatus     *string    `json:"hold_status,omitempty"`
	HoldNote       *string    `json:"hold_note,omitempty"`
	StopResult     *string    `json:"stop_result,omitempty"`
	Restock        *bool      `json:"restock,omitempty"`
	RefundID       *string    `json:"refund_id,omitempty"`
	DecidedAt      *time.Time `json:"decided_at,omitempty"`
	DecisionReason *string    `json:"decision_reason,omitempty"`
	ReviewReason   *string    `json:"review_reason,omitempty"`
	ResolvedAt     *time.Time `json:"resolved_at,omitempty"`
	// PW-036: interception asked; the failed-delivery case it moved to.
	InterceptionRequestedAt *time.Time `json:"interception_requested_at,omitempty"`
	DeliveryExceptionID     *string    `json:"delivery_exception_id,omitempty"`
	ActionDueAt             *time.Time `json:"action_due_at,omitempty"`
	WaitingOn               string     `json:"waiting_on,omitempty"`
	Version                 int64      `json:"version"`
	CreatedAt               time.Time  `json:"created_at"`
	UpdatedAt               time.Time  `json:"updated_at"`
}

func toCancellation(c *domain.CancellationRequest) cancellationResponse {
	return cancellationResponse{ID: c.ID, OrderID: c.OrderID, VendorOrderID: c.VendorOrderID, VendorID: c.VendorID, BuyerID: c.BuyerID,
		Origin: c.Origin, ReasonCode: c.ReasonCode, Reason: c.Reason, Status: string(c.Status), PolicyVersion: c.PolicyVersion,
		HoldStatus: c.HoldStatus, HoldNote: c.HoldNote, StopResult: c.StopResult, Restock: c.Restock, RefundID: c.RefundID,
		DecidedAt: c.DecidedAt, DecisionReason: c.DecisionReason, ReviewReason: c.ReviewReason, ResolvedAt: c.ResolvedAt,
		InterceptionRequestedAt: c.InterceptionRequestedAt, DeliveryExceptionID: c.DeliveryExceptionID,
		ActionDueAt: c.ActionDueAt, WaitingOn: c.WaitingOn, Version: c.Version, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt}
}

func toCancellations(items []*domain.CancellationRequest) []cancellationResponse {
	out := make([]cancellationResponse, 0, len(items))
	for _, c := range items {
		out = append(out, toCancellation(c))
	}
	return out
}

type cancellationEventResponse struct {
	ActorID    *string   `json:"actor_id,omitempty"`
	ActorRole  string    `json:"actor_role"`
	Action     string    `json:"action"`
	FromStatus *string   `json:"from_status"`
	ToStatus   string    `json:"to_status"`
	Note       *string   `json:"note,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type createCancellationRequest struct {
	ReasonCode string `json:"reason_code" binding:"required,max=40"`
	Reason     string `json:"reason" binding:"required,max=1000"`
}

// Create: buyer (own order) or vendor (orders.fulfill) on a vendor order:
// 202 (the request is preparing or waiting), 200 for a key replay.
func (h *CancellationHandler) Create(c *gin.Context) {
	if !validID(c, c.Param("id")) {
		return
	}
	var req createCancellationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "reason_code and a reason of at most 1000 characters are required")
		return
	}
	out, replayed, err := h.orders.RequestCancellation(c.Request.Context(), actorOf(c), c.Param("id"),
		usecase.CancellationInput{ReasonCode: req.ReasonCode, Reason: req.Reason, IdempotencyKey: c.GetHeader("Idempotency-Key")})
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	status := http.StatusAccepted
	if replayed {
		status = http.StatusOK
	}
	httpresponse.OK(c, status, toCancellation(out))
}

func (h *CancellationHandler) Get(c *gin.Context) {
	id := c.Param("requestID")
	if !validID(c, id) {
		return
	}
	detail, err := h.orders.GetCancellation(c.Request.Context(), actorOf(c), id)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	events := make([]cancellationEventResponse, 0, len(detail.Events))
	for _, e := range detail.Events {
		events = append(events, cancellationEventResponse{ActorID: e.ActorID, ActorRole: e.ActorRole, Action: e.Action, FromStatus: e.FromStatus,
			ToStatus: e.ToStatus, Note: e.Note, CreatedAt: e.CreatedAt})
	}
	c.Header("Cache-Control", "no-store")
	httpresponse.OK(c, http.StatusOK, gin.H{"request": toCancellation(detail.Request), "events": events})
}

// OrderList: the buyer's requests on one of their orders.
func (h *CancellationHandler) OrderList(c *gin.Context) {
	if !validID(c, c.Param("id")) {
		return
	}
	items, err := h.orders.ListOrderCancellations(c.Request.Context(), middleware.GetUserID(c), c.Param("id"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	httpresponse.OK(c, http.StatusOK, toCancellations(items))
}

func pageParams(c *gin.Context) (int, int) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if limit < 1 || limit > 100 {
		limit = 50
	}
	if offset < 0 || offset > 10_000 {
		offset = 0
	}
	return limit, offset
}

func validCancellationFilter(status string) bool {
	switch status {
	case "", "open", "preparing", "requested", "stopping_fulfillment", "approved", "refund_pending", "resolved", "rejected", "needs_review":
		return true
	}
	return false
}

func (h *CancellationHandler) VendorList(c *gin.Context) {
	if !validCancellationFilter(c.Query("status")) {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "Unknown status filter")
		return
	}
	limit, offset := pageParams(c)
	items, err := h.orders.ListVendorCancellations(c.Request.Context(), middleware.GetUserID(c), c.Query("vendor_id"), c.Query("status"), limit, offset)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toCancellations(items))
}

func (h *CancellationHandler) AdminList(c *gin.Context) {
	status := c.DefaultQuery("status", "open")
	if !validCancellationFilter(status) {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "Unknown status filter")
		return
	}
	limit, offset := pageParams(c)
	items, err := h.orders.ListCancellations(c.Request.Context(), status, limit, offset)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	httpresponse.OK(c, http.StatusOK, toCancellations(items))
}

type cancellationDecisionRequest struct {
	Decision        string `json:"decision" binding:"required,oneof=approve reject retry_refund intercept"`
	Reason          string `json:"reason" binding:"required,max=500"`
	ExpectedVersion int64  `json:"expected_version" binding:"required,min=1"`
	Restock         *bool  `json:"restock"`
}

func (h *CancellationHandler) Decide(c *gin.Context) {
	id := c.Param("requestID")
	if !validID(c, id) {
		return
	}
	var req cancellationDecisionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "decision (approve|reject|retry_refund|intercept), reason and expected_version are required")
		return
	}
	out, err := h.orders.DecideCancellation(c.Request.Context(), middleware.GetUserID(c), id, usecase.CancellationDecision{Decision: req.Decision,
		Reason: req.Reason, ExpectedVersion: req.ExpectedVersion, Restock: req.Restock})
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toCancellation(out))
}

type claimHandoverRequest struct {
	ShipmentID string `json:"shipment_id" binding:"required,uuid"`
}

// ClaimHandover is Shipment claiming the fulfillment grant (AF-03): 200
// granted, 409 cancellation_pending / not fulfillable.
func (h *CancellationHandler) ClaimHandover(c *gin.Context) {
	if !validID(c, c.Param("id")) {
		return
	}
	var req claimHandoverRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "shipment_id is required")
		return
	}
	if err := h.orders.ClaimHandover(c.Request.Context(), c.Param("id"), req.ShipmentID); err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"vendor_order_id": c.Param("id"), "granted": true})
}
