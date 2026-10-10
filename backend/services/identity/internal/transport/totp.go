package transport

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/authjwt"
	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/identity/internal/usecase"
)

// TOTPHandler serves PW-028 enrollment for the signed-in admin. The secret
// and the recovery codes are answered once and never logged.
type TOTPHandler struct {
	UseCase *usecase.TOTPUseCase
	Log     zerolog.Logger
}

// RegisterTOTP adds /api/auth/mfa routes (admins only, no caching).
func RegisterTOTP(r gin.IRouter, jwt *authjwt.Manager, security Security, h TOTPHandler) {
	g := r.Group("/api/auth/mfa", security.BrowserProtection(), security.RateLimit(), middleware.RequireAuth(jwt), middleware.RequireRole("admin"),
		func(c *gin.Context) { c.Header("Cache-Control", "no-store"); c.Next() })
	g.GET("", h.status)
	g.POST("/totp", h.start)
	g.POST("/totp/confirmations", h.confirm)
}

func (h TOTPHandler) status(c *gin.Context) {
	s, err := h.UseCase.Status(c.Request.Context(), middleware.GetUserID(c))
	if err != nil {
		httpresponse.HandleError(c, h.Log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"enrolled": s.Enrolled, "recovery_codes_left": s.RecoveryCodesLeft, "enrollment_possible": s.EnrollmentPossible})
}

func (h TOTPHandler) start(c *gin.Context) {
	e, err := h.UseCase.Start(c.Request.Context(), middleware.GetUserID(c))
	if err != nil {
		httpresponse.HandleError(c, h.Log, err)
		return
	}
	httpresponse.OK(c, http.StatusCreated, gin.H{"secret": e.Secret, "otpauth_uri": e.URI})
}

func (h TOTPHandler) confirm(c *gin.Context) {
	var req struct {
		Code string `json:"code" binding:"required,max=20"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "code is required")
		return
	}
	codes, err := h.UseCase.Confirm(c.Request.Context(), middleware.GetUserID(c), req.Code)
	if err != nil {
		httpresponse.HandleError(c, h.Log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"recovery_codes": codes})
}
