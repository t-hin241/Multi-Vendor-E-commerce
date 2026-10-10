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

// RegisterEmailVerification adds the PW-022 routes under /api/auth with
// the same browser protection and rate limit as the other auth routes.
func RegisterEmailVerification(r gin.IRouter, jwt *authjwt.Manager, security Security, h EmailVerificationHandler) {
	g := r.Group("/api/auth/email-verifications", security.BrowserProtection(), security.RateLimit())
	g.POST("", middleware.RequireAuth(jwt), h.Resend)
	g.POST("/confirmations", h.Confirm)
}

// EmailVerificationHandler serves PW-022: resend a link to the signed-in
// account, and confirm a link (the token in the body, never in a URL).
type EmailVerificationHandler struct {
	UseCase *usecase.EmailVerificationUseCase
	Log     zerolog.Logger
}

func (h EmailVerificationHandler) Resend(c *gin.Context) {
	if err := h.UseCase.Resend(c.Request.Context(), middleware.GetUserID(c)); err != nil {
		httpresponse.HandleError(c, h.Log, err)
		return
	}
	httpresponse.OK(c, http.StatusAccepted, gin.H{"sent": true})
}

func (h EmailVerificationHandler) Confirm(c *gin.Context) {
	var req struct {
		Token string `json:"token" binding:"required,max=200"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "token is required")
		return
	}
	if err := h.UseCase.Confirm(c.Request.Context(), req.Token); err != nil {
		httpresponse.HandleError(c, h.Log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"verified": true})
}
