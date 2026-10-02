package eventbus

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
)

// RoleVerifier re-verifies an admin with Identity (identityclient.Client).
type RoleVerifier interface {
	RequireRole(ctx context.Context, userID, role string) error
}

// Admin exposes a service's parked events to operators.
type Admin struct {
	Bus   *Bus
	Inbox Inbox
	Roles RoleVerifier
	Log   zerolog.Logger
}

// Register adds, under an admin route group (admin token already
// required):
//
//	GET  /events/stats                              parked count, oldest, throughput
//	GET  /events/parked?limit=                      parked events (no payload)
//	POST /events/:consumer/:eventId/replay  {reason} apply again
//	POST /events/:consumer/:eventId/discard {reason} give up
//
// Writes re-verify the admin with Identity and are audited.
func (a Admin) Register(group gin.IRoutes) {
	group.GET("/events/stats", a.stats)
	group.GET("/events/parked", a.parked)
	group.POST("/events/:consumer/:eventId/replay", a.act(true))
	group.POST("/events/:consumer/:eventId/discard", a.act(false))
}

func (a Admin) stats(c *gin.Context) {
	stats, err := a.Inbox.Stats(c.Request.Context())
	if err != nil {
		httpresponse.HandleError(c, a.Log, apperror.Internal(err))
		return
	}
	connected := int64(0)
	if a.Bus != nil && a.Bus.Connected() {
		connected = 1
	}
	stats["event_bus_connected"] = connected
	httpresponse.OK(c, http.StatusOK, gin.H{"counts": stats})
}

func (a Admin) parked(c *gin.Context) {
	limit, _ := strconv.Atoi(c.Query("limit"))
	if limit < 1 || limit > 200 {
		limit = 50
	}
	items, err := a.Inbox.ListParked(c.Request.Context(), limit)
	if err != nil {
		httpresponse.HandleError(c, a.Log, apperror.Internal(err))
		return
	}
	httpresponse.OK(c, http.StatusOK, items)
}

func (a Admin) act(replay bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		var body struct {
			Reason string `json:"reason"`
		}
		_ = c.ShouldBindJSON(&body)
		reason := strings.TrimSpace(body.Reason)
		if reason == "" || len(reason) > 500 {
			httpresponse.HandleError(c, a.Log, apperror.Validation("A reason of at most 500 characters is required"))
			return
		}
		ctx := c.Request.Context()
		adminID := middleware.GetUserID(c)
		if a.Roles == nil {
			httpresponse.HandleError(c, a.Log, apperror.Internal(errors.New("admin verification is not configured")))
			return
		}
		if err := a.Roles.RequireRole(ctx, adminID, "admin"); err != nil {
			var app *apperror.Error
			if !errors.As(err, &app) {
				app = apperror.Internal(err)
			}
			httpresponse.HandleError(c, a.Log, app)
			return
		}
		consumer, eventID := c.Param("consumer"), c.Param("eventId")
		var err error
		if replay {
			handler, ok := a.Bus.HandlerFor(consumer)
			if !ok {
				httpresponse.HandleError(c, a.Log, apperror.NotFound("Unknown consumer"))
				return
			}
			err = a.Inbox.Replay(ctx, consumer, eventID, adminID, reason, handler)
		} else {
			err = a.Inbox.Discard(ctx, consumer, eventID, adminID, reason)
		}
		switch {
		case err == nil:
			a.Log.Info().Str("consumer", consumer).Str("event_id", eventID).Bool("replay", replay).Msg("parked_event_resolved")
			httpresponse.OK(c, http.StatusOK, gin.H{"consumer": consumer, "event_id": eventID, "replayed": replay})
		case errors.Is(err, ErrNotParked):
			httpresponse.HandleError(c, a.Log, apperror.Conflict("This event is not parked"))
		default:
			var app *apperror.Error
			if errors.As(err, &app) {
				httpresponse.HandleError(c, a.Log, apperror.Conflict("Replay was refused: "+app.Message))
				return
			}
			httpresponse.HandleError(c, a.Log, apperror.Internal(err))
		}
	}
}
