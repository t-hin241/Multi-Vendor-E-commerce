package transport

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/order/internal/usecase"
)

// AdminHandler serves admin's operational view of orders: listing and
// detail, cancelling unpaid orders, requesting refunds through Payment,
// payment exceptions, parked side effects and commission rules. Sensitive
// actions re-verify the admin with Identity in the use case.
type AdminHandler struct {
	orders *usecase.OrderUseCase
	log    zerolog.Logger
}

func NewAdminHandler(orders *usecase.OrderUseCase, log zerolog.Logger) *AdminHandler {
	return &AdminHandler{orders: orders, log: log}
}

// List: ?status=&buyer_id=&from=&to= (RFC3339) &limit=&offset=; the total
// count is in X-Total-Count.
func (h *AdminHandler) List(c *gin.Context) {
	limit, offset := paginationParams(c)
	f := usecase.ListFilter{Status: c.Query("status"), BuyerID: c.Query("buyer_id")}
	if f.BuyerID != "" && !validID(c, f.BuyerID) {
		return
	}
	for _, p := range []struct {
		name string
		dst  **time.Time
	}{{"from", &f.From}, {"to", &f.To}} {
		if raw := c.Query(p.name); raw != "" {
			t, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				httpresponse.Error(c, http.StatusBadRequest, "validation_error", p.name+" must be an RFC3339 time")
				return
			}
			*p.dst = &t
		}
	}
	orders, total, err := h.orders.AdminList(c.Request.Context(), f, limit, offset)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	c.Header("X-Total-Count", strconv.FormatInt(total, 10))
	httpresponse.OK(c, http.StatusOK, toOrderResponseList(orders))
}

func (h *AdminHandler) Get(c *gin.Context) {
	if !validID(c, c.Param("id")) {
		return
	}
	detail, err := h.orders.AdminGetOrder(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toOrderDetailResponse(detail, true))
}

// Transition keeps the old route for cancellation only.
func (h *AdminHandler) Transition(c *gin.Context) {
	if !validID(c, c.Param("id")) {
		return
	}
	var req adminTransitionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "status and a reason of at most 500 characters are required")
		return
	}
	if req.Status != "cancelled" {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error",
			"Admin can only cancel an unpaid order here; refund a paid order with POST /api/orders/admin/:id/refunds")
		return
	}
	order, err := h.orders.AdminCancel(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), req.Reason)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toOrderResponse(order))
}

// CreateRefund requests a refund through Payment (dispute on a paid vendor
// order, or a rejected late/duplicate capture).
func (h *AdminHandler) CreateRefund(c *gin.Context) {
	if !validID(c, c.Param("id")) {
		return
	}
	var req createRefundRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error",
			"reason_code (dispute, late_payment, duplicate_payment), a positive amount and a reason are required")
		return
	}
	refund, err := h.orders.AdminRequestRefund(c.Request.Context(), middleware.GetUserID(c), usecase.RefundInput{
		OrderID: c.Param("id"), VendorOrderID: req.VendorOrderID, PaymentID: req.PaymentID, ReasonCode: req.ReasonCode,
		Amount: req.Amount, Reason: req.Reason,
	})
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusCreated, toRefundResponse(refund))
}

func (h *AdminHandler) ListRefunds(c *gin.Context) {
	limit, offset := paginationParams(c)
	refunds, err := h.orders.ListRefunds(c.Request.Context(), c.Query("status"), limit, offset)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	out := make([]refundResponse, 0, len(refunds))
	for _, f := range refunds {
		out = append(out, toRefundResponse(f))
	}
	httpresponse.OK(c, http.StatusOK, out)
}

// PaymentExceptions lists captures Order rejected with no refund yet.
func (h *AdminHandler) PaymentExceptions(c *gin.Context) {
	limit, offset := paginationParams(c)
	payments, err := h.orders.ListPaymentExceptions(c.Request.Context(), limit, offset)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	out := make([]orderPaymentResponse, 0, len(payments))
	for _, p := range payments {
		out = append(out, toOrderPaymentResponse(p))
	}
	httpresponse.OK(c, http.StatusOK, out)
}

// Operations reports the side-effect backlog and the parked effects.
func (h *AdminHandler) Operations(c *gin.Context) {
	limit, offset := paginationParams(c)
	stats, err := h.orders.EffectBacklog(c.Request.Context())
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	parked, err := h.orders.ListParkedEffects(c.Request.Context(), limit, offset)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	out := make([]effectResponse, 0, len(parked))
	for _, e := range parked {
		out = append(out, toEffectResponse(e))
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"pending": stats.Pending, "parked": stats.Parked, "oldest_pending": stats.OldestPending, "parked_effects": out})
}

func (h *AdminHandler) ReplayEffect(c *gin.Context) {
	if !validID(c, c.Param("effectID")) {
		return
	}
	if err := h.orders.ReplayEffect(c.Request.Context(), middleware.GetUserID(c), c.Param("effectID")); err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"replayed": true})
}

func (h *AdminHandler) SetCommissionRule(c *gin.Context) {
	var req setCommissionRuleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "rate_bps must be between 0 and 10000")
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
