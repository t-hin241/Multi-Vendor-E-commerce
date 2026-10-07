package transport

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/adminaccess"
	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/identity/internal/domain"
	"shopee/backend/services/identity/internal/usecase"
)

// AccessHandler serves scoped admin permissions and reauthentication
// (AF-19).
type AccessHandler struct {
	access *usecase.AccessUseCase
	log    zerolog.Logger
}

func NewAccessHandler(access *usecase.AccessUseCase, log zerolog.Logger) *AccessHandler {
	return &AccessHandler{access: access, log: log}
}

type grantResponse struct {
	ID           string     `json:"id"`
	UserID       string     `json:"user_id"`
	Bundle       string     `json:"bundle"`
	Status       string     `json:"status"`
	GrantedBy    *string    `json:"granted_by,omitempty"`
	Reason       string     `json:"reason"`
	CreatedAt    time.Time  `json:"created_at"`
	RevokedBy    *string    `json:"revoked_by,omitempty"`
	RevokeReason *string    `json:"revoke_reason,omitempty"`
	RevokedAt    *time.Time `json:"revoked_at,omitempty"`
}

func toGrantResponse(g *domain.PermissionGrant) grantResponse {
	return grantResponse{ID: g.ID, UserID: g.UserID, Bundle: g.Bundle, Status: g.Status, GrantedBy: g.GrantedBy, Reason: g.Reason,
		CreatedAt: g.CreatedAt, RevokedBy: g.RevokedBy, RevokeReason: g.RevokeReason, RevokedAt: g.RevokedAt}
}

// Mine returns the caller's effective bundles (for the admin console).
func (h *AccessHandler) Mine(c *gin.Context) {
	held, version, err := h.access.Effective(c.Request.Context(), middleware.GetUserID(c))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	if held == nil {
		held = []string{}
	}
	c.Header("Cache-Control", "no-store")
	httpresponse.OK(c, http.StatusOK, gin.H{"permissions": held, "permission_version": version, "scoped": h.access.Scoped,
		"bundles": adminaccess.Bundles()})
}

// Subjects lists admins with their active grants.
func (h *AccessHandler) Subjects(c *gin.Context) {
	items, err := h.access.ListSubjects(c.Request.Context(), middleware.GetUserID(c))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	out := make([]gin.H, 0, len(items))
	for _, it := range items {
		grants := make([]grantResponse, 0, len(it.Grants))
		for _, g := range it.Grants {
			grants = append(grants, toGrantResponse(g))
		}
		out = append(out, gin.H{"user": toAdminUserResponse(it.User), "grants": grants})
	}
	httpresponse.OK(c, http.StatusOK, out)
}

func (h *AccessHandler) ListGrants(c *gin.Context) {
	items, err := h.access.ListGrants(c.Request.Context(), middleware.GetUserID(c), c.Query("user_id"), c.Query("include_revoked") == "true")
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	out := make([]grantResponse, 0, len(items))
	for _, g := range items {
		out = append(out, toGrantResponse(g))
	}
	httpresponse.OK(c, http.StatusOK, out)
}

type grantRequest struct {
	SubjectID       string `json:"subject_id" binding:"required,uuid"`
	Bundle          string `json:"bundle" binding:"required,max=64"`
	Reason          string `json:"reason" binding:"required,max=500"`
	ExpectedVersion *int64 `json:"expected_version" binding:"required,min=0"`
}

func (h *AccessHandler) Grant(c *gin.Context) {
	var req grantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "subject_id, bundle, reason and expected_version are required")
		return
	}
	g, err := h.access.Grant(c.Request.Context(), middleware.GetUserID(c), req.SubjectID, req.Bundle, req.Reason, *req.ExpectedVersion)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusCreated, toGrantResponse(g))
}

type revokeGrantRequest struct {
	Reason          string `json:"reason" binding:"required,max=500"`
	ExpectedVersion *int64 `json:"expected_version" binding:"required,min=0"`
}

func (h *AccessHandler) Revoke(c *gin.Context) {
	var req revokeGrantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "reason and expected_version are required")
		return
	}
	if err := h.access.Revoke(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), req.Reason, *req.ExpectedVersion); err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"revoked": true})
}

type reauthRequest struct {
	Password      string `json:"password" binding:"required,max=200"`
	Purpose       string `json:"purpose" binding:"required,max=64"`
	OperationHash string `json:"operation_hash" binding:"required,max=200"`
}

// Reauthenticate re-checks the password and returns a one-time proof.
// Neither the body nor the proof is logged; the answer is never cached.
func (h *AccessHandler) Reauthenticate(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var req reauthRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "password, purpose and operation_hash are required")
		return
	}
	proof, err := h.access.Reauthenticate(c.Request.Context(), middleware.GetUserID(c), req.Password, req.Purpose, req.OperationHash)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusCreated, gin.H{"proof": proof.Value, "expires_at": proof.ExpiresAt})
}

// Check is POST /internal/admin-permissions/check.
func (h *AccessHandler) Check(c *gin.Context) {
	var req struct {
		UserID     string `json:"user_id" binding:"required"`
		Permission string `json:"permission" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "user_id and permission are required")
		return
	}
	ok, version, err := h.access.Check(c.Request.Context(), req.UserID, req.Permission)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"allowed": ok, "user_id": req.UserID, "permission_version": version})
}

// ConsumeProof is POST /internal/reauth-proofs/consume.
func (h *AccessHandler) ConsumeProof(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var req struct {
		Proof         string `json:"proof" binding:"required,max=128"`
		UserID        string `json:"user_id" binding:"required,uuid"`
		Purpose       string `json:"purpose" binding:"required,max=64"`
		OperationHash string `json:"operation_hash" binding:"required,max=200"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	if err := h.access.ConsumeProof(c.Request.Context(), req.Proof, req.UserID, req.Purpose, req.OperationHash); err != nil {
		h.log.Info().Str("user_id", req.UserID).Str("purpose", req.Purpose).Msg("reauth_proof_refused")
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"consumed": true})
}
