package transport

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/serviceauth"
	"shopee/backend/services/notification/internal/domain"
	"shopee/backend/services/notification/internal/sender"
)

// StaffInvitationSender sends one invitation email synchronously.
type StaffInvitationSender interface {
	SendInvitation(context.Context, domain.StaffInvitation) error
}

// RegisterStaffInvitation serves Vendor's invitation delivery call
// (AF-17). The email is sent before answering and nothing is stored; a
// failure is logged with the delivery id and a short reason only (never
// the address or the link), and Vendor retries with a fresh token.
func RegisterStaffInvitation(r *gin.Engine, internal *serviceauth.Verifier, s StaffInvitationSender, log zerolog.Logger) {
	r.POST("/internal/notifications/staff-invitations", internal.Allow("vendor"), func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
		var input struct {
			ID        string    `json:"delivery_id"`
			Email     string    `json:"email"`
			ShopName  string    `json:"shop_name"`
			URL       string    `json:"url"`
			ExpiresAt time.Time `json:"expires_at"`
		}
		if c.ShouldBindJSON(&input) != nil {
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}
		if _, err := uuid.Parse(input.ID); err != nil || input.Email == "" || len(input.Email) > 254 || input.ShopName == "" ||
			len(input.ShopName) > 200 || !validInvitationLink(input.URL) || input.ExpiresAt.IsZero() {
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
		defer cancel()
		err := s.SendInvitation(ctx, domain.StaffInvitation{Email: input.Email, ShopName: input.ShopName, URL: input.URL, ExpiresAt: input.ExpiresAt})
		if err != nil {
			log.Warn().Str("delivery_id", input.ID).Str("reason", sender.Classify(err).Reason).Msg("staff_invitation_delivery_failed")
			c.AbortWithStatus(http.StatusServiceUnavailable)
			return
		}
		c.Status(http.StatusNoContent)
	})
}

// validInvitationLink: an absolute http(s) link whose token is in the
// fragment, never in the query.
func validInvitationLink(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.User == nil && u.RawQuery == "" &&
		strings.HasPrefix(u.Fragment, "token=") && len(raw) <= 1024
}
