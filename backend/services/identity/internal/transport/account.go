package transport

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"shopee/backend/pkg/authjwt"
	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/identity/internal/domain"
	"shopee/backend/services/identity/internal/usecase"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
)

type AccountHandler struct {
	UseCase *usecase.AccountUseCase
	Log     zerolog.Logger
	Secure  bool
}

func RegisterAccount(r gin.IRouter, jwt *authjwt.Manager, security Security, h AccountHandler) {
	g := r.Group("/api/auth", security.BrowserProtection(), security.RateLimit())
	g.GET("/account-capabilities", middleware.RequireAuth(jwt), func(c *gin.Context) { httpresponse.OK(c, 200, gin.H{"account_self_service": h.UseCase.Enabled}) })
	g.POST("/email-changes/confirm", h.ConfirmEmailChange)
	self := g.Group("", middleware.RequireAuth(jwt))
	self.PATCH("/me", h.Profile)
	self.POST("/password-changes", h.Password)
	self.POST("/email-changes", h.EmailChange)
	self.GET("/sessions", h.Sessions)
	self.DELETE("/sessions/:id", h.Revoke)
	self.POST("/session-revocations", h.RevokeAll)
}

func accountActor(c *gin.Context) usecase.AccountActor {
	return usecase.AccountActor{UserID: middleware.GetUserID(c), Role: middleware.GetRole(c), SessionID: c.GetString(middleware.ContextKeySessionID)}
}
func accountBody(c *gin.Context, body any) bool {
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(body); err != nil {
		httpresponse.Error(c, 400, "validation_error", "Invalid request fields")
		return false
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		httpresponse.Error(c, 400, "validation_error", "Expected one JSON object")
		return false
	}
	return true
}
func (h AccountHandler) fail(c *gin.Context, err error) bool {
	if err != nil {
		httpresponse.HandleError(c, h.Log, err)
		return true
	}
	return false
}
func (h AccountHandler) clear(c *gin.Context) {
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie("shopee_refresh", "", -1, "/api/auth", "", h.Secure, true)
}
func (h AccountHandler) Profile(c *gin.Context) {
	var req domain.ProfileUpdate
	if !accountBody(c, &req) {
		return
	}
	user, err := h.UseCase.Profile(c.Request.Context(), accountActor(c), req)
	if h.fail(c, err) {
		return
	}
	httpresponse.OK(c, 200, toUserResponse(user))
}
func (h AccountHandler) Password(c *gin.Context) {
	var req struct {
		Current string `json:"current_password"`
		Next    string `json:"new_password"`
	}
	if !accountBody(c, &req) {
		return
	}
	if h.fail(c, h.UseCase.ChangePassword(c.Request.Context(), accountActor(c), req.Current, req.Next)) {
		return
	}
	h.clear(c)
	httpresponse.OK(c, 200, gin.H{"sign_in_required": true})
}
func (h AccountHandler) EmailChange(c *gin.Context) {
	var req struct {
		Email    string `json:"new_email"`
		Password string `json:"current_password"`
	}
	if !accountBody(c, &req) {
		return
	}
	if h.fail(c, h.UseCase.RequestEmailChange(c.Request.Context(), accountActor(c), req.Email, req.Password)) {
		return
	}
	httpresponse.OK(c, 202, gin.H{"sent": true})
}
func (h AccountHandler) ConfirmEmailChange(c *gin.Context) {
	var req struct {
		Token string `json:"token"`
	}
	if !accountBody(c, &req) {
		return
	}
	if h.fail(c, h.UseCase.ConfirmEmailChange(c.Request.Context(), req.Token)) {
		return
	}
	h.clear(c)
	httpresponse.OK(c, 200, gin.H{"sign_in_required": true})
}
func (h AccountHandler) Sessions(c *gin.Context) {
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if err != nil {
		httpresponse.Error(c, 422, "validation_error", "limit: must be an integer")
		return
	}
	page, err := h.UseCase.Sessions(c.Request.Context(), accountActor(c), c.Query("cursor"), limit)
	if h.fail(c, err) {
		return
	}
	httpresponse.OK(c, 200, page)
}
func (h AccountHandler) Revoke(c *gin.Context) {
	a := accountActor(c)
	if h.fail(c, h.UseCase.Revoke(c.Request.Context(), a, c.Param("id"), false)) {
		return
	}
	current := a.SessionID == c.Param("id")
	if current {
		h.clear(c)
	}
	httpresponse.OK(c, 200, gin.H{"sign_in_required": current})
}
func (h AccountHandler) RevokeAll(c *gin.Context) {
	var req struct {
		Scope string `json:"scope"`
	}
	if !accountBody(c, &req) {
		return
	}
	if req.Scope != "all" {
		httpresponse.Error(c, 422, "validation_error", "scope: must be all")
		return
	}
	if h.fail(c, h.UseCase.Revoke(c.Request.Context(), accountActor(c), "", true)) {
		return
	}
	h.clear(c)
	httpresponse.OK(c, 200, gin.H{"sign_in_required": true})
}
