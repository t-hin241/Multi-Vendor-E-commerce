package transport

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/usecase"
)

// Support intakes (PW-012): a buyer without an order id sends the
// reference they have; an admin links it to the buyer's order.

type supportIntakeResponse struct {
	ID            string     `json:"id"`
	BuyerID       string     `json:"buyer_id,omitempty"`
	ReferenceKind string     `json:"reference_kind"`
	Reference     string     `json:"reference"`
	Message       string     `json:"message"`
	Status        string     `json:"status"`
	LinkedCaseID  *string    `json:"linked_case_id,omitempty"`
	HandledAt     *time.Time `json:"handled_at,omitempty"`
	CloseReason   *string    `json:"close_reason,omitempty"`
	Version       int64      `json:"version"`
	CreatedAt     time.Time  `json:"created_at"`
}

func toIntakeResponse(in *domain.SupportIntake, admin bool) supportIntakeResponse {
	out := supportIntakeResponse{ID: in.ID, ReferenceKind: in.ReferenceKind, Reference: in.Reference, Message: in.Message, Status: in.Status,
		LinkedCaseID: in.LinkedCaseID, HandledAt: in.HandledAt, CloseReason: in.CloseReason, Version: in.Version, CreatedAt: in.CreatedAt}
	if admin {
		out.BuyerID = in.BuyerID
	}
	return out
}

func toIntakeResponses(items []*domain.SupportIntake, admin bool) []supportIntakeResponse {
	out := make([]supportIntakeResponse, 0, len(items))
	for _, in := range items {
		out = append(out, toIntakeResponse(in, admin))
	}
	return out
}

type createIntakeRequest struct {
	ReferenceKind string `json:"reference_kind" binding:"required"`
	Reference     string `json:"reference" binding:"required,max=100"`
	Message       string `json:"message" binding:"required"`
}

// CreateIntake: 201, or 200 for an Idempotency-Key replay.
func (h *SupportHandler) CreateIntake(c *gin.Context) {
	var req createIntakeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "reference_kind, reference and message are required")
		return
	}
	intake, replayed, err := h.orders.CreateSupportIntake(c.Request.Context(), middleware.GetUserID(c), usecase.SupportIntakeInput{
		ReferenceKind: req.ReferenceKind, Reference: req.Reference, Message: req.Message, IdempotencyKey: c.GetHeader("Idempotency-Key")})
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
	}
	httpresponse.OK(c, status, toIntakeResponse(intake, false))
}

func (h *SupportHandler) MyIntakes(c *gin.Context) {
	items, err := h.orders.ListMySupportIntakes(c.Request.Context(), middleware.GetUserID(c))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	httpresponse.OK(c, http.StatusOK, toIntakeResponses(items, false))
}

func (h *SupportHandler) AdminIntakes(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if limit < 1 || limit > 100 {
		limit = 50
	}
	if offset < 0 || offset > 10_000 {
		offset = 0
	}
	items, err := h.orders.ListSupportIntakes(c.Request.Context(), c.DefaultQuery("status", domain.IntakeOpen), limit, offset)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	httpresponse.OK(c, http.StatusOK, toIntakeResponses(items, true))
}

type linkIntakeRequest struct {
	OrderID         string `json:"order_id" binding:"required,uuid"`
	VendorOrderID   string `json:"vendor_order_id" binding:"required,uuid"`
	Category        string `json:"category" binding:"required"`
	ExpectedVersion int64  `json:"expected_version" binding:"required,min=1"`
	Reason          string `json:"reason" binding:"required,max=500"`
}

// LinkIntake opens (or adds to) the support case on the verified order.
func (h *SupportHandler) LinkIntake(c *gin.Context) {
	if !validID(c, c.Param("intakeID")) {
		return
	}
	var req linkIntakeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "order_id, vendor_order_id, category, expected_version and reason are required")
		return
	}
	sc, err := h.orders.LinkSupportIntake(c.Request.Context(), middleware.GetUserID(c), c.Param("intakeID"), usecase.LinkIntakeInput{
		OrderID: req.OrderID, VendorOrderID: req.VendorOrderID, Category: req.Category, ExpectedVersion: req.ExpectedVersion, Reason: req.Reason})
	h.respondCase(c, sc, err)
}

type closeIntakeRequest struct {
	ExpectedVersion int64  `json:"expected_version" binding:"required,min=1"`
	Reason          string `json:"reason" binding:"required,max=500"`
}

func (h *SupportHandler) CloseIntake(c *gin.Context) {
	if !validID(c, c.Param("intakeID")) {
		return
	}
	var req closeIntakeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "expected_version and a reason of at most 500 characters are required")
		return
	}
	intake, err := h.orders.CloseSupportIntake(c.Request.Context(), middleware.GetUserID(c), c.Param("intakeID"), req.ExpectedVersion, req.Reason)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toIntakeResponse(intake, true))
}
