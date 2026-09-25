package transport

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/services/notification/internal/domain"
	"shopee/backend/services/notification/internal/usecase"
)

// InternalHandler serves the internal "notify" contract Order and Vendor
// call — fire-and-forget from their point of view, since a notification
// failure must never roll back the event that triggered it.
type InternalHandler struct {
	notifications *usecase.NotificationUseCase
	log           zerolog.Logger
}

func NewInternalHandler(notifications *usecase.NotificationUseCase, log zerolog.Logger) *InternalHandler {
	return &InternalHandler{notifications: notifications, log: log}
}

func (h *InternalHandler) Notify(c *gin.Context) {
	var req notifyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	if err := h.notifications.Notify(c.Request.Context(), req.UserID, domain.Type(req.Type), req.ReferenceID); err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusCreated, gin.H{"notified": true})
}

// AdminHandler serves admin's read-only view of what's been sent, for
// operational visibility and debugging.
type AdminHandler struct {
	notifications *usecase.NotificationUseCase
	log           zerolog.Logger
}

func NewAdminHandler(notifications *usecase.NotificationUseCase, log zerolog.Logger) *AdminHandler {
	return &AdminHandler{notifications: notifications, log: log}
}

func (h *AdminHandler) List(c *gin.Context) {
	limit := parseIntDefault(c.Query("limit"), 20, 1, 100)
	offset := parseIntDefault(c.Query("offset"), 0, 0, 1_000_000)

	notifications, err := h.notifications.List(c.Request.Context(), limit, offset)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}

	httpresponse.OK(c, http.StatusOK, toNotificationResponseList(notifications))
}

func parseIntDefault(raw string, fallback, min, max int) int {
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < min || v > max {
		return fallback
	}
	return v
}
