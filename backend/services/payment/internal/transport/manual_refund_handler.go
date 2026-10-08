package transport

import (
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/payment/internal/domain"
	"shopee/backend/services/payment/internal/repository"
	"shopee/backend/services/payment/internal/usecase"
)

// ManualRefundHandler serves AF-06: the buyer's refund status and
// destination, and the finance steps of a manual bank-transfer refund.
type ManualRefundHandler struct {
	manual *usecase.ManualRefundUseCase
	log    zerolog.Logger
}

func NewManualRefundHandler(manual *usecase.ManualRefundUseCase, log zerolog.Logger) *ManualRefundHandler {
	return &ManualRefundHandler{manual: manual, log: log}
}

// ----- buyer -----

type destinationSummary struct {
	Version        int        `json:"version"`
	Masked         string     `json:"masked"`
	Status         string     `json:"status"`
	SubmittedAt    time.Time  `json:"submitted_at"`
	DecidedAt      *time.Time `json:"decided_at,omitempty"`
	DecisionReason *string    `json:"decision_reason,omitempty"`
}

func toDestinationSummary(d *domain.RefundDestination, withReason bool) *destinationSummary {
	if d == nil {
		return nil
	}
	out := &destinationSummary{Version: d.Version, Masked: d.Masked(), Status: string(d.Status), SubmittedAt: d.SubmittedAt, DecidedAt: d.DecidedAt}
	// A buyer sees why a destination was rejected, never a verifier's note.
	if withReason || d.Status == domain.DestinationRejected {
		out.DecisionReason = d.DecisionReason
	}
	return out
}

type timelineEvent struct {
	Event string    `json:"event"`
	At    time.Time `json:"at"`
}

type buyerRefundResponse struct {
	ID                   string              `json:"id"`
	OrderID              string              `json:"order_id"`
	OrderRefundID        *string             `json:"order_refund_id,omitempty"`
	VendorOrderID        *string             `json:"vendor_order_id,omitempty"`
	Amount               int64               `json:"amount"`
	Currency             string              `json:"currency"`
	Status               string              `json:"status"`
	Stage                string              `json:"stage"`
	Destination          *destinationSummary `json:"destination,omitempty"`
	CanSubmitDestination bool                `json:"can_submit_destination"`
	Timeline             []timelineEvent     `json:"timeline"`
	CreatedAt            time.Time           `json:"created_at"`
	ResolvedAt           *time.Time          `json:"resolved_at,omitempty"`
}

func toBuyerRefund(v *usecase.RefundView) buyerRefundResponse {
	r := v.Refund
	out := buyerRefundResponse{ID: r.ID, OrderID: r.OrderID, OrderRefundID: r.OrderRefundID, VendorOrderID: r.VendorOrderID, Amount: r.Amount,
		Currency: r.Currency, Status: string(r.Status), Stage: v.BuyerStage, Destination: toDestinationSummary(v.Destination, false),
		CanSubmitDestination: v.CanSubmitDestination, CreatedAt: r.CreatedAt, ResolvedAt: r.ResolvedAt,
		Timeline: []timelineEvent{{Event: "requested", At: r.CreatedAt}}}
	if d := v.Destination; d != nil {
		out.Timeline = append(out.Timeline, timelineEvent{Event: "destination_submitted", At: d.SubmittedAt})
		if d.DecidedAt != nil {
			out.Timeline = append(out.Timeline, timelineEvent{Event: "destination_" + string(d.Status), At: *d.DecidedAt})
		}
	}
	if v.Attempt != nil && v.Attempt.ClaimedAt != nil {
		out.Timeline = append(out.Timeline, timelineEvent{Event: "transfer_in_progress", At: *v.Attempt.ClaimedAt})
	}
	if r.ResolvedAt != nil {
		out.Timeline = append(out.Timeline, timelineEvent{Event: string(r.Status), At: *r.ResolvedAt})
	}
	return out
}

// BuyerList lists the caller's refunds (?order_id= narrows to one order).
func (h *ManualRefundHandler) BuyerList(c *gin.Context) {
	var orderID *string
	if raw := c.Query("order_id"); raw != "" {
		if _, err := uuid.Parse(raw); err != nil {
			httpresponse.Error(c, http.StatusBadRequest, "validation_error", "Invalid order id")
			return
		}
		orderID = &raw
	}
	views, err := h.manual.BuyerRefunds(c.Request.Context(), middleware.GetUserID(c), orderID)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	out := make([]buyerRefundResponse, 0, len(views))
	for _, v := range views {
		out = append(out, toBuyerRefund(v))
	}
	httpresponse.OK(c, http.StatusOK, out)
}

func (h *ManualRefundHandler) BuyerGet(c *gin.Context) {
	if _, err := uuid.Parse(c.Param("id")); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "Invalid refund id")
		return
	}
	v, err := h.manual.BuyerRefund(c.Request.Context(), middleware.GetUserID(c), c.Param("id"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toBuyerRefund(v))
}

type beneficiaryRequest struct {
	BankCode        string `json:"bank_code" binding:"required,max=20"`
	AccountNumber   string `json:"account_number" binding:"required,max=40"`
	AccountName     string `json:"account_name" binding:"required,max=120"`
	ExpectedVersion *int   `json:"expected_version" binding:"required,min=0"`
}

// SubmitBeneficiary stores where the buyer wants the money; the answer
// carries the masked destination only.
func (h *ManualRefundHandler) SubmitBeneficiary(c *gin.Context) {
	if _, err := uuid.Parse(c.Param("id")); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "Invalid refund id")
		return
	}
	var req beneficiaryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "bank_code, account_number, account_name and expected_version are required")
		return
	}
	v, err := h.manual.SubmitDestination(c.Request.Context(), middleware.GetUserID(c), c.Param("id"),
		domain.Beneficiary{BankCode: req.BankCode, AccountNumber: req.AccountNumber, AccountName: req.AccountName}, *req.ExpectedVersion)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toBuyerRefund(v))
}

// ----- admin -----

type attemptResponse struct {
	ID                 string             `json:"id"`
	RefundID           string             `json:"refund_id"`
	DestinationVersion int                `json:"destination_version"`
	Amount             int64              `json:"amount"`
	Currency           string             `json:"currency"`
	Stage              string             `json:"stage"`
	Version            int                `json:"version"`
	PreparedBy         string             `json:"prepared_by"`
	PrepareReason      string             `json:"prepare_reason"`
	ClaimedBy          *string            `json:"claimed_by,omitempty"`
	ClaimedAt          *time.Time         `json:"claimed_at,omitempty"`
	LeaseExpiresAt     *time.Time         `json:"lease_expires_at,omitempty"`
	SourceAccount      *string            `json:"source_account,omitempty"`
	BankReference      *string            `json:"bank_reference,omitempty"`
	ExecutedAt         *time.Time         `json:"executed_at,omitempty"`
	SubmittedBy        *string            `json:"submitted_by,omitempty"`
	SubmittedAt        *time.Time         `json:"submitted_at,omitempty"`
	DecidedBy          *string            `json:"decided_by,omitempty"`
	DecidedAt          *time.Time         `json:"decided_at,omitempty"`
	DecisionReason     *string            `json:"decision_reason,omitempty"`
	Evidence           []evidenceResponse `json:"evidence"`
	CreatedAt          time.Time          `json:"created_at"`
	UpdatedAt          time.Time          `json:"updated_at"`
}

type evidenceResponse struct {
	ID          string    `json:"id"`
	ContentType string    `json:"content_type"`
	SizeBytes   int       `json:"size_bytes"`
	SHA256      string    `json:"sha256"`
	UploadedBy  string    `json:"uploaded_by"`
	State       string    `json:"state"`
	CreatedAt   time.Time `json:"created_at"`
}

func toEvidence(e *domain.RefundEvidence) evidenceResponse {
	return evidenceResponse{ID: e.ID, ContentType: e.ContentType, SizeBytes: e.SizeBytes, SHA256: e.SHA256, UploadedBy: e.UploadedBy, State: e.State, CreatedAt: e.CreatedAt}
}

func toAttempt(a *domain.ManualRefundAttempt, evidence []*domain.RefundEvidence) attemptResponse {
	out := attemptResponse{ID: a.ID, RefundID: a.RefundID, DestinationVersion: a.DestinationVersion, Amount: a.Amount, Currency: a.Currency,
		Stage: string(a.Stage), Version: a.Version, PreparedBy: a.PreparedBy, PrepareReason: a.PrepareReason, ClaimedBy: a.ClaimedBy, ClaimedAt: a.ClaimedAt,
		LeaseExpiresAt: a.LeaseExpiresAt, SourceAccount: a.SourceAccount, BankReference: a.BankReference, ExecutedAt: a.ExecutedAt,
		SubmittedBy: a.SubmittedBy, SubmittedAt: a.SubmittedAt, DecidedBy: a.DecidedBy, DecidedAt: a.DecidedAt, DecisionReason: a.DecisionReason,
		Evidence: []evidenceResponse{}, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt}
	for _, e := range evidence {
		out.Evidence = append(out.Evidence, toEvidence(e))
	}
	return out
}

type adminDestinationResponse struct {
	destinationSummary
	ID          string  `json:"id"`
	KeyVersion  int     `json:"key_version"`
	SubmittedBy string  `json:"submitted_by"`
	DecidedBy   *string `json:"decided_by,omitempty"`
}

type manualDetailResponse struct {
	Refund       refundResponse             `json:"refund"`
	Stage        string                     `json:"stage"`
	Destinations []adminDestinationResponse `json:"destinations"`
	Attempts     []attemptResponse          `json:"attempts"`
	Audit        []repository.AuditEntry    `json:"audit"`
}

// AdminDetail shows a refund's destinations (masked), attempts and audit.
func (h *ManualRefundHandler) AdminDetail(c *gin.Context) {
	if _, err := uuid.Parse(c.Param("id")); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "Invalid refund id")
		return
	}
	d, err := h.manual.Detail(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	out := manualDetailResponse{Refund: toRefundResponse(d.View.Refund), Stage: d.View.Stage, Destinations: []adminDestinationResponse{},
		Attempts: []attemptResponse{}, Audit: d.Audit}
	for _, dest := range d.Destinations {
		out.Destinations = append(out.Destinations, adminDestinationResponse{destinationSummary: *toDestinationSummary(dest, true), ID: dest.ID,
			KeyVersion: dest.KeyVersion, SubmittedBy: dest.SubmittedBy, DecidedBy: dest.DecidedBy})
	}
	for _, a := range d.Attempts {
		out.Attempts = append(out.Attempts, toAttempt(a, d.Evidence[a.ID]))
	}
	if out.Audit == nil {
		out.Audit = []repository.AuditEntry{}
	}
	httpresponse.OK(c, http.StatusOK, out)
}

type destinationDecisionRequest struct {
	DestinationVersion int    `json:"destination_version" binding:"required,min=1"`
	Decision           string `json:"decision" binding:"required,oneof=verify reject"`
	Reason             string `json:"reason" binding:"required,max=500"`
	Proof              string `json:"proof" binding:"max=200"`
}

func (h *ManualRefundHandler) DecideDestination(c *gin.Context) {
	if _, err := uuid.Parse(c.Param("id")); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "Invalid refund id")
		return
	}
	var req destinationDecisionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "destination_version, decision (verify|reject) and reason are required")
		return
	}
	v, err := h.manual.DecideDestination(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), usecase.DestinationDecision{
		Version: req.DestinationVersion, Verify: req.Decision == "verify", Reason: req.Reason, Proof: req.Proof})
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"stage": v.Stage, "destination": toDestinationSummary(v.Destination, true)})
}

type sensitiveAccessRequest struct {
	Reason string `json:"reason" binding:"required,max=500"`
	Proof  string `json:"proof" binding:"max=200"`
}

// RevealDestination answers the full destination once, never cached.
func (h *ManualRefundHandler) RevealDestination(c *gin.Context) {
	if _, err := uuid.Parse(c.Param("id")); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "Invalid refund id")
		return
	}
	var req sensitiveAccessRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "A reason is required")
		return
	}
	d, err := h.manual.RevealDestination(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), req.Reason, req.Proof)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	c.Header("Cache-Control", "no-store, private")
	c.Header("Pragma", "no-cache")
	httpresponse.OK(c, http.StatusOK, gin.H{"destination_version": d.Version, "bank_code": d.Beneficiary.BankCode,
		"account_number": d.Beneficiary.AccountNumber, "account_name": d.Beneficiary.AccountName})
}

type prepareAttemptRequest struct {
	DestinationVersion int    `json:"destination_version" binding:"required,min=1"`
	Reason             string `json:"reason" binding:"required,max=500"`
}

func (h *ManualRefundHandler) PrepareAttempt(c *gin.Context) {
	if _, err := uuid.Parse(c.Param("id")); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "Invalid refund id")
		return
	}
	var req prepareAttemptRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "destination_version and reason are required")
		return
	}
	a, err := h.manual.PrepareAttempt(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), req.DestinationVersion, req.Reason)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusCreated, toAttempt(a, nil))
}

type attemptVersionRequest struct {
	ExpectedVersion int    `json:"expected_version" binding:"required,min=1"`
	Reason          string `json:"reason" binding:"max=500"`
}

func (h *ManualRefundHandler) attemptID(c *gin.Context) (string, bool) {
	if _, err := uuid.Parse(c.Param("id")); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "Invalid attempt id")
		return "", false
	}
	return c.Param("id"), true
}

func (h *ManualRefundHandler) Claim(c *gin.Context) {
	id, ok := h.attemptID(c)
	if !ok {
		return
	}
	var req attemptVersionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "expected_version is required")
		return
	}
	a, err := h.manual.Claim(c.Request.Context(), middleware.GetUserID(c), id, req.ExpectedVersion)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toAttempt(a, nil))
}

func (h *ManualRefundHandler) Cancel(c *gin.Context) {
	id, ok := h.attemptID(c)
	if !ok {
		return
	}
	var req attemptVersionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "expected_version and reason are required")
		return
	}
	a, err := h.manual.Cancel(c.Request.Context(), middleware.GetUserID(c), id, req.ExpectedVersion, req.Reason)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toAttempt(a, nil))
}

type submitAttemptRequest struct {
	ExpectedVersion int       `json:"expected_version" binding:"required,min=1"`
	BankReference   string    `json:"bank_reference" binding:"required,max=100"`
	SourceAccount   string    `json:"source_account" binding:"required,max=64"`
	ExecutedAt      time.Time `json:"executed_at" binding:"required"`
	EvidenceIDs     []string  `json:"evidence_ids" binding:"max=5"`
}

func (h *ManualRefundHandler) Submit(c *gin.Context) {
	id, ok := h.attemptID(c)
	if !ok {
		return
	}
	var req submitAttemptRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "expected_version, bank_reference, source_account and executed_at are required")
		return
	}
	a, err := h.manual.Submit(c.Request.Context(), middleware.GetUserID(c), id, usecase.SubmitInput{ExpectedVersion: req.ExpectedVersion,
		Submission:  domain.Submission{BankReference: req.BankReference, SourceAccount: req.SourceAccount, ExecutedAt: req.ExecutedAt},
		EvidenceIDs: req.EvidenceIDs})
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toAttempt(a, nil))
}

type attemptDecisionRequest struct {
	ExpectedVersion int    `json:"expected_version" binding:"required,min=1"`
	Decision        string `json:"decision" binding:"required,oneof=confirm fail"`
	Reason          string `json:"reason" binding:"required,max=500"`
	Proof           string `json:"proof" binding:"max=200"`
}

func (h *ManualRefundHandler) Decide(c *gin.Context) {
	id, ok := h.attemptID(c)
	if !ok {
		return
	}
	var req attemptDecisionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "expected_version, decision (confirm|fail) and reason are required")
		return
	}
	a, err := h.manual.Decide(c.Request.Context(), middleware.GetUserID(c), id, usecase.AttemptDecision{ExpectedVersion: req.ExpectedVersion,
		Confirm: req.Decision == "confirm", Reason: req.Reason, Proof: req.Proof})
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toAttempt(a, nil))
}

// UploadEvidence takes one multipart "file" (JPEG, PNG or PDF, 5 MiB).
func (h *ManualRefundHandler) UploadEvidence(c *gin.Context) {
	id, ok := h.attemptID(c)
	if !ok {
		return
	}
	file, err := c.FormFile("file")
	if err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "A file field is required")
		return
	}
	if file.Size > domain.MaxEvidenceBytes {
		httpresponse.Error(c, http.StatusRequestEntityTooLarge, "payload_too_large", "Evidence files are at most 5 MiB")
		return
	}
	f, err := file.Open()
	if err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "The file cannot be read")
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, domain.MaxEvidenceBytes+1))
	if err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "The file cannot be read")
		return
	}
	e, err := h.manual.UploadEvidence(c.Request.Context(), middleware.GetUserID(c), id, data)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusCreated, toEvidence(e))
}

// Evidence streams one stored receipt, never cached.
func (h *ManualRefundHandler) Evidence(c *gin.Context) {
	if _, err := uuid.Parse(c.Param("id")); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "Invalid evidence id")
		return
	}
	e, body, err := h.manual.OpenEvidence(c.Request.Context(), middleware.GetUserID(c), c.Param("id"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	defer body.Close()
	c.Header("Cache-Control", "no-store, private")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Disposition", "attachment; filename=\"refund-evidence-"+e.ID+evidenceExtension(e.ContentType)+"\"")
	c.DataFromReader(http.StatusOK, int64(e.SizeBytes), e.ContentType, body, nil)
}

func evidenceExtension(contentType string) string {
	switch contentType {
	case "image/png":
		return ".png"
	case "application/pdf":
		return ".pdf"
	}
	return ".jpg"
}
