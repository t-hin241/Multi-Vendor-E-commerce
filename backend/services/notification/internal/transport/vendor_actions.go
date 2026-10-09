package transport

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/authjwt"
	"shopee/backend/pkg/eventbus"
	"shopee/backend/pkg/events"
	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/pkg/serviceauth"
	"shopee/backend/services/notification/internal/domain"
	"shopee/backend/services/notification/internal/repository"
	"shopee/backend/services/notification/internal/usecase"
)

// VendorActionHandler records order.vendor_action_required and
// payment.vendor_action_required (AF-08) under the producer's event id; a
// redelivered event is a duplicate. Recording only touches PostgreSQL.
func VendorActionHandler(uc *usecase.VendorActionUseCase) eventbus.Handler {
	return func(ctx context.Context, _ pgx.Tx, env eventbus.Envelope) error {
		req := domain.VendorActionRequest{Source: env.Producer, EventID: env.EventID, CorrelationID: env.CorrelationID}
		switch env.Type {
		case events.OrderVendorActionRequired:
			var a events.VendorOrderAction
			if err := env.Decode(&a); err != nil {
				return err
			}
			req.VendorID, req.ActionKind, req.ReferenceID, req.VendorOrderID = a.VendorID, a.ActionKind, a.ReferenceID, a.VendorOrderID
		case events.PaymentVendorActionRequired:
			var a events.VendorPayoutAction
			if err := env.Decode(&a); err != nil {
				return err
			}
			req.VendorID, req.ActionKind, req.ReferenceID = a.VendorID, "payout_"+a.Outcome, a.PayoutID
		}
		_, _, err := uc.Record(ctx, req)
		return err
	}
}

// vendorActionRequest is the HTTP form of the two events, for producers
// in EVENT_PUBLISHING=http mode. The source is the authenticated caller.
type vendorActionRequest struct {
	EventID       string `json:"event_id" binding:"required,max=100"`
	Source        string `json:"source" binding:"max=30"`
	VendorID      string `json:"vendor_id" binding:"required"`
	VendorOrderID string `json:"vendor_order_id"`
	ActionKind    string `json:"action_kind" binding:"max=40"`
	ReferenceID   string `json:"reference_id" binding:"max=100"`
	PayoutID      string `json:"payout_id" binding:"max=100"`
	Outcome       string `json:"outcome" binding:"max=20"`
	CorrelationID string `json:"correlation_id" binding:"max=64"`
}

type preferencesRequest struct {
	OptionalVendorCategories []string `json:"optional_vendor_categories" binding:"required,max=10"`
	ExpectedVersion          int64    `json:"expected_version"`
}

type vendorActionResponse struct {
	ID                string     `json:"id"`
	Source            string     `json:"source"`
	EventID           string     `json:"event_id"`
	VendorID          string     `json:"vendor_id"`
	ActionKind        string     `json:"action_kind"`
	Purpose           string     `json:"purpose"`
	ReferenceID       string     `json:"reference_id"`
	VendorOrderID     *string    `json:"vendor_order_id,omitempty"`
	Status            string     `json:"status"`
	Attempts          int        `json:"attempts"`
	NextAttemptAt     time.Time  `json:"next_attempt_at"`
	LastError         *string    `json:"last_error,omitempty"`
	Recipients        int        `json:"recipients"`
	PermissionVersion *string    `json:"permission_version,omitempty"`
	ResolvedAt        *time.Time `json:"resolved_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// toVendorActionResponse gives the admin the count of recipients, not who
// they are: user ids stay in the database and in the notices themselves.
func toVendorActionResponse(a *domain.VendorAction) vendorActionResponse {
	return vendorActionResponse{ID: a.ID, Source: a.Source, EventID: a.EventID, VendorID: a.VendorID, ActionKind: a.ActionKind, Purpose: a.Purpose,
		ReferenceID: a.ReferenceID, VendorOrderID: a.VendorOrderID, Status: string(a.Status), Attempts: a.Attempts, NextAttemptAt: a.NextAttemptAt,
		LastError: a.LastError, Recipients: len(a.RecipientUserIDs), PermissionVersion: a.PermissionVersion, ResolvedAt: a.ResolvedAt,
		CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt}
}

// VendorActionRoutes serves the AF-08 HTTP contract.
type VendorActionRoutes struct {
	UseCase *usecase.VendorActionUseCase
	Log     zerolog.Logger
}

// RegisterVendorActions adds the internal report route (Order, Payment),
// the person's own preferences and the admin review of events.
func RegisterVendorActions(r gin.IRoutes, jwt *authjwt.Manager, internal *serviceauth.Verifier, adminGuard gin.HandlerFunc, h VendorActionRoutes) {
	r.POST("/internal/vendor-action-notices", internal.Allow("order", "payment"), h.report)
	auth := middleware.RequireAuth(jwt)
	noStore := func(c *gin.Context) { c.Header("Cache-Control", "no-store"); c.Next() }
	r.GET("/api/notifications/preferences", auth, noStore, h.preferences)
	r.PATCH("/api/notifications/preferences", auth, noStore, h.updatePreferences)
	admin := []gin.HandlerFunc{auth, middleware.RequireRole("admin"), adminGuard, noStore}
	r.GET("/api/notifications/admin/vendor-actions", append(admin, h.list)...)
	r.GET("/api/notifications/admin/vendor-actions/summary", append(admin, h.summary)...)
	r.POST("/api/notifications/admin/vendor-actions/:id/retry", append(admin, h.retry)...)
}

func (h VendorActionRoutes) report(c *gin.Context) {
	var req vendorActionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "event_id and vendor_id are required")
		return
	}
	source := c.GetString("service_caller")
	if source == "shared-key" {
		source = req.Source // legacy shared key: the caller names itself
	}
	in := domain.VendorActionRequest{Source: source, EventID: req.EventID, VendorID: req.VendorID, ActionKind: req.ActionKind,
		ReferenceID: req.ReferenceID, VendorOrderID: req.VendorOrderID, CorrelationID: req.CorrelationID}
	if source == "payment" {
		in.ActionKind, in.ReferenceID = "payout_"+req.Outcome, req.PayoutID
	}
	if in.CorrelationID == "" {
		in.CorrelationID = middleware.GetRequestID(c)
	}
	a, duplicate, err := h.UseCase.Record(c.Request.Context(), in)
	if err != nil {
		httpresponse.HandleError(c, h.Log, err)
		return
	}
	httpresponse.OK(c, http.StatusAccepted, gin.H{"id": a.ID, "status": a.Status, "duplicate": duplicate})
}

func preferencesBody(p *usecase.Preferences) gin.H {
	return gin.H{"optional_vendor_categories": p.Optional, "available_vendor_categories": domain.VendorCategories,
		"version": p.Version, "owner_receives_all": true}
}

func (h VendorActionRoutes) preferences(c *gin.Context) {
	p, err := h.UseCase.GetPreferences(c.Request.Context(), middleware.GetUserID(c))
	if err != nil {
		httpresponse.HandleError(c, h.Log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, preferencesBody(p))
}

func (h VendorActionRoutes) updatePreferences(c *gin.Context) {
	var req preferencesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "optional_vendor_categories is required")
		return
	}
	p, err := h.UseCase.UpdatePreferences(c.Request.Context(), middleware.GetUserID(c), req.OptionalVendorCategories, req.ExpectedVersion)
	if err != nil {
		httpresponse.HandleError(c, h.Log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, preferencesBody(p))
}

func (h VendorActionRoutes) list(c *gin.Context) {
	limit := parseIntDefault(c.Query("limit"), 20, 1, 100)
	offset := parseIntDefault(c.Query("offset"), 0, 0, 1_000_000)
	items, total, err := h.UseCase.List(c.Request.Context(), repository.VendorActionFilter{Status: c.Query("status"), VendorID: c.Query("vendor_id")}, limit, offset)
	if err != nil {
		httpresponse.HandleError(c, h.Log, err)
		return
	}
	out := make([]vendorActionResponse, 0, len(items))
	for _, a := range items {
		out = append(out, toVendorActionResponse(a))
	}
	c.Header("X-Total-Count", strconv.FormatInt(total, 10))
	httpresponse.OK(c, http.StatusOK, out)
}

func (h VendorActionRoutes) summary(c *gin.Context) {
	counts, err := h.UseCase.Counts(c.Request.Context(), middleware.GetUserID(c))
	if err != nil {
		httpresponse.HandleError(c, h.Log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"counts": counts})
}

func (h VendorActionRoutes) retry(c *gin.Context) {
	var req retryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "A reason of at most 500 characters is required")
		return
	}
	a, err := h.UseCase.Retry(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), req.Reason)
	if err != nil {
		httpresponse.HandleError(c, h.Log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toVendorActionResponse(a))
}
