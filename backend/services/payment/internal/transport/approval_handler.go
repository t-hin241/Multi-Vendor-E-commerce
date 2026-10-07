package transport

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/adminaccess"
	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/payment/internal/domain"
	"shopee/backend/services/payment/internal/usecase"
)

// ApprovalHandler serves maker-checker requests for manual money actions
// (AF-19).
type ApprovalHandler struct {
	approvals *usecase.ApprovalUseCase
	log       zerolog.Logger
}

func NewApprovalHandler(approvals *usecase.ApprovalUseCase, log zerolog.Logger) *ApprovalHandler {
	return &ApprovalHandler{approvals: approvals, log: log}
}

type approvalResponse struct {
	ID                       string          `json:"id"`
	OperationKind            string          `json:"operation_kind"`
	TargetID                 string          `json:"target_id"`
	Payload                  json.RawMessage `json:"payload"`
	PayloadHash              string          `json:"payload_hash"`
	Snapshot                 json.RawMessage `json:"snapshot"`
	Status                   string          `json:"status"`
	MakerID                  string          `json:"maker_id"`
	MakerPermissionVersion   int64           `json:"maker_permission_version"`
	Reason                   string          `json:"reason"`
	CheckerID                *string         `json:"checker_id,omitempty"`
	CheckerPermissionVersion *int64          `json:"checker_permission_version,omitempty"`
	DecisionReason           *string         `json:"decision_reason,omitempty"`
	Version                  int64           `json:"version"`
	ExpiresAt                time.Time       `json:"expires_at"`
	CreatedAt                time.Time       `json:"created_at"`
	SubmittedAt              *time.Time      `json:"submitted_at,omitempty"`
	DecidedAt                *time.Time      `json:"decided_at,omitempty"`
	ExecutionRef             *string         `json:"execution_ref,omitempty"`
}

func toApprovalResponse(a *domain.ApprovalRequest) approvalResponse {
	return approvalResponse{ID: a.ID, OperationKind: string(a.Kind), TargetID: a.TargetID, Payload: a.Payload, PayloadHash: a.PayloadHash,
		Snapshot: a.Snapshot, Status: string(a.Status), MakerID: a.MakerID, MakerPermissionVersion: a.MakerPermissionVersion, Reason: a.Reason,
		CheckerID: a.CheckerID, CheckerPermissionVersion: a.CheckerPermissionVersion, DecisionReason: a.DecisionReason, Version: a.Version,
		ExpiresAt: a.ExpiresAt, CreatedAt: a.CreatedAt, SubmittedAt: a.SubmittedAt, DecidedAt: a.DecidedAt, ExecutionRef: a.ExecutionRef}
}

type draftRequest struct {
	OperationKind string          `json:"operation_kind" binding:"required"`
	TargetID      string          `json:"target_id" binding:"required"`
	Payload       json.RawMessage `json:"payload" binding:"required"`
	Reason        string          `json:"reason" binding:"required,max=500"`
}

func (h *ApprovalHandler) Create(c *gin.Context) {
	var req draftRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "operation_kind, target_id, payload and reason are required")
		return
	}
	a, err := h.approvals.Draft(c.Request.Context(), middleware.GetUserID(c), usecase.DraftInput{Kind: domain.ApprovalKind(req.OperationKind),
		TargetID: req.TargetID, Payload: req.Payload, Reason: req.Reason, PermissionVersion: adminaccess.PermissionVersion(c)})
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusCreated, toApprovalResponse(a))
}

type submissionRequest struct {
	Proof           string `json:"proof" binding:"required,max=128"`
	ExpectedVersion int64  `json:"expected_version" binding:"required,min=1"`
}

// Submit sends the draft to the checkers; the proof is never logged.
func (h *ApprovalHandler) Submit(c *gin.Context) {
	var req submissionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "proof and expected_version are required")
		return
	}
	a, err := h.approvals.Submit(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), req.Proof, req.ExpectedVersion)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toApprovalResponse(a))
}

type decisionRequest struct {
	Decision        string `json:"decision" binding:"required,oneof=approve reject"`
	Proof           string `json:"proof" binding:"required,max=128"`
	ExpectedVersion int64  `json:"expected_version" binding:"required,min=1"`
	Reason          string `json:"reason" binding:"required,max=500"`
}

func (h *ApprovalHandler) Decide(c *gin.Context) {
	var req decisionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "decision (approve|reject), proof, expected_version and reason are required")
		return
	}
	a, err := h.approvals.Decide(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), usecase.DecisionInput{Approve: req.Decision == "approve",
		Proof: req.Proof, ExpectedVersion: req.ExpectedVersion, Reason: req.Reason, PermissionVersion: adminaccess.PermissionVersion(c)})
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toApprovalResponse(a))
}

type cancellationRequest struct {
	ExpectedVersion int64 `json:"expected_version" binding:"required,min=1"`
}

func (h *ApprovalHandler) Cancel(c *gin.Context) {
	var req cancellationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "expected_version is required")
		return
	}
	a, err := h.approvals.Cancel(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), req.ExpectedVersion)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toApprovalResponse(a))
}

func (h *ApprovalHandler) List(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	items, err := h.approvals.List(c.Request.Context(), c.Query("status"), limit, offset)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	out := make([]approvalResponse, 0, len(items))
	for _, a := range items {
		out = append(out, toApprovalResponse(a))
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"items": out, "enabled": h.approvals.Enabled})
}

func (h *ApprovalHandler) Get(c *gin.Context) {
	a, err := h.approvals.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toApprovalResponse(a))
}
