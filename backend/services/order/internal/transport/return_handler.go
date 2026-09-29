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

type ReturnHandler struct {
	returns *usecase.ReturnUseCase
	log     zerolog.Logger
}

func NewReturnHandler(returns *usecase.ReturnUseCase, log zerolog.Logger) *ReturnHandler {
	return &ReturnHandler{returns: returns, log: log}
}

type createReturnRequest struct {
	OrderItemID string `json:"order_item_id" binding:"required"`
	Reason      string `json:"reason" binding:"required"`
}
type decideReturnRequest struct {
	Approve bool   `json:"approve"`
	Note    string `json:"note"`
}
type vendorConfirmReturnRequest struct {
	VendorID string `json:"vendor_id" binding:"required"`
}
type returnResponse struct {
	ID           string     `json:"id"`
	OrderID      string     `json:"order_id"`
	OrderItemID  string     `json:"order_item_id"`
	Reason       string     `json:"reason"`
	Status       string     `json:"status"`
	DecisionNote *string    `json:"decision_note,omitempty"`
	DecidedAt    *time.Time `json:"decided_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

func toReturnResponse(r *domain.ReturnRequest) returnResponse {
	return returnResponse{ID: r.ID, OrderID: r.OrderID, OrderItemID: r.OrderItemID, Reason: r.Reason, Status: string(r.Status), DecisionNote: r.DecisionNote, DecidedAt: r.DecidedAt, CreatedAt: r.CreatedAt}
}
func toReturnResponses(items []*domain.ReturnRequest) []returnResponse {
	out := make([]returnResponse, 0, len(items))
	for _, item := range items {
		out = append(out, toReturnResponse(item))
	}
	return out
}
func (h *ReturnHandler) Create(c *gin.Context) {
	var req createReturnRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	r, err := h.returns.Create(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), req.OrderItemID, req.Reason)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusCreated, toReturnResponse(r))
}
func (h *ReturnHandler) ListMine(c *gin.Context) {
	limit, offset := paginationParams(c)
	items, err := h.returns.ListMine(c.Request.Context(), middleware.GetUserID(c), limit, offset)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toReturnResponses(items))
}
func (h *ReturnHandler) AdminList(c *gin.Context) {
	limit, offset := paginationParams(c)
	items, err := h.returns.AdminList(c.Request.Context(), c.Query("status"), limit, offset)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toReturnResponses(items))
}
func (h *ReturnHandler) ConfirmByVendor(c *gin.Context) {
	var req vendorConfirmReturnRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	item, err := h.returns.ConfirmByVendor(c.Request.Context(), c.Param("id"), req.VendorID, middleware.GetUserID(c))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toReturnResponse(item))
}
func (h *ReturnHandler) Decide(c *gin.Context) {
	var req decideReturnRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	item, err := h.returns.Decide(c.Request.Context(), c.Param("id"), middleware.GetUserID(c), req.Approve, req.Note)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toReturnResponse(item))
}
