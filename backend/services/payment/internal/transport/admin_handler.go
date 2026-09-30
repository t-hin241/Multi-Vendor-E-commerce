package transport

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/payment/internal/domain"
	"shopee/backend/services/payment/internal/usecase"
)

// AdminHandler serves reconciliation and settlement for admins. Every use
// case re-verifies the admin role with Identity; retries and money
// movements are audited with their reason.
type AdminHandler struct {
	recon      *usecase.ReconciliationUseCase
	settlement *usecase.SettlementUseCase
	log        zerolog.Logger
}

func NewAdminHandler(recon *usecase.ReconciliationUseCase, settlement *usecase.SettlementUseCase, log zerolog.Logger) *AdminHandler {
	return &AdminHandler{recon: recon, settlement: settlement, log: log}
}

type reasonRequest struct {
	Reason string `json:"reason" binding:"required,max=500"`
}

type adminIntentResponse struct {
	paymentIntentResponse
	BuyerID           string  `json:"buyer_id"`
	ProviderReference string  `json:"provider_reference,omitempty"`
	CreateAttempts    int     `json:"create_attempts"`
	LastError         *string `json:"last_error,omitempty"`
	ClosedReason      *string `json:"closed_reason,omitempty"`
}

func toAdminIntent(i *domain.PaymentIntent) adminIntentResponse {
	return adminIntentResponse{paymentIntentResponse: toPaymentIntentResponse(i), BuyerID: i.BuyerID, ProviderReference: i.ProviderReference,
		CreateAttempts: i.CreateAttempts, LastError: i.LastError, ClosedReason: i.ClosedReason}
}

type receiptResponse struct {
	ID                string     `json:"id"`
	Provider          string     `json:"provider"`
	ProviderEventID   string     `json:"provider_event_id"`
	ProviderIntentID  string     `json:"provider_intent_id,omitempty"`
	ProviderReference string     `json:"provider_reference,omitempty"`
	PaymentIntentID   *string    `json:"payment_intent_id,omitempty"`
	EventType         string     `json:"event_type"`
	Amount            int64      `json:"amount"`
	Currency          string     `json:"currency"`
	Status            string     `json:"status"`
	Outcome           *string    `json:"outcome,omitempty"`
	Attempts          int        `json:"attempts"`
	LastError         *string    `json:"last_error,omitempty"`
	ReceivedAt        time.Time  `json:"received_at"`
	ProcessedAt       *time.Time `json:"processed_at,omitempty"`
}

func toReceipt(r *domain.Receipt) receiptResponse {
	return receiptResponse{ID: r.ID, Provider: r.Provider, ProviderEventID: r.ProviderEventID, ProviderIntentID: r.ProviderIntentID,
		ProviderReference: r.ProviderReference, PaymentIntentID: r.PaymentIntentID, EventType: string(r.EventType), Amount: r.Amount,
		Currency: r.Currency, Status: string(r.Status), Outcome: r.Outcome, Attempts: r.Attempts, LastError: r.LastError,
		ReceivedAt: r.ReceivedAt, ProcessedAt: r.ProcessedAt}
}

func toReceipts(list []*domain.Receipt) []receiptResponse {
	out := make([]receiptResponse, 0, len(list))
	for _, r := range list {
		out = append(out, toReceipt(r))
	}
	return out
}

func validUUID(c *gin.Context, id string) bool {
	if _, err := uuid.Parse(id); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "Invalid id")
		return false
	}
	return true
}

func (h *AdminHandler) Overview(c *gin.Context) {
	o, err := h.recon.Overview(c.Request.Context(), middleware.GetUserID(c))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, gin.H{
		"counts": o.Counts, "parked_receipts": toReceipts(o.ParkedReceipts), "rejected_receipts": toReceipts(o.RejectedReceipts),
		"retryable_receipts": toReceipts(o.RetryableReceipts), "order_sync": o.OrderSync, "refund_sync": o.RefundSync, "generated_at": o.GeneratedAt,
	})
}

func (h *AdminHandler) Search(c *gin.Context) {
	res, err := h.recon.Search(c.Request.Context(), middleware.GetUserID(c), c.Query("q"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	intents := make([]adminIntentResponse, 0, len(res.Intents))
	for _, i := range res.Intents {
		intents = append(intents, toAdminIntent(i))
	}
	refunds := make([]refundResponse, 0, len(res.Refunds))
	for _, r := range res.Refunds {
		refunds = append(refunds, toRefundResponse(r))
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"intents": intents, "receipts": toReceipts(res.Receipts), "refunds": refunds})
}

func (h *AdminHandler) bindReason(c *gin.Context) (string, bool) {
	var req reasonRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Reason) == "" {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "A reason is required")
		return "", false
	}
	return req.Reason, true
}

func (h *AdminHandler) RetryReceipt(c *gin.Context) {
	if !validUUID(c, c.Param("id")) {
		return
	}
	reason, ok := h.bindReason(c)
	if !ok {
		return
	}
	r, err := h.recon.RetryReceipt(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), reason)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toReceipt(r))
}

func (h *AdminHandler) RetryOrderSync(c *gin.Context) {
	if !validUUID(c, c.Param("id")) {
		return
	}
	reason, ok := h.bindReason(c)
	if !ok {
		return
	}
	if err := h.recon.RetryOrderSync(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), reason); err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"requeued": true})
}

func (h *AdminHandler) RetryRefundSync(c *gin.Context) {
	if !validUUID(c, c.Param("id")) {
		return
	}
	reason, ok := h.bindReason(c)
	if !ok {
		return
	}
	if err := h.recon.RetryRefundSync(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), reason); err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"requeued": true})
}

func (h *AdminHandler) ReconcileIntent(c *gin.Context) {
	if !validUUID(c, c.Param("id")) {
		return
	}
	reason, ok := h.bindReason(c)
	if !ok {
		return
	}
	i, err := h.recon.ReconcileIntent(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), reason)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toAdminIntent(i))
}

func (h *AdminHandler) History(c *gin.Context) {
	targetType := c.Query("target_type")
	switch targetType {
	case "receipt", "payment_intent", "payment_refund", "payout_batch", "payout_item", "vendor":
	default:
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "Unknown target_type")
		return
	}
	out, err := h.recon.History(c.Request.Context(), middleware.GetUserID(c), targetType, c.Query("target_id"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, out)
}

// ----- settlement -----

type entryResponse struct {
	ID            string    `json:"id"`
	VendorID      string    `json:"vendor_id"`
	VendorOrderID *string   `json:"vendor_order_id,omitempty"`
	Type          string    `json:"entry_type"`
	Amount        int64     `json:"amount"`
	Currency      string    `json:"currency"`
	EligibleAt    time.Time `json:"eligible_at"`
	Note          *string   `json:"note,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

func toEntry(e *domain.Entry) entryResponse {
	return entryResponse{ID: e.ID, VendorID: e.VendorID, VendorOrderID: e.VendorOrderID, Type: string(e.Type), Amount: e.Amount,
		Currency: e.Currency, EligibleAt: e.EligibleAt, Note: e.Note, CreatedAt: e.CreatedAt}
}

type payoutItemResponse struct {
	ID                string     `json:"id"`
	BatchID           string     `json:"batch_id"`
	VendorID          string     `json:"vendor_id"`
	Amount            int64      `json:"amount"`
	Currency          string     `json:"currency"`
	DestinationID     string     `json:"destination_account_id"`
	DestinationVer    int64      `json:"destination_version"`
	DestinationMask   string     `json:"destination_mask"`
	Status            string     `json:"status"`
	EvidenceReference *string    `json:"evidence_reference,omitempty"`
	Note              *string    `json:"note,omitempty"`
	FailureReason     *string    `json:"failure_reason,omitempty"`
	ResolvedBy        *string    `json:"resolved_by,omitempty"`
	ResolvedAt        *time.Time `json:"resolved_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
}

func toPayoutItem(i *domain.PayoutItem) payoutItemResponse {
	return payoutItemResponse{ID: i.ID, BatchID: i.BatchID, VendorID: i.VendorID, Amount: i.Amount, Currency: i.Currency,
		DestinationID: i.DestinationAccountID, DestinationVer: i.DestinationVersion, DestinationMask: i.DestinationMask, Status: string(i.Status),
		EvidenceReference: i.EvidenceReference, Note: i.Note, FailureReason: i.FailureReason, ResolvedBy: i.ResolvedBy, ResolvedAt: i.ResolvedAt, CreatedAt: i.CreatedAt}
}

type batchResponse struct {
	ID             string               `json:"id"`
	IdempotencyKey string               `json:"idempotency_key"`
	Currency       string               `json:"currency"`
	Status         string               `json:"status"`
	CreatedBy      string               `json:"created_by"`
	CreatedAt      time.Time            `json:"created_at"`
	Items          []payoutItemResponse `json:"items,omitempty"`
}

func toBatch(b *domain.PayoutBatch) batchResponse {
	out := batchResponse{ID: b.ID, IdempotencyKey: b.IdempotencyKey, Currency: b.Currency, Status: b.Status, CreatedBy: b.CreatedBy, CreatedAt: b.CreatedAt}
	for _, i := range b.Items {
		out.Items = append(out.Items, toPayoutItem(i))
	}
	return out
}

func currencyParam(c *gin.Context) string {
	if v := c.Query("currency"); v != "" {
		return v
	}
	return "VND"
}

func (h *AdminHandler) Balances(c *gin.Context) {
	out, err := h.settlement.Balances(c.Request.Context(), middleware.GetUserID(c), currencyParam(c),
		boundedInt(c.Query("limit"), 50, 1, 200), boundedInt(c.Query("offset"), 0, 0, 10_000))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, out)
}

func (h *AdminHandler) Statement(c *gin.Context) {
	if !validUUID(c, c.Param("vendorId")) {
		return
	}
	entries, err := h.settlement.Statement(c.Request.Context(), middleware.GetUserID(c), c.Param("vendorId"), currencyParam(c),
		boundedInt(c.Query("limit"), 50, 1, 200), boundedInt(c.Query("offset"), 0, 0, 10_000))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	out := make([]entryResponse, 0, len(entries))
	for _, e := range entries {
		out = append(out, toEntry(e))
	}
	httpresponse.OK(c, http.StatusOK, out)
}

type adjustmentRequest struct {
	VendorID string `json:"vendor_id" binding:"required,uuid"`
	Amount   int64  `json:"amount" binding:"required"`
	Currency string `json:"currency" binding:"required,len=3"`
	Reason   string `json:"reason" binding:"required,max=500"`
}

func (h *AdminHandler) Adjust(c *gin.Context) {
	var req adjustmentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "vendor_id, a non-zero amount, currency and reason are required")
		return
	}
	e, err := h.settlement.Adjust(c.Request.Context(), middleware.GetUserID(c), req.VendorID, req.Amount, req.Currency, req.Reason)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusCreated, toEntry(e))
}

type createBatchRequest struct {
	IdempotencyKey string   `json:"idempotency_key" binding:"required,min=8,max=128"`
	Currency       string   `json:"currency" binding:"required,len=3"`
	VendorIDs      []string `json:"vendor_ids" binding:"omitempty,max=200,dive,uuid"`
}

func (h *AdminHandler) CreateBatch(c *gin.Context) {
	var req createBatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "idempotency_key and currency are required; vendor_ids must be ids")
		return
	}
	batch, skipped, err := h.settlement.CreatePayoutBatch(c.Request.Context(), middleware.GetUserID(c), req.IdempotencyKey, req.Currency, req.VendorIDs)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusCreated, gin.H{"batch": toBatch(batch), "skipped": skipped})
}

func (h *AdminHandler) ListBatches(c *gin.Context) {
	batches, err := h.settlement.ListBatches(c.Request.Context(), middleware.GetUserID(c),
		boundedInt(c.Query("limit"), 20, 1, 100), boundedInt(c.Query("offset"), 0, 0, 10_000))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	out := make([]batchResponse, 0, len(batches))
	for _, b := range batches {
		out = append(out, toBatch(b))
	}
	httpresponse.OK(c, http.StatusOK, out)
}

func (h *AdminHandler) GetBatch(c *gin.Context) {
	if !validUUID(c, c.Param("id")) {
		return
	}
	b, err := h.settlement.GetBatch(c.Request.Context(), middleware.GetUserID(c), c.Param("id"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toBatch(b))
}

type resolvePayoutRequest struct {
	Outcome           string `json:"outcome" binding:"required,oneof=succeeded failed"`
	EvidenceReference string `json:"evidence_reference" binding:"max=200"`
	Note              string `json:"note" binding:"max=500"`
}

func (h *AdminHandler) ResolvePayoutItem(c *gin.Context) {
	if !validUUID(c, c.Param("id")) {
		return
	}
	var req resolvePayoutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "outcome must be succeeded or failed")
		return
	}
	item, err := h.settlement.ResolvePayoutItem(c.Request.Context(), middleware.GetUserID(c), c.Param("id"),
		domain.PayoutResolution{Outcome: domain.PayoutItemStatus(req.Outcome), EvidenceReference: req.EvidenceReference, Note: req.Note})
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toPayoutItem(item))
}

// ----- internal intake from Order -----

type settlementIntakeRequest struct {
	VendorOrderID         string    `json:"vendor_order_id" binding:"required,uuid"`
	OrderID               string    `json:"order_id" binding:"required,uuid"`
	VendorID              string    `json:"vendor_id" binding:"required,uuid"`
	Currency              string    `json:"currency" binding:"required,len=3"`
	SubtotalAmount        int64     `json:"subtotal_amount" binding:"min=0"`
	ShippingAmount        int64     `json:"shipping_amount" binding:"min=0"`
	CommissionAmount      int64     `json:"commission_amount" binding:"min=0"`
	CommissionRateBps     int       `json:"commission_rate_bps" binding:"min=0,max=10000"`
	CommissionRuleVersion *int64    `json:"commission_rule_version"`
	CompletedAt           time.Time `json:"completed_at" binding:"required"`
	EligibleAt            time.Time `json:"eligible_at" binding:"required"`
}

// IngestVendorOrder is Order reporting a completed vendor order: 201 the
// first time, 200 for a replay.
func (h *AdminHandler) IngestVendorOrder(c *gin.Context) {
	var req settlementIntakeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "Invalid settlement vendor order")
		return
	}
	created, err := h.settlement.IngestVendorOrder(c.Request.Context(), domain.SettlementOrder{
		VendorOrderID: req.VendorOrderID, OrderID: req.OrderID, VendorID: req.VendorID, Currency: req.Currency,
		SubtotalAmount: req.SubtotalAmount, ShippingAmount: req.ShippingAmount, CommissionAmount: req.CommissionAmount,
		CommissionRateBps: req.CommissionRateBps, CommissionRuleVersion: req.CommissionRuleVersion,
		CompletedAt: req.CompletedAt.UTC(), EligibleAt: req.EligibleAt.UTC(),
	})
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	httpresponse.OK(c, status, gin.H{"vendor_order_id": req.VendorOrderID, "recorded": true})
}
