package transport

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/usecase"
)

// AF-05 return shipping: instructions and dispatch for the buyer, the
// goods receipt for the shop, authorization and decisions for admins.

type returnDestinationResponse struct {
	RecipientName  string `json:"recipient_name"`
	Phone          string `json:"phone"`
	Province       string `json:"province"`
	District       string `json:"district"`
	Ward           string `json:"ward"`
	StreetAddress  string `json:"street_address"`
	ReceivingHours string `json:"receiving_hours"`
}

type shippingInstructionsResponse struct {
	ReturnID             string                    `json:"return_id"`
	ReturnCode           string                    `json:"return_code"`
	AuthorizationVersion int                       `json:"authorization_version"`
	Version              int64                     `json:"version"`
	Deadline             *time.Time                `json:"dispatch_deadline"`
	Overdue              bool                      `json:"dispatch_overdue"`
	FeePayer             *string                   `json:"fee_payer"`
	Address              returnDestinationResponse `json:"address"`
	Instructions         string                    `json:"instructions"`
	ShippingStatus       *string                   `json:"shipping_status"`
	CarrierName          *string                   `json:"carrier_name,omitempty"`
	TrackingNumber       *string                   `json:"tracking_number,omitempty"`
	DispatchedAt         *time.Time                `json:"dispatched_at,omitempty"`
}

// ShippingInstructions: GET /api/orders/return-requests/:id/shipping-instructions
// (the buyer's own authorized return; 409 return_not_approved otherwise).
func (h *ReturnHandler) ShippingInstructions(c *gin.Context) {
	if !validID(c, c.Param("id")) {
		return
	}
	out, err := h.orders.GetShippingInstructions(c.Request.Context(), middleware.GetUserID(c), c.Param("id"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	r, d := out.Return, out.Return.Destination
	c.Header("Cache-Control", "no-store")
	httpresponse.OK(c, http.StatusOK, shippingInstructionsResponse{ReturnID: r.ID, ReturnCode: out.ReturnCode,
		AuthorizationVersion: r.AuthorizationVersion, Version: r.Version, Deadline: r.DispatchDeadline, Overdue: r.DispatchOverdueAt != nil,
		FeePayer: r.FeePayer, Instructions: out.Instructions, ShippingStatus: r.ShippingStatus, CarrierName: r.DispatchCarrier,
		TrackingNumber: r.DispatchTracking, DispatchedAt: r.DispatchedAt,
		Address: returnDestinationResponse{RecipientName: d.RecipientName, Phone: d.Phone, Province: d.Province, District: d.District,
			Ward: d.Ward, StreetAddress: d.StreetAddress, ReceivingHours: d.ReceivingHours}})
}

type returnDispatchRequest struct {
	CarrierName     string    `json:"carrier_name" binding:"required,max=60"`
	TrackingNumber  string    `json:"tracking_number" binding:"required,max=64"`
	DispatchedAt    time.Time `json:"dispatched_at" binding:"required"`
	ExpectedVersion int64     `json:"expected_version" binding:"required,min=1"`
}

// Dispatch: POST /api/orders/return-requests/:id/dispatches with an
// Idempotency-Key; 202, or 200 for a replay of the same key.
func (h *ReturnHandler) Dispatch(c *gin.Context) {
	if !validID(c, c.Param("id")) {
		return
	}
	var req returnDispatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "carrier_name, tracking_number, dispatched_at and expected_version are required")
		return
	}
	item, replayed, err := h.orders.ReportReturnDispatch(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), usecase.ReturnDispatchInput{
		CarrierName: req.CarrierName, TrackingNumber: req.TrackingNumber, DispatchedAt: req.DispatchedAt, ExpectedVersion: req.ExpectedVersion,
		IdempotencyKey: c.GetHeader("Idempotency-Key")})
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	status := http.StatusAccepted
	if replayed {
		status = http.StatusOK
	}
	httpresponse.OK(c, status, toReturnResponse(item))
}

type goodsReceiptRequest struct {
	Sellable        *int64 `json:"sellable_quantity" binding:"required,min=0"`
	Damaged         *int64 `json:"damaged_quantity" binding:"required,min=0"`
	Missing         *int64 `json:"missing_quantity" binding:"required,min=0"`
	Note            string `json:"note" binding:"max=1000"`
	ExpectedVersion int64  `json:"expected_version" binding:"required,min=1"`
}

// GoodsReceipt: the shop (returns.handle) or an admin records what came
// back; the refund follows when everything is sellable.
func (h *ReturnHandler) GoodsReceipt(c *gin.Context) {
	if !validID(c, c.Param("id")) {
		return
	}
	var req goodsReceiptRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error",
			"sellable_quantity, damaged_quantity, missing_quantity and expected_version are required")
		return
	}
	actor := usecase.SupportActor{ID: middleware.GetUserID(c), Role: "vendor"}
	if middleware.GetRole(c) == "admin" {
		actor.Role = "admin"
	}
	item, err := h.orders.RecordReturnReceipt(c.Request.Context(), actor, c.Param("id"), usecase.ReturnReceiptInput{Sellable: *req.Sellable,
		Damaged: *req.Damaged, Missing: *req.Missing, Note: req.Note, ExpectedVersion: req.ExpectedVersion})
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toReturnResponse(item))
}

type shippingAuthorizationRequest struct {
	FeePayer        string `json:"fee_payer" binding:"omitempty,oneof=buyer seller"`
	FeeCap          *int64 `json:"fee_cap" binding:"omitempty,min=0"`
	Reason          string `json:"reason" binding:"required,max=500"`
	ExpectedVersion int64  `json:"expected_version" binding:"required,min=1"`
}

// AuthorizeShipping: an admin gives an approved return its instructions
// (or points a parcel not sent yet at the newly verified destination).
func (h *ReturnHandler) AuthorizeShipping(c *gin.Context) {
	if !validID(c, c.Param("id")) {
		return
	}
	var req shippingAuthorizationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "reason and expected_version are required; fee_payer is buyer or seller")
		return
	}
	item, err := h.orders.AuthorizeReturnShipping(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), usecase.AuthorizeReturnShippingInput{
		Terms: usecase.ReturnShippingTerms{FeePayer: req.FeePayer, FeeCap: req.FeeCap}, Reason: req.Reason, ExpectedVersion: req.ExpectedVersion})
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toReturnResponse(item))
}

type shippingDecisionRequest struct {
	Action          string `json:"action" binding:"required,oneof=refund mark_lost"`
	Reason          string `json:"reason" binding:"required,max=500"`
	ExpectedVersion int64  `json:"expected_version" binding:"required,min=1"`
}

// ShippingDecision: refund a disputed inspection in full, or mark a parcel
// lost on the way back.
func (h *ReturnHandler) ShippingDecision(c *gin.Context) {
	if !validID(c, c.Param("id")) {
		return
	}
	var req shippingDecisionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "action (refund|mark_lost), reason and expected_version are required")
		return
	}
	item, err := h.orders.DecideReturnShipping(c.Request.Context(), middleware.GetUserID(c), c.Param("id"),
		usecase.ReturnShippingDecision{Action: req.Action, Reason: req.Reason, ExpectedVersion: req.ExpectedVersion})
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toReturnResponse(item))
}

type receiptResponseBody struct {
	Version   int       `json:"version"`
	ActorRole string    `json:"actor_role"`
	Sellable  int64     `json:"sellable_quantity"`
	Damaged   int64     `json:"damaged_quantity"`
	Missing   int64     `json:"missing_quantity"`
	Note      *string   `json:"note,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

func toReturnReceipt(g *domain.ReturnReceipt) *receiptResponseBody {
	if g == nil {
		return nil
	}
	return &receiptResponseBody{Version: g.Version, ActorRole: g.ActorRole, Sellable: g.Sellable, Damaged: g.Damaged, Missing: g.Missing,
		Note: g.Note, CreatedAt: g.CreatedAt}
}
