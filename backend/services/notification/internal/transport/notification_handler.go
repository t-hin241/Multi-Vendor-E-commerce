package transport

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/notification/internal/domain"
	"shopee/backend/services/notification/internal/repository"
	"shopee/backend/services/notification/internal/usecase"
)

// InternalHandler serves the service-authenticated notify contract. It
// records the request and answers 202 at once; delivery happens in the
// background, so a mail outage never fails the producer.
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
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "user_id, type and reference_id are required")
		return
	}
	correlation := req.CorrelationID
	if correlation == "" {
		correlation = middleware.GetRequestID(c)
	}
	n, duplicate, err := h.notifications.Accept(c.Request.Context(), domain.Request{
		EventID: req.EventID, Source: req.Source, UserID: req.UserID, Type: domain.Type(req.Type),
		ReferenceID: req.ReferenceID, CorrelationID: correlation,
	})
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusAccepted, gin.H{"id": n.ID, "status": n.Status, "duplicate": duplicate})
}

// AdminHandler serves the delivery status view and the audited retry.
type AdminHandler struct {
	notifications *usecase.NotificationUseCase
	log           zerolog.Logger
}

func NewAdminHandler(notifications *usecase.NotificationUseCase, log zerolog.Logger) *AdminHandler {
	return &AdminHandler{notifications: notifications, log: log}
}

// List: ?status=&type=&user_id=&limit=&offset=; total in X-Total-Count.
func (h *AdminHandler) List(c *gin.Context) {
	limit := parseIntDefault(c.Query("limit"), 20, 1, 100)
	offset := parseIntDefault(c.Query("offset"), 0, 0, 1_000_000)
	items, total, err := h.notifications.List(c.Request.Context(),
		repository.Filter{Status: c.Query("status"), Type: c.Query("type"), UserID: c.Query("user_id")}, limit, offset)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	c.Header("X-Total-Count", strconv.FormatInt(total, 10))
	httpresponse.OK(c, http.StatusOK, toNotificationResponseList(items))
}

func (h *AdminHandler) Attempts(c *gin.Context) {
	items, err := h.notifications.Attempts(c.Request.Context(), c.Param("id"))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toAttemptResponseList(items))
}

// Operations is the delivery report: queued, parked, failed and sent in
// the last 24 hours, the oldest waiting and the delivery latency.
func (h *AdminHandler) Operations(c *gin.Context) {
	counts, err := h.notifications.Operations(c.Request.Context(), middleware.GetUserID(c))
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"counts": counts})
}

func (h *AdminHandler) Retry(c *gin.Context) {
	var req retryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "A reason of at most 500 characters is required")
		return
	}
	n, err := h.notifications.Retry(c.Request.Context(), middleware.GetUserID(c), c.Param("id"), req.Reason)
	if err != nil {
		httpresponse.HandleError(c, h.log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, toNotificationResponse(n))
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
