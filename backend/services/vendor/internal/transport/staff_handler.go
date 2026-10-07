package transport

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/pkg/shopaccess"
	"shopee/backend/services/vendorsvc/internal/domain"
	"shopee/backend/services/vendorsvc/internal/usecase"
)

// StaffHandler serves shop staff (AF-17): invitations, members, the shops
// a person may open, and the internal authorize contract.
type StaffHandler struct {
	staff *usecase.StaffUseCase
	log   zerolog.Logger
}

func NewStaffHandler(staff *usecase.StaffUseCase, log zerolog.Logger) *StaffHandler {
	return &StaffHandler{staff: staff, log: log}
}

type inviteRequest struct {
	Email       string   `json:"email" binding:"required,max=254"`
	Permissions []string `json:"permissions" binding:"required,min=1,max=32"`
	// WarehouseScopes is reserved for multi-warehouse (AF-32).
	WarehouseScopes []string `json:"warehouse_scopes"`
}

type acceptInvitationRequest struct {
	Token string `json:"token" binding:"required,max=128"`
}

type memberChangeRequest struct {
	Permissions     []string `json:"permissions" binding:"required,min=1,max=32"`
	ExpectedVersion int64    `json:"expected_version" binding:"required,min=1"`
	Reason          *string  `json:"reason" binding:"omitempty,max=500"`
}

type memberRemovalRequest struct {
	ExpectedVersion int64   `json:"expected_version" binding:"required,min=1"`
	Reason          *string `json:"reason" binding:"omitempty,max=500"`
}

type invitationRevocationRequest struct {
	Reason *string `json:"reason" binding:"omitempty,max=500"`
}

type authorizeRequest struct {
	ActorUserID string `json:"actor_user_id" binding:"required"`
	VendorID    string `json:"vendor_id" binding:"required"`
	Permission  string `json:"permission" binding:"required,max=64"`
}

type invitationResponse struct {
	ID          string     `json:"id"`
	EmailHint   string     `json:"email_hint"`
	Permissions []string   `json:"permissions"`
	Status      string     `json:"status"`
	Expired     bool       `json:"expired"`
	Delivery    string     `json:"delivery_status"`
	InvitedBy   string     `json:"invited_by"`
	ExpiresAt   time.Time  `json:"expires_at"`
	AcceptedAt  *time.Time `json:"accepted_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

func toInvitationResponse(i *domain.Invitation) invitationResponse {
	return invitationResponse{ID: i.ID, EmailHint: i.EmailHint, Permissions: i.Permissions, Status: i.Status,
		Expired: i.Status == domain.InvitationPending && !i.Usable(time.Now()), Delivery: i.DeliveryStatus, InvitedBy: i.InvitedBy,
		ExpiresAt: i.ExpiresAt, AcceptedAt: i.AcceptedAt, CreatedAt: i.CreatedAt}
}

type memberResponse struct {
	VendorID    string     `json:"vendor_id"`
	UserID      string     `json:"user_id"`
	Email       string     `json:"email,omitempty"`
	Role        string     `json:"role"`
	Status      string     `json:"status"`
	Version     int64      `json:"version"`
	Permissions []string   `json:"permissions"`
	CreatedAt   time.Time  `json:"created_at"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
}

func toMemberResponse(m *domain.Membership, email string) memberResponse {
	return memberResponse{VendorID: m.VendorID, UserID: m.UserID, Email: email, Role: m.Role, Status: m.Status, Version: m.Version,
		Permissions: m.Effective(), CreatedAt: m.CreatedAt, RevokedAt: m.RevokedAt}
}

type accessibleShopResponse struct {
	VendorID          string   `json:"vendor_id"`
	ShopName          string   `json:"shop_name"`
	Status            string   `json:"status"`
	LogoURL           *string  `json:"logo_url,omitempty"`
	Role              string   `json:"role"`
	Capabilities      []string `json:"capabilities"`
	MembershipVersion int64    `json:"membership_version"`
}

// memberShopResponse is the shop as before plus the caller's access.
type memberShopResponse struct {
	vendorResponse
	Role              string   `json:"role"`
	Capabilities      []string `json:"capabilities"`
	MembershipVersion int64    `json:"membership_version"`
}

// Permissions lists the permission registry for the staff screens.
func (h *StaffHandler) Permissions(c *gin.Context) {
	httpresponse.OK(c, http.StatusOK, gin.H{"permissions": shopaccess.Registry(), "enabled": h.staff.Enabled})
}

// AccessibleShops lists the shops the caller may open, with capabilities.
func (h *StaffHandler) AccessibleShops(c *gin.Context) {
	shops, err := h.staff.AccessibleShops(c.Request.Context(), middleware.GetUserID(c))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	out := make([]accessibleShopResponse, 0, len(shops))
	for _, s := range shops {
		out = append(out, accessibleShopResponse{VendorID: s.Vendor.ID, ShopName: s.Vendor.ShopName, Status: string(s.Vendor.Status),
			LogoURL: s.Vendor.LogoURL, Role: s.Membership.Role, Capabilities: s.Membership.Effective(), MembershipVersion: s.Membership.Version})
	}
	c.Header("Cache-Control", "no-store")
	httpresponse.OK(c, http.StatusOK, gin.H{"shops": out, "staff_enabled": h.staff.Enabled})
}

// Shop returns a shop to any active member, with the caller's capabilities.
func (h *StaffHandler) Shop(c *gin.Context) {
	v, m, err := h.staff.GetForMember(c.Request.Context(), middleware.GetUserID(c), c.Param("vendorId"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	httpresponse.OK(c, http.StatusOK, memberShopResponse{vendorResponse: toVendorResponse(v), Role: m.Role, Capabilities: m.Effective(),
		MembershipVersion: m.Version})
}

// Invite creates an invitation. The answer is the same whether or not an
// account exists for the address.
func (h *StaffHandler) Invite(c *gin.Context) {
	var req inviteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "Enter an email and at least one permission")
		return
	}
	if len(req.WarehouseScopes) > 0 {
		httpresponse.HandleError(c, h.log, apperror.Validation("Warehouse scopes need multi-warehouse, which is not enabled"))
		return
	}
	inv, err := h.staff.Invite(c.Request.Context(), middleware.GetUserID(c), c.Param("vendorId"), usecase.InviteInput{Email: req.Email, Permissions: req.Permissions})
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusCreated, toInvitationResponse(inv))
}

func (h *StaffHandler) ListInvitations(c *gin.Context) {
	items, err := h.staff.ListInvitations(c.Request.Context(), middleware.GetUserID(c), c.Param("vendorId"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	out := make([]invitationResponse, 0, len(items))
	for _, i := range items {
		out = append(out, toInvitationResponse(i))
	}
	httpresponse.OK(c, http.StatusOK, out)
}

func (h *StaffHandler) RevokeInvitation(c *gin.Context) {
	var req invitationRevocationRequest
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			httpresponse.Error(c, http.StatusBadRequest, "validation_error", "Reason must be at most 500 characters")
			return
		}
	}
	if err := h.staff.RevokeInvitation(c.Request.Context(), middleware.GetUserID(c), c.Param("vendorId"), c.Param("id"), req.Reason); err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"revoked": true})
}

// Accept joins the caller to a shop. The token comes in the body only; the
// body is neither logged nor echoed.
func (h *StaffHandler) Accept(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Referrer-Policy", "no-referrer")
	var req acceptInvitationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "Invitation token is required")
		return
	}
	m, err := h.staff.AcceptInvitation(c.Request.Context(), middleware.GetUserID(c), req.Token)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toMemberResponse(m, ""))
}

func (h *StaffHandler) ListMembers(c *gin.Context) {
	members, err := h.staff.ListMembers(c.Request.Context(), middleware.GetUserID(c), c.Param("vendorId"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	out := make([]memberResponse, 0, len(members))
	for _, m := range members {
		out = append(out, toMemberResponse(m.Membership, m.Email))
	}
	c.Header("Cache-Control", "no-store")
	httpresponse.OK(c, http.StatusOK, out)
}

func (h *StaffHandler) GetMember(c *gin.Context) {
	m, err := h.staff.GetMember(c.Request.Context(), middleware.GetUserID(c), c.Param("vendorId"), c.Param("userID"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toMemberResponse(m, ""))
}

func (h *StaffHandler) UpdateMember(c *gin.Context) {
	var req memberChangeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "Permissions and expected_version are required")
		return
	}
	m, err := h.staff.UpdateMember(c.Request.Context(), middleware.GetUserID(c), c.Param("vendorId"), c.Param("userID"),
		usecase.MemberChange{Permissions: req.Permissions, ExpectedVersion: req.ExpectedVersion, Reason: req.Reason})
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toMemberResponse(m, ""))
}

func (h *StaffHandler) RemoveMember(c *gin.Context) {
	var req memberRemovalRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "expected_version is required")
		return
	}
	if err := h.staff.RemoveMember(c.Request.Context(), middleware.GetUserID(c), c.Param("vendorId"), c.Param("userID"), req.ExpectedVersion, req.Reason); err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"removed": true})
}

// Authorize is POST /internal/vendors/authorize. It always answers 200
// with allowed true/false for a well-formed question (never 404, which an
// older caller reads as "contract missing"), 400 for a malformed one and
// 5xx when the answer is unknown.
func (h *StaffHandler) Authorize(c *gin.Context) {
	var req authorizeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "actor_user_id, vendor_id and permission are required")
		return
	}
	res, err := h.staff.Authorize(c.Request.Context(), req.ActorUserID, req.VendorID, req.Permission)
	if err != nil {
		if app, ok := err.(*apperror.Error); ok && app.Status < 500 {
			httpresponse.HandleError(c, h.log, err)
			return
		}
		h.log.Warn().Str("vendor_id", req.VendorID).Str("permission", req.Permission).Msg("shop_authorization_unavailable")
		httpresponse.HandleError(c, h.log, shopaccess.Unavailable(err))
		return
	}
	if !res.Allowed {
		h.log.Info().Str("vendor_id", req.VendorID).Str("actor_user_id", req.ActorUserID).Str("permission", req.Permission).
			Str("caller", c.GetHeader("X-Service-Name")).Msg("shop_permission_denied")
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"allowed": res.Allowed, "vendor_id": res.VendorID, "status": res.Status,
		"vendor_version": res.VendorVersion, "role": res.Role, "membership_version": res.MembershipVersion})
}
