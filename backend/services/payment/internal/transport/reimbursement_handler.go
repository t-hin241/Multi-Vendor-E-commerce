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

// ReimbursementHandler serves the PW-032 admin book.
type ReimbursementHandler struct {
	uc  *usecase.ReimbursementUseCase
	log zerolog.Logger
}

func NewReimbursementHandler(uc *usecase.ReimbursementUseCase, log zerolog.Logger) *ReimbursementHandler {
	return &ReimbursementHandler{uc: uc, log: log}
}

type reimbursementResponse struct {
	ID                string     `json:"id"`
	OrderID           string     `json:"order_id"`
	BuyerID           string     `json:"buyer_id"`
	ReasonCode        string     `json:"reason_code"`
	Reason            string     `json:"reason"`
	Amount            int64      `json:"amount"`
	Currency          string     `json:"currency"`
	Status            string     `json:"status"`
	RequestedBy       string     `json:"requested_by"`
	DecidedBy         *string    `json:"decided_by,omitempty"`
	DecidedAt         *time.Time `json:"decided_at,omitempty"`
	DecisionReason    *string    `json:"decision_reason,omitempty"`
	DestinationMasked *string    `json:"destination_masked,omitempty"`
	BankReference     *string    `json:"bank_reference,omitempty"`
	PaidBy            *string    `json:"paid_by,omitempty"`
	PaidAt            *time.Time `json:"paid_at,omitempty"`
	Version           int        `json:"version"`
	CreatedAt         time.Time  `json:"created_at"`
}

func toReimbursement(r *domain.Reimbursement) reimbursementResponse {
	return reimbursementResponse{ID: r.ID, OrderID: r.OrderID, BuyerID: r.BuyerID, ReasonCode: r.ReasonCode, Reason: r.Reason, Amount: r.Amount,
		Currency: r.Currency, Status: string(r.Status), RequestedBy: r.RequestedBy, DecidedBy: r.DecidedBy, DecidedAt: r.DecidedAt,
		DecisionReason: r.DecisionReason, DestinationMasked: r.DestinationMasked, BankReference: r.BankReference, PaidBy: r.PaidBy,
		PaidAt: r.PaidAt, Version: r.Version, CreatedAt: r.CreatedAt}
}

func (h *ReimbursementHandler) List(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if limit < 1 || limit > 100 || offset < 0 {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "limit must be 1-100 and offset non-negative")
		return
	}
	items, err := h.uc.List(c.Request.Context(), c.Query("status"), limit, offset)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	out := make([]reimbursementResponse, 0, len(items))
	for _, r := range items {
		out = append(out, toReimbursement(r))
	}
	httpresponse.OK(c, http.StatusOK, out)
}

func (h *ReimbursementHandler) Create(c *gin.Context) {
	var req struct {
		OrderID    string `json:"order_id" binding:"required,uuid"`
		ReasonCode string `json:"reason_code" binding:"required,max=40"`
		Reason     string `json:"reason" binding:"required,max=500"`
		Amount     int64  `json:"amount" binding:"required,min=1"`
		Currency   string `json:"currency" binding:"required,len=3"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "order_id, reason_code, reason, amount and currency are required")
		return
	}
	r, created, err := h.uc.Request(c.Request.Context(), middleware.GetUserID(c), domain.ReimbursementInput{OrderID: req.OrderID,
		ReasonCode: req.ReasonCode, Reason: req.Reason, Amount: req.Amount, Currency: req.Currency, IdempotencyKey: c.GetHeader("Idempotency-Key")})
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	httpresponse.OK(c, status, toReimbursement(r))
}

func (h *ReimbursementHandler) id(c *gin.Context) (string, bool) {
	if _, err := uuid.Parse(c.Param("id")); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "Invalid reimbursement id")
		return "", false
	}
	return c.Param("id"), true
}

func (h *ReimbursementHandler) Decide(c *gin.Context) {
	id, ok := h.id(c)
	if !ok {
		return
	}
	var req struct {
		Decision        string `json:"decision" binding:"required,oneof=approve reject"`
		Reason          string `json:"reason" binding:"required,max=500"`
		ExpectedVersion int    `json:"expected_version" binding:"required,min=1"`
		Proof           string `json:"proof" binding:"max=200"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "decision (approve|reject), reason and expected_version are required")
		return
	}
	r, err := h.uc.Decide(c.Request.Context(), middleware.GetUserID(c), id, usecase.ReimbursementDecision{Approve: req.Decision == "approve",
		Reason: req.Reason, ExpectedVersion: req.ExpectedVersion, Proof: req.Proof})
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toReimbursement(r))
}

func (h *ReimbursementHandler) Pay(c *gin.Context) {
	id, ok := h.id(c)
	if !ok {
		return
	}
	var req struct {
		BankReference   string `json:"bank_reference" binding:"required,max=100"`
		ExpectedVersion int    `json:"expected_version" binding:"required,min=1"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "bank_reference and expected_version are required")
		return
	}
	r, err := h.uc.RecordPayment(c.Request.Context(), middleware.GetUserID(c), id, usecase.ReimbursementPayment{BankReference: req.BankReference,
		ExpectedVersion: req.ExpectedVersion})
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toReimbursement(r))
}
