package transport

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/order/internal/usecase"
)

// ReturnHandler serves the return flow for buyers, the selling vendor and
// admins. Every step is authorized and audited in the use case.
type ReturnHandler struct {
	orders *usecase.OrderUseCase
	log    zerolog.Logger
}

func NewReturnHandler(orders *usecase.OrderUseCase, log zerolog.Logger) *ReturnHandler {
	return &ReturnHandler{orders: orders, log: log}
}

type createReturnRequest struct {
	OrderItemID string `json:"order_item_id" binding:"required,uuid"`
	Quantity    int64  `json:"quantity" binding:"omitempty,min=1"`
	Reason      string `json:"reason" binding:"required,max=2000"`
	Evidence    string `json:"evidence" binding:"max=2000"`
}

type decideReturnRequest struct {
	Approve bool   `json:"approve"`
	Note    string `json:"note" binding:"max=1000"`
}

type noteRequest struct {
	Note string `json:"note" binding:"max=1000"`
}

type receiveReturnRequest struct {
	Restock bool   `json:"restock"`
	Note    string `json:"note" binding:"max=1000"`
}

func (h *ReturnHandler) Create(c *gin.Context) {
	if !validID(c, c.Param("id")) {
		return
	}
	var req createReturnRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "order_item_id and a reason are required; quantity must be positive")
		return
	}
	r, err := h.orders.CreateReturn(c.Request.Context(), middleware.GetUserID(c), usecase.ReturnInput{
		OrderID: c.Param("id"), ItemID: req.OrderItemID, Quantity: req.Quantity, Reason: req.Reason, Evidence: req.Evidence,
	})
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusCreated, toReturnResponse(r))
}

func (h *ReturnHandler) ListMine(c *gin.Context) {
	limit, offset := paginationParams(c)
	items, err := h.orders.ListMyReturns(c.Request.Context(), middleware.GetUserID(c), limit, offset)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toReturnResponses(items))
}

func (h *ReturnHandler) VendorList(c *gin.Context) {
	limit, offset := paginationParams(c)
	items, err := h.orders.ListVendorReturns(c.Request.Context(), middleware.GetUserID(c), c.Query("vendor_id"), c.Query("status"), limit, offset)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toReturnResponses(items))
}

func (h *ReturnHandler) ConfirmByVendor(c *gin.Context) {
	if !validID(c, c.Param("id")) {
		return
	}
	var req noteRequest
	_ = c.ShouldBindJSON(&req)
	item, err := h.orders.VendorConfirmReturn(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), req.Note)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toReturnResponse(item))
}

// Receive records goods received and inspected; role comes from the token.
func (h *ReturnHandler) Receive(c *gin.Context) {
	if !validID(c, c.Param("id")) {
		return
	}
	var req receiveReturnRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "note must be at most 1000 characters")
		return
	}
	item, err := h.orders.MarkReturnReceived(c.Request.Context(), middleware.GetUserID(c), middleware.GetRole(c), c.Param("id"), req.Restock, req.Note)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toReturnResponse(item))
}

func (h *ReturnHandler) AdminList(c *gin.Context) {
	limit, offset := paginationParams(c)
	items, err := h.orders.ListReturns(c.Request.Context(), c.Query("status"), limit, offset)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toReturnResponses(items))
}

func (h *ReturnHandler) Decide(c *gin.Context) {
	if !validID(c, c.Param("id")) {
		return
	}
	var req decideReturnRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "note must be at most 1000 characters")
		return
	}
	item, err := h.orders.AdminDecideReturn(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), req.Approve, req.Note)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toReturnResponse(item))
}

func (h *ReturnHandler) RetryRefund(c *gin.Context) {
	if !validID(c, c.Param("id")) {
		return
	}
	item, err := h.orders.RetryReturnRefund(c.Request.Context(), middleware.GetUserID(c), c.Param("id"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toReturnResponse(item))
}

func (h *ReturnHandler) History(c *gin.Context) {
	if !validID(c, c.Param("id")) {
		return
	}
	events, err := h.orders.ReturnHistory(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toReturnEventResponses(events))
}
