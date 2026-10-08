package transport

import (
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/usecase"
)

// SupportHandler serves support cases for buyers, the selling shop and
// admins. The caller's role comes from the verified token; every access
// and transition is checked in the use case.
type SupportHandler struct {
	orders *usecase.OrderUseCase
	log    zerolog.Logger
}

func NewSupportHandler(orders *usecase.OrderUseCase, log zerolog.Logger) *SupportHandler {
	return &SupportHandler{orders: orders, log: log}
}

// maxAttachmentRequestBytes bounds an upload request: one 5 MiB image plus
// the multipart framing.
const maxAttachmentRequestBytes = 5<<20 + 64<<10

func actorOf(c *gin.Context) usecase.SupportActor {
	return usecase.SupportActor{ID: middleware.GetUserID(c), Role: middleware.GetRole(c)}
}

type createSupportCaseRequest struct {
	VendorOrderID string   `json:"vendor_order_id" binding:"required,uuid"`
	Category      string   `json:"category" binding:"required"`
	Message       string   `json:"message" binding:"required"`
	AttachmentIDs []string `json:"attachment_ids" binding:"max=5,dive,uuid"`
	RelatedCaseID string   `json:"related_case_id" binding:"omitempty,uuid"`
}

type supportMessageRequest struct {
	Text          string   `json:"text" binding:"required"`
	AttachmentIDs []string `json:"attachment_ids" binding:"max=5,dive,uuid"`
	Visibility    string   `json:"visibility"`
}

type reopenSupportCaseRequest struct {
	Message string `json:"message" binding:"required"`
}

type assignSupportCaseRequest struct {
	Reason          string `json:"reason" binding:"required,max=500"`
	AssigneeID      string `json:"assignee_id" binding:"required,uuid"`
	ExpectedVersion int64  `json:"expected_version" binding:"required,min=1"`
}

type supportCaseStatusRequest struct {
	Status          string `json:"status" binding:"required"`
	ExpectedVersion int64  `json:"expected_version" binding:"required,min=1"`
	Note            string `json:"note" binding:"max=1000"`
}

type resolveSupportCaseRequest struct {
	ResolutionKind    string `json:"resolution_kind" binding:"required"`
	LinkedOperationID string `json:"linked_operation_id" binding:"omitempty,uuid"`
	Reason            string `json:"reason" binding:"required,max=500"`
	ExpectedVersion   int64  `json:"expected_version" binding:"required,min=1"`
}

type closeSupportCaseRequest struct {
	Reason          string `json:"reason" binding:"required,max=500"`
	ExpectedVersion int64  `json:"expected_version" binding:"required,min=1"`
}

func (h *SupportHandler) Capability(c *gin.Context) {
	capability := h.orders.SupportCapability()
	c.Header("Cache-Control", "no-store")
	httpresponse.OK(c, http.StatusOK, gin.H{
		"enabled": capability.Enabled, "attachments_enabled": capability.AttachmentsEnabled, "max_attachments": capability.MaxAttachments,
		"max_attachment_bytes": capability.MaxAttachmentBytes, "max_message_chars": capability.MaxMessageChars,
		"reopen_window_days": capability.ReopenWindowDays, "poll_interval_seconds": capability.PollIntervalSeconds, "pilot_only": capability.PilotOnly,
	})
}

// Create opens a case on the buyer's order. Idempotency-Key (optional)
// makes a resend return the same case (200) instead of a conflict.
func (h *SupportHandler) Create(c *gin.Context) {
	if !validID(c, c.Param("id")) {
		return
	}
	var req createSupportCaseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error",
			"vendor_order_id, category and message are required; attachment_ids must be at most 5 ids")
		return
	}
	sc, replayed, err := h.orders.CreateSupportCase(c.Request.Context(), middleware.GetUserID(c), usecase.CreateSupportCaseInput{
		OrderID: c.Param("id"), VendorOrderID: req.VendorOrderID, Category: req.Category, Message: req.Message,
		AttachmentIDs: req.AttachmentIDs, RelatedCaseID: req.RelatedCaseID, IdempotencyKey: c.GetHeader("Idempotency-Key"),
	})
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
	}
	httpresponse.OK(c, status, toSupportCaseResponse(sc))
}

// List is the caller's case list: the buyer's own, the shop's
// (?vendor_id=) or the admin queue (?assignee=me|<id>&unassigned=&overdue=).
func (h *SupportHandler) List(c *gin.Context) {
	limit := parseIntDefault(c.Query("limit"), 20, 1, 100)
	ctx, userID := c.Request.Context(), middleware.GetUserID(c)
	var page *usecase.SupportCasePage
	var err error
	switch middleware.GetRole(c) {
	case "buyer":
		page, err = h.orders.ListMySupportCases(ctx, userID, c.Query("status"), c.Query("cursor"), limit)
	case "vendor":
		page, err = h.orders.ListVendorSupportCases(ctx, userID, c.Query("vendor_id"), c.Query("status"), c.Query("cursor"), limit)
	default:
		assignee := c.Query("assignee")
		if assignee != "" && assignee != "me" && !validID(c, assignee) {
			return
		}
		page, err = h.orders.ListSupportCases(ctx, userID, usecase.AdminSupportFilter{Status: c.Query("status"), AssigneeID: assignee,
			Unassigned: c.Query("unassigned") == "true", Overdue: c.Query("overdue") == "true"}, c.Query("cursor"), limit)
	}
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	items := make([]supportCaseResponse, 0, len(page.Items))
	for _, sc := range page.Items {
		items = append(items, toSupportCaseResponse(sc))
	}
	c.Header("Cache-Control", "no-store")
	httpresponse.OK(c, http.StatusOK, gin.H{"items": items, "next_cursor": page.NextCursor})
}

func (h *SupportHandler) Get(c *gin.Context) {
	if !validID(c, c.Param("caseID")) {
		return
	}
	detail, err := h.orders.GetSupportCase(c.Request.Context(), actorOf(c), c.Param("caseID"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	httpresponse.OK(c, http.StatusOK, toSupportCaseDetailResponse(detail))
}

func (h *SupportHandler) Messages(c *gin.Context) {
	if !validID(c, c.Param("caseID")) {
		return
	}
	detail, err := h.orders.GetSupportCase(c.Request.Context(), actorOf(c), c.Param("caseID"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	httpresponse.OK(c, http.StatusOK, toSupportMessageResponses(detail.Messages))
}

// PostMessage adds a message; Idempotency-Key (optional) makes a resend
// return the same message.
func (h *SupportHandler) PostMessage(c *gin.Context) {
	if !validID(c, c.Param("caseID")) {
		return
	}
	var req supportMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "text is required; attachment_ids must be at most 5 ids")
		return
	}
	m, replayed, err := h.orders.PostSupportMessage(c.Request.Context(), actorOf(c), c.Param("caseID"), usecase.SupportMessageInput{
		Text: req.Text, AttachmentIDs: req.AttachmentIDs, Visibility: req.Visibility, IdempotencyKey: c.GetHeader("Idempotency-Key"),
	})
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
	}
	httpresponse.OK(c, status, toSupportMessageResponse(m))
}

func (h *SupportHandler) Reopen(c *gin.Context) {
	if !validID(c, c.Param("caseID")) {
		return
	}
	var req reopenSupportCaseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "A message explaining why is required")
		return
	}
	sc, err := h.orders.ReopenSupportCase(c.Request.Context(), middleware.GetUserID(c), c.Param("caseID"), req.Message)
	h.respondCase(c, sc, err)
}

func (h *SupportHandler) Confirm(c *gin.Context) {
	if !validID(c, c.Param("caseID")) {
		return
	}
	sc, err := h.orders.ConfirmSupportCase(c.Request.Context(), middleware.GetUserID(c), c.Param("caseID"))
	h.respondCase(c, sc, err)
}

func (h *SupportHandler) Assign(c *gin.Context) {
	if !validID(c, c.Param("caseID")) {
		return
	}
	var req assignSupportCaseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "assignee_id and expected_version are required")
		return
	}
	sc, err := h.orders.AssignSupportCase(c.Request.Context(), middleware.GetUserID(c), c.Param("caseID"), req.AssigneeID, req.ExpectedVersion, req.Reason)
	h.respondCase(c, sc, err)
}

func (h *SupportHandler) ChangeStatus(c *gin.Context) {
	if !validID(c, c.Param("caseID")) {
		return
	}
	var req supportCaseStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "status and expected_version are required; note at most 1000 characters")
		return
	}
	sc, err := h.orders.ChangeSupportCaseStatus(c.Request.Context(), middleware.GetUserID(c), c.Param("caseID"), req.Status, req.ExpectedVersion, req.Note)
	h.respondCase(c, sc, err)
}

// Resolve records the conclusion: 200 when resolved, 202 when the case
// waits for its linked refund or return.
func (h *SupportHandler) Resolve(c *gin.Context) {
	if !validID(c, c.Param("caseID")) {
		return
	}
	var req resolveSupportCaseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error",
			"resolution_kind, a reason of at most 500 characters and expected_version are required")
		return
	}
	sc, pending, err := h.orders.ResolveSupportCase(c.Request.Context(), middleware.GetUserID(c), c.Param("caseID"), usecase.ResolveInput{
		Kind: req.ResolutionKind, LinkedOperationID: req.LinkedOperationID, Reason: req.Reason, ExpectedVersion: req.ExpectedVersion,
	})
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	status := http.StatusOK
	if pending {
		status = http.StatusAccepted
	}
	httpresponse.OK(c, status, toSupportCaseResponse(sc))
}

type caseRefundRequest struct {
	Amount          int64  `json:"amount" binding:"required,min=1"`
	Reason          string `json:"reason" binding:"required,max=500"`
	ExpectedVersion int64  `json:"expected_version" binding:"required,min=1"`
}

// Refund opens a dispute refund from the case and links it as the pending
// resolution (202; 200 on an Idempotency-Key replay).
func (h *SupportHandler) Refund(c *gin.Context) {
	if !validID(c, c.Param("caseID")) {
		return
	}
	var req caseRefundRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "amount, a reason of at most 500 characters and expected_version are required")
		return
	}
	sc, refund, replayed, err := h.orders.CreateCaseRefund(c.Request.Context(), middleware.GetUserID(c), c.Param("caseID"), usecase.CaseRefundInput{
		Amount: req.Amount, Reason: req.Reason, ExpectedVersion: req.ExpectedVersion, IdempotencyKey: c.GetHeader("Idempotency-Key")})
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	status := http.StatusAccepted
	if replayed {
		status = http.StatusOK
	}
	httpresponse.OK(c, status, gin.H{"case": toSupportCaseResponse(sc), "refund_id": refund.ID, "refund_status": refund.Status,
		"amount": refund.Amount, "currency": refund.Currency})
}

func (h *SupportHandler) Close(c *gin.Context) {
	if !validID(c, c.Param("caseID")) {
		return
	}
	var req closeSupportCaseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "A reason of at most 500 characters and expected_version are required")
		return
	}
	sc, err := h.orders.CloseSupportCase(c.Request.Context(), middleware.GetUserID(c), c.Param("caseID"), req.ExpectedVersion, req.Reason)
	h.respondCase(c, sc, err)
}

// Upload stores one image (multipart field "file") for a later message.
func (h *SupportHandler) Upload(c *gin.Context) {
	file, err := c.FormFile("file")
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httpresponse.Error(c, http.StatusUnprocessableEntity, string(domain.CodeUnsupportedAttachment), "Each image must be at most 5 MiB")
			return
		}
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "A file field named file is required")
		return
	}
	f, err := file.Open()
	if err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "The file could not be read")
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxAttachmentRequestBytes))
	if err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "The file could not be read")
		return
	}
	a, err := h.orders.UploadSupportAttachment(c.Request.Context(), actorOf(c), data)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusCreated, toAttachmentResponse(a))
}

// Attachment streams an evidence image after the access check. It is never
// cached by shared caches and never sniffed as another type.
func (h *SupportHandler) Attachment(c *gin.Context) {
	if !validID(c, c.Param("caseID")) || !validID(c, c.Param("attachmentID")) {
		return
	}
	file, err := h.orders.OpenSupportAttachment(c.Request.Context(), actorOf(c), c.Param("caseID"), c.Param("attachmentID"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	defer file.Body.Close()
	c.DataFromReader(http.StatusOK, file.SizeBytes, file.ContentType, file.Body, map[string]string{
		"Cache-Control": "private, no-store", "X-Content-Type-Options": "nosniff", "Content-Disposition": "inline",
		"Content-Security-Policy": "default-src 'none'; sandbox",
	})
}

func (h *SupportHandler) respondCase(c *gin.Context, sc *domain.SupportCase, err error) {
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toSupportCaseResponse(sc))
}

type supportCaseResponse struct {
	ActionDueAt    *time.Time `json:"action_due_at"`
	WaitingOn      string     `json:"waiting_on"`
	ID             string     `json:"id"`
	OrderID        string     `json:"order_id"`
	VendorOrderID  string     `json:"vendor_order_id"`
	VendorID       string     `json:"vendor_id"`
	BuyerID        string     `json:"buyer_id,omitempty"`
	Category       string     `json:"category"`
	Status         string     `json:"status"`
	AssigneeID     *string    `json:"assignee_id,omitempty"`
	PolicyVersion  string     `json:"policy_version"`
	DueAt          *time.Time `json:"due_at"`
	FinancialHold  bool       `json:"financial_hold"`
	ResolutionKind *string    `json:"resolution_kind"`
	ResolutionRef  *string    `json:"resolution_ref"`
	ResolutionNote *string    `json:"resolution_note"`
	ResolvedAt     *time.Time `json:"resolved_at"`
	ClosedAt       *time.Time `json:"closed_at"`
	RelatedCaseID  *string    `json:"related_case_id,omitempty"`
	Version        int64      `json:"version"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

func toSupportCaseResponse(sc *domain.SupportCase) supportCaseResponse {
	return supportCaseResponse{ActionDueAt: sc.ActionDueAt, WaitingOn: sc.WaitingOn, ID: sc.ID, OrderID: sc.OrderID, VendorOrderID: sc.VendorOrderID, VendorID: sc.VendorID, BuyerID: sc.BuyerID,
		Category: string(sc.Category), Status: string(sc.Status), AssigneeID: sc.AssigneeID, PolicyVersion: sc.PolicyVersion, DueAt: sc.DueAt,
		FinancialHold: sc.FinancialHold, ResolutionKind: sc.ResolutionKind, ResolutionRef: sc.ResolutionRef, ResolutionNote: sc.ResolutionNote,
		ResolvedAt: sc.ResolvedAt, ClosedAt: sc.ClosedAt, RelatedCaseID: sc.RelatedCaseID, Version: sc.Version, CreatedAt: sc.CreatedAt, UpdatedAt: sc.UpdatedAt}
}

type attachmentResponse struct {
	ID          string `json:"id"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
}

func toAttachmentResponse(a *domain.CaseAttachment) attachmentResponse {
	return attachmentResponse{ID: a.ID, ContentType: a.ContentType, SizeBytes: a.SizeBytes}
}

type supportMessageResponse struct {
	ID          string               `json:"id"`
	AuthorID    string               `json:"author_id,omitempty"`
	AuthorRole  string               `json:"author_role"`
	Visibility  string               `json:"visibility"`
	Text        string               `json:"text"`
	Attachments []attachmentResponse `json:"attachments"`
	CreatedAt   time.Time            `json:"created_at"`
}

func toSupportMessageResponse(m *domain.SupportMessage) supportMessageResponse {
	attachments := make([]attachmentResponse, 0, len(m.Attachments))
	for _, a := range m.Attachments {
		attachments = append(attachments, toAttachmentResponse(a))
	}
	return supportMessageResponse{ID: m.ID, AuthorID: m.AuthorID, AuthorRole: m.AuthorRole, Visibility: m.Visibility, Text: m.Text,
		Attachments: attachments, CreatedAt: m.CreatedAt}
}

func toSupportMessageResponses(messages []*domain.SupportMessage) []supportMessageResponse {
	out := make([]supportMessageResponse, 0, len(messages))
	for _, m := range messages {
		out = append(out, toSupportMessageResponse(m))
	}
	return out
}

type supportEventResponse struct {
	ID         string    `json:"id"`
	ActorID    *string   `json:"actor_id,omitempty"`
	ActorRole  string    `json:"actor_role"`
	Action     string    `json:"action"`
	FromStatus *string   `json:"from_status"`
	ToStatus   string    `json:"to_status"`
	Note       *string   `json:"note,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type caseHoldResponse struct {
	Status    string    `json:"status"`
	Note      *string   `json:"note,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

type supportCaseDetailResponse struct {
	supportCaseResponse
	Messages []supportMessageResponse `json:"messages"`
	Events   []supportEventResponse   `json:"events"`
	// SettlementHold (admin only): the case's payout hold in Payment.
	SettlementHold *caseHoldResponse `json:"settlement_hold,omitempty"`
}

func toSupportCaseDetailResponse(d *usecase.SupportCaseDetail) supportCaseDetailResponse {
	events := make([]supportEventResponse, 0, len(d.Events))
	for _, e := range d.Events {
		events = append(events, supportEventResponse{ID: e.ID, ActorID: e.ActorID, ActorRole: e.ActorRole, Action: e.Action,
			FromStatus: e.FromStatus, ToStatus: e.ToStatus, Note: e.Note, CreatedAt: e.CreatedAt})
	}
	out := supportCaseDetailResponse{supportCaseResponse: toSupportCaseResponse(d.Case), Messages: toSupportMessageResponses(d.Messages), Events: events}
	if d.Hold != nil {
		out.SettlementHold = &caseHoldResponse{Status: d.Hold.Status, Note: d.Hold.Note, UpdatedAt: d.Hold.UpdatedAt}
	}
	return out
}

// isAttachmentUpload reports whether the matched route is an upload, which
// gets a larger body limit than the 1 MiB JSON default.
func isAttachmentUpload(fullPath string) bool {
	switch fullPath {
	case "/api/orders/support-attachments", "/api/orders/vendor/support-attachments", "/api/orders/admin/support-attachments":
		return true
	}
	return false
}
