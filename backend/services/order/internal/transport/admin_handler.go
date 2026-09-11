package transport

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/usecase"
)

// AdminHandler serves admin's operational view of orders: listing across
// every buyer, and intervening (cancel/refund) outside the normal
// vendor-driven fulfillment path.
type AdminHandler struct {
	orders *usecase.OrderUseCase
	log    zerolog.Logger
}

func NewAdminHandler(orders *usecase.OrderUseCase, log zerolog.Logger) *AdminHandler {
	return &AdminHandler{orders: orders, log: log}
}

func (h *AdminHandler) List(c *gin.Context) {
	limit, offset := paginationParams(c)

	orders, err := h.orders.AdminList(c.Request.Context(), c.Query("status"), limit, offset)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toOrderResponseList(orders))
}

func (h *AdminHandler) Transition(c *gin.Context) {
	var req adminTransitionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	order, err := h.orders.AdminTransition(c.Request.Context(), c.Param("id"), domain.Status(req.Status), req.Reason)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toOrderResponse(order))
}

func (h *AdminHandler) SetCommissionRule(c *gin.Context) {
	var req setCommissionRuleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	rule, err := h.orders.SetCommissionRule(c.Request.Context(), middleware.GetUserID(c), req.RateBps)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusCreated, toCommissionRuleResponse(rule))
}

func (h *AdminHandler) ListCommissionRules(c *gin.Context) {
	limit, offset := paginationParams(c)

	rules, err := h.orders.ListCommissionRules(c.Request.Context(), limit, offset)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toCommissionRuleResponseList(rules))
}
