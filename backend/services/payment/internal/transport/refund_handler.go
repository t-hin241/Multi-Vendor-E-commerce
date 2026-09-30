package transport

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/payment/internal/domain"
	"shopee/backend/services/payment/internal/usecase"
)

// RefundHandler serves Order's internal refund intake and the admin queue
// where operators record what happened to the money.
type RefundHandler struct {
	refunds *usecase.RefundUseCase
	log     zerolog.Logger
}

func NewRefundHandler(refunds *usecase.RefundUseCase, log zerolog.Logger) *RefundHandler {
	return &RefundHandler{refunds: refunds, log: log}
}

type refundIntakeRequest struct {
	OrderRefundID string  `json:"order_refund_id" binding:"required,uuid"`
	OrderID       string  `json:"order_id" binding:"required,uuid"`
	PaymentID     *string `json:"payment_id" binding:"omitempty,uuid"`
	VendorOrderID *string `json:"vendor_order_id" binding:"omitempty,uuid"`
	Amount        int64   `json:"amount" binding:"required,min=1"`
	Currency      string  `json:"currency" binding:"required,len=3"`
	Reason        string  `json:"reason" binding:"required,max=500"`
	RequestedBy   string  `json:"requested_by" binding:"required,uuid"`
}

type resolveRefundRequest struct {
	Outcome           string `json:"outcome" binding:"required,oneof=succeeded failed"`
	EvidenceReference string `json:"evidence_reference" binding:"max=200"`
	Note              string `json:"note" binding:"max=500"`
}

type refundResponse struct {
	ID                string     `json:"id"`
	PaymentIntentID   string     `json:"payment_intent_id"`
	OrderID           string     `json:"order_id"`
	OrderRefundID     *string    `json:"order_refund_id,omitempty"`
	VendorOrderID     *string    `json:"vendor_order_id,omitempty"`
	Amount            int64      `json:"amount"`
	Currency          string     `json:"currency"`
	Reason            string     `json:"reason"`
	Status            string     `json:"status"`
	RequestedBy       string     `json:"requested_by"`
	EvidenceReference *string    `json:"evidence_reference,omitempty"`
	Note              *string    `json:"note,omitempty"`
	FailureReason     *string    `json:"failure_reason,omitempty"`
	ResolvedBy        *string    `json:"resolved_by,omitempty"`
	ResolvedAt        *time.Time `json:"resolved_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

func toRefundResponse(r *domain.Refund) refundResponse {
	return refundResponse{
		ID: r.ID, PaymentIntentID: r.PaymentIntentID, OrderID: r.OrderID, OrderRefundID: r.OrderRefundID, VendorOrderID: r.VendorOrderID,
		Amount: r.Amount, Currency: r.Currency, Reason: r.Reason, Status: string(r.Status), RequestedBy: r.RequestedBy,
		EvidenceReference: r.EvidenceReference, Note: r.Note, FailureReason: r.FailureReason, ResolvedBy: r.ResolvedBy,
		ResolvedAt: r.ResolvedAt, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

// Request is Order's refund intake: 201 when accepted, 200 on a replay.
func (h *RefundHandler) Request(c *gin.Context) {
	var req refundIntakeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "order_refund_id, order_id, amount, currency, reason and requested_by are required")
		return
	}
	refund, created, err := h.refunds.Request(c.Request.Context(), domain.RefundRequest{
		OrderRefundID: req.OrderRefundID, OrderID: req.OrderID, PaymentID: req.PaymentID, VendorOrderID: req.VendorOrderID, Amount: req.Amount,
		Currency: req.Currency, Reason: req.Reason, RequestedBy: req.RequestedBy,
	})
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	httpresponse.OK(c, status, gin.H{"payment_refund_id": refund.ID, "status": refund.Status})
}

func (h *RefundHandler) AdminList(c *gin.Context) {
	limit := boundedInt(c.Query("limit"), 20, 1, 100)
	offset := boundedInt(c.Query("offset"), 0, 0, 10_000)
	refunds, total, err := h.refunds.List(c.Request.Context(), middleware.GetUserID(c), c.Query("status"), limit, offset)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	out := make([]refundResponse, 0, len(refunds))
	for _, r := range refunds {
		out = append(out, toRefundResponse(r))
	}
	c.Header("X-Total-Count", strconv.Itoa(total))
	httpresponse.OK(c, http.StatusOK, out)
}

func (h *RefundHandler) AdminResolve(c *gin.Context) {
	if _, err := uuid.Parse(c.Param("id")); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "Invalid refund id")
		return
	}
	var req resolveRefundRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "outcome must be succeeded or failed")
		return
	}
	refund, err := h.refunds.Resolve(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), domain.RefundResolution{
		Outcome: domain.RefundStatus(req.Outcome), EvidenceReference: req.EvidenceReference, Note: req.Note,
	})
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toRefundResponse(refund))
}

func boundedInt(raw string, fallback, min, max int) int {
	v, err := strconv.Atoi(raw)
	if raw == "" || err != nil || v < min || v > max {
		return fallback
	}
	return v
}
