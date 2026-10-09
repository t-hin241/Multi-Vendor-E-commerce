package transport

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/authjwt"
	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
	"shopee/backend/services/notification/internal/domain"
	"shopee/backend/services/notification/internal/usecase"
)

// InboxRoutes serves a person's own inbox (AF-09): any signed-in role,
// always the session's user, never cached.
type InboxRoutes struct {
	UseCase *usecase.InboxUseCase
	Log     zerolog.Logger
}

// RegisterInbox adds the inbox routes.
func RegisterInbox(r gin.IRoutes, jwt *authjwt.Manager, h InboxRoutes) {
	mw := []gin.HandlerFunc{middleware.RequireAuth(jwt), func(c *gin.Context) { c.Header("Cache-Control", "private, no-store"); c.Next() }}
	r.GET("/api/notifications/inbox", append(mw, h.list)...)
	r.GET("/api/notifications/inbox/unread-count", append(mw, h.unreadCount)...)
	r.POST("/api/notifications/inbox/read-markers", append(mw, h.markThrough)...)
	r.PUT("/api/notifications/inbox/:id/read", append(mw, h.markRead)...)
	r.DELETE("/api/notifications/inbox/:id", append(mw, h.hide)...)
}

// inboxItemResponse is plain text only; the link is an app route.
type inboxItemResponse struct {
	ID            string     `json:"id"`
	Type          string     `json:"type"`
	Title         string     `json:"title"`
	Body          string     `json:"body"`
	ReferenceType string     `json:"reference_type"`
	ReferenceID   string     `json:"reference_id"`
	Link          string     `json:"link"`
	ReadAt        *time.Time `json:"read_at"`
	CreatedAt     time.Time  `json:"created_at"`
}

func toInboxItem(i *domain.InboxItem) inboxItemResponse {
	return inboxItemResponse{ID: i.ID, Type: string(i.Kind), Title: i.Title, Body: i.Body, ReferenceType: i.ReferenceType,
		ReferenceID: i.ReferenceID, Link: i.Link, ReadAt: i.ReadAt, CreatedAt: i.CreatedAt}
}

func (h InboxRoutes) list(c *gin.Context) {
	page, err := h.UseCase.List(c.Request.Context(), middleware.GetUserID(c), c.Query("unread_only") == "true", c.Query("cursor"),
		parseIntDefault(c.Query("limit"), 20, 1, 100))
	if err != nil {
		httpresponse.HandleError(c, h.Log, err)
		return
	}
	items := make([]inboxItemResponse, 0, len(page.Items))
	for _, i := range page.Items {
		items = append(items, toInboxItem(i))
	}
	var next *string
	if page.NextCursor != "" {
		next = &page.NextCursor
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"items": items, "next_cursor": next, "as_of": page.AsOf})
}

func (h InboxRoutes) unreadCount(c *gin.Context) {
	n, err := h.UseCase.UnreadCount(c.Request.Context(), middleware.GetUserID(c))
	if err != nil {
		httpresponse.HandleError(c, h.Log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"unread_count": n})
}

func (h InboxRoutes) markRead(c *gin.Context) {
	item, err := h.UseCase.MarkRead(c.Request.Context(), middleware.GetUserID(c), c.Param("id"))
	if err != nil {
		httpresponse.HandleError(c, h.Log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"id": item.ID, "read_at": item.ReadAt})
}

func (h InboxRoutes) hide(c *gin.Context) {
	item, err := h.UseCase.Hide(c.Request.Context(), middleware.GetUserID(c), c.Param("id"))
	if err != nil {
		httpresponse.HandleError(c, h.Log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"id": item.ID, "hidden_at": item.HiddenAt})
}

type readMarkerRequest struct {
	ThroughID string `json:"through_id" binding:"required,max=64"`
}

func (h InboxRoutes) markThrough(c *gin.Context) {
	var req readMarkerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpresponse.Error(c, http.StatusBadRequest, "validation_error", "through_id is required")
		return
	}
	affected, unread, err := h.UseCase.MarkThrough(c.Request.Context(), middleware.GetUserID(c), req.ThroughID)
	if err != nil {
		httpresponse.HandleError(c, h.Log, err)
		return
	}
	httpresponse.OK(c, http.StatusOK, gin.H{"affected": affected, "unread_count": unread})
}
