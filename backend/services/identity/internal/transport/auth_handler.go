package transport

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/identity/internal/usecase"
)

type AuthHandler struct {
	auth   *usecase.AuthUseCase
	log    zerolog.Logger
	secure bool
}

func NewAuthHandler(auth *usecase.AuthUseCase, log zerolog.Logger, secure bool) *AuthHandler {
	return &AuthHandler{auth: auth, log: log, secure: secure}
}

func (h *AuthHandler) Register(c *gin.Context) {
	var req registerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	result, err := h.auth.Register(c.Request.Context(), req.Email, req.Password, req.FullName, req.Role)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	h.setSession(c, result)
	httpresponse.OK(c, http.StatusCreated, toAuthResponse(result))
}

func (h *AuthHandler) Login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	result, err := h.auth.Login(c.Request.Context(), req.Email, req.Password)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	h.setSession(c, result)
	httpresponse.OK(c, http.StatusOK, toAuthResponse(result))
}

func (h *AuthHandler) Refresh(c *gin.Context) {
	token, err := c.Cookie("shopee_refresh")
	if err != nil || token == "" {
		httpresponse.Error(c, 401, "unauthorized", "Session expired. Please sign in again.")
		return
	}

	result, err := h.auth.RefreshToken(c.Request.Context(), token)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	h.setSession(c, result)
	httpresponse.OK(c, http.StatusOK, toAuthResponse(result))
}

func (h *AuthHandler) Logout(c *gin.Context) {
	token, err := c.Cookie("shopee_refresh")
	if err != nil || token == "" {
		httpresponse.Error(c, 401, "unauthorized", "Session expired. Please sign in again.")
		return
	}

	if err := h.auth.Logout(c.Request.Context(), token); err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	h.clearSession(c)
	httpresponse.OK(c, http.StatusOK, gin.H{"logged_out": true})
}

func (h *AuthHandler) RequestPasswordReset(c *gin.Context) {
	var req passwordResetRequestBody
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	if err := h.auth.RequestPasswordReset(c.Request.Context(), req.Email); err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, gin.H{"message": "If that email is registered, a reset link has been sent."})
}

func (h *AuthHandler) ConfirmPasswordReset(c *gin.Context) {
	var req passwordResetConfirmBody
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	if err := h.auth.ResetPassword(c.Request.Context(), req.Token, req.NewPassword); err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, gin.H{"message": "Password has been reset."})
}

func (h *AuthHandler) Me(c *gin.Context) {
	userID := middleware.GetUserID(c)

	user, err := h.auth.Me(c.Request.Context(), userID)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toUserResponse(user))
}

func (h *AuthHandler) setSession(c *gin.Context, result *usecase.AuthResult) {
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie("shopee_refresh", result.RefreshToken, int(time.Until(result.RefreshTokenExpiresAt).Seconds()), "/api/auth", "", h.secure, true)
	c.Header("Cache-Control", "no-store")
}
func (h *AuthHandler) clearSession(c *gin.Context) {
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie("shopee_refresh", "", -1, "/api/auth", "", h.secure, true)
}
