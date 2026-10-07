package transport

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/vendorsvc/internal/domain"
	"shopee/backend/services/vendorsvc/internal/usecase"
)

// PolicyHandler serves versioned policies: public reading, admin drafting
// and publishing, shop proposals and their review.
type PolicyHandler struct {
	policies *usecase.PolicyUseCase
	vendors  *usecase.VendorUseCase
	log      zerolog.Logger
}

func NewPolicyHandler(policies *usecase.PolicyUseCase, vendors *usecase.VendorUseCase, log zerolog.Logger) *PolicyHandler {
	return &PolicyHandler{policies: policies, vendors: vendors, log: log}
}

type createPolicyRequest struct {
	Kind        string            `json:"kind" binding:"required"`
	Title       string            `json:"title" binding:"required"`
	Summary     string            `json:"summary" binding:"required"`
	Content     string            `json:"content" binding:"required"`
	Contact     string            `json:"contact" binding:"required"`
	RuleRefs    map[string]string `json:"rule_refs" binding:"max=10"`
	EffectiveAt time.Time         `json:"effective_at" binding:"required"`
}

type policyDecisionRequest struct {
	ExpectedVersion int64  `json:"expected_version" binding:"required,min=1"`
	Reason          string `json:"reason" binding:"required,max=500"`
}

type shopPolicyProposalRequest struct {
	Content string `json:"content" binding:"required"`
}

type shopPolicyDecisionRequest struct {
	Approve bool   `json:"approve"`
	Reason  string `json:"reason" binding:"max=500"`
}

type policyResponse struct {
	ID                string                          `json:"id"`
	Kind              string                          `json:"kind"`
	Version           int                             `json:"version"`
	Title             string                          `json:"title"`
	Summary           string                          `json:"summary"`
	Content           string                          `json:"content"`
	Contact           string                          `json:"contact"`
	RuleRefs          map[string]string               `json:"rule_refs"`
	EffectiveAt       time.Time                       `json:"effective_at"`
	ContentHash       string                          `json:"content_hash"`
	Status            string                          `json:"status,omitempty"`
	Readiness         map[string]domain.RuleReadiness `json:"readiness,omitempty"`
	PublicationReason *string                         `json:"publication_reason,omitempty"`
	PublishedAt       *time.Time                      `json:"published_at,omitempty"`
	RowVersion        int64                           `json:"row_version,omitempty"`
	CreatedAt         *time.Time                      `json:"created_at,omitempty"`
}

// toPolicyResponse: the admin view carries the workflow fields; the public
// view only what a reader needs.
func toPolicyResponse(p *domain.MarketplacePolicy, admin bool) policyResponse {
	out := policyResponse{ID: p.ID, Kind: string(p.Kind), Version: p.Version, Title: p.Title, Summary: p.Summary, Content: p.Content,
		Contact: p.Contact, RuleRefs: p.RuleRefs, EffectiveAt: p.EffectiveAt, ContentHash: p.ContentHash, PublishedAt: p.PublishedAt}
	if admin {
		created := p.CreatedAt
		out.Status, out.Readiness, out.PublicationReason, out.RowVersion, out.CreatedAt = string(p.Status), p.Readiness, p.PublicationReason, p.RowVersion, &created
	}
	return out
}

func toPolicyResponses(items []*domain.MarketplacePolicy, admin bool) []policyResponse {
	out := make([]policyResponse, 0, len(items))
	for _, p := range items {
		out = append(out, toPolicyResponse(p, admin))
	}
	return out
}

type shopPolicyResponse struct {
	ID             string     `json:"id"`
	VendorID       string     `json:"vendor_id"`
	Version        int        `json:"version"`
	Content        string     `json:"content"`
	ContentHash    string     `json:"content_hash"`
	Status         string     `json:"status"`
	Source         string     `json:"source"`
	DecisionReason *string    `json:"decision_reason,omitempty"`
	DecidedAt      *time.Time `json:"decided_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

func toShopPolicyResponse(p *domain.ShopPolicy) shopPolicyResponse {
	return shopPolicyResponse{ID: p.ID, VendorID: p.VendorID, Version: p.Version, Content: p.Content, ContentHash: p.ContentHash,
		Status: string(p.Status), Source: p.Source, DecisionReason: p.DecisionReason, DecidedAt: p.DecidedAt, CreatedAt: p.CreatedAt}
}

func toShopPolicyResponses(items []*domain.ShopPolicy) []shopPolicyResponse {
	out := make([]shopPolicyResponse, 0, len(items))
	for _, p := range items {
		out = append(out, toShopPolicyResponse(p))
	}
	return out
}

// Public answers GET /api/vendor/public/policies[?kind=]: every policy in
// force, or one kind. Shared caches may keep it briefly; the version and
// hash in the body tell which text a page showed.
func (h *PolicyHandler) Public(c *gin.Context) {
	items, err := h.policies.ActivePolicies(c.Request.Context())
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	c.Header("Cache-Control", "public, max-age=60")
	if kind := c.Query("kind"); kind != "" {
		if _, err := domain.ParsePolicyKind(kind); err != nil {
			httpresponse.HandleError(c, h.log, err)
			return
		}
		for _, p := range items {
			if string(p.Kind) == kind {
				httpresponse.OK(c, http.StatusOK, toPolicyResponse(p, false))
				return
			}
		}
		httpresponse.Error(c, http.StatusNotFound, "not_found", "This policy is not published yet")
		return
	}
	httpresponse.OK(c, http.StatusOK, toPolicyResponses(items, false))
}

func (h *PolicyHandler) History(c *gin.Context) {
	items, err := h.policies.PolicyHistory(c.Request.Context(), c.Param("kind"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	c.Header("Cache-Control", "public, max-age=60")
	httpresponse.OK(c, http.StatusOK, toPolicyResponses(items, false))
}

// Version serves one published version; it never changes, so it may be
// cached for long.
func (h *PolicyHandler) Version(c *gin.Context) {
	version, err := strconv.Atoi(c.Param("version"))
	if err != nil || version < 1 {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "Invalid version")
		return
	}
	p, err := h.policies.PolicyVersion(c.Request.Context(), c.Param("kind"), version)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	c.Header("Cache-Control", "public, max-age=86400, immutable")
	httpresponse.OK(c, http.StatusOK, toPolicyResponse(p, false))
}

// PublicShop is the public shop page: with versioned policies on, the
// shop policy shown is the approved version, never unreviewed text.
func (h *PolicyHandler) PublicShop(c *gin.Context) {
	v, err := h.vendors.GetPublicProfile(c.Request.Context(), c.Param("vendorId"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	out := toPublicVendorResponse(v)
	if h.policies.Enabled {
		out.PolicyText = ""
		approved, err := h.policies.PublicShopPolicy(c.Request.Context(), v.ID)
		if err != nil {
			httpresponse.HandleError(c, h.log, err)
			return
		}
		if approved != nil {
			out.PolicyText, out.ShopPolicyVersion = approved.Content, &approved.Version
		}
	}
	httpresponse.OK(c, http.StatusOK, out)
}

func (h *PolicyHandler) AdminList(c *gin.Context) {
	items, err := h.policies.ListMarketplace(c.Request.Context(), middleware.GetUserID(c), c.Query("kind"), c.Query("status"),
		parseIntDefault(c.Query("limit"), 50, 1, 100), parseIntDefault(c.Query("offset"), 0, 0, 1_000_000))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toPolicyResponses(items, true))
}

func (h *PolicyHandler) CreateDraft(c *gin.Context) {
	var req createPolicyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "kind, title, summary, content, contact and effective_at (RFC3339) are required")
		return
	}
	p, err := h.policies.CreateDraft(c.Request.Context(), middleware.GetUserID(c), domain.PolicyInput{Kind: req.Kind, Title: req.Title,
		Summary: req.Summary, Content: req.Content, Contact: req.Contact, RuleRefs: req.RuleRefs, EffectiveAt: req.EffectiveAt})
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusCreated, toPolicyResponse(p, true))
}

// Publish answers 200 when published, 202 while rule owners have not all
// acknowledged (the previous version stays public).
func (h *PolicyHandler) Publish(c *gin.Context) {
	var req policyDecisionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "expected_version and a reason of at most 500 characters are required")
		return
	}
	p, published, err := h.policies.Publish(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), req.ExpectedVersion, req.Reason)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	status := http.StatusOK
	if !published {
		status = http.StatusAccepted
	}
	httpresponse.OK(c, status, toPolicyResponse(p, true))
}

func (h *PolicyHandler) Withdraw(c *gin.Context) {
	var req policyDecisionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "expected_version and a reason of at most 500 characters are required")
		return
	}
	p, err := h.policies.Withdraw(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), req.ExpectedVersion, req.Reason)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toPolicyResponse(p, true))
}

func (h *PolicyHandler) Propose(c *gin.Context) {
	var req shopPolicyProposalRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "content is required")
		return
	}
	p, err := h.policies.ProposeShopPolicy(c.Request.Context(), middleware.GetUserID(c), c.Param("vendorId"), req.Content)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusCreated, toShopPolicyResponse(p))
}

func (h *PolicyHandler) ListMine(c *gin.Context) {
	items, err := h.policies.ListShopPolicies(c.Request.Context(), middleware.GetUserID(c), c.Param("vendorId"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toShopPolicyResponses(items))
}

func (h *PolicyHandler) AdminProposals(c *gin.Context) {
	items, err := h.policies.ListShopProposals(c.Request.Context(), middleware.GetUserID(c), c.DefaultQuery("status", "proposed"),
		parseIntDefault(c.Query("limit"), 50, 1, 100), parseIntDefault(c.Query("offset"), 0, 0, 1_000_000))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toShopPolicyResponses(items))
}

func (h *PolicyHandler) Decide(c *gin.Context) {
	var req shopPolicyDecisionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "reason must be at most 500 characters")
		return
	}
	p, err := h.policies.DecideShopPolicy(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), req.Approve, req.Reason)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toShopPolicyResponse(p))
}
