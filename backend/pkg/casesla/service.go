package casesla

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

type Roles interface {
	RequireRole(context.Context, string, string) error
}
type Repository interface {
	List(context.Context, Filter, time.Time) (*Page, error)
	Mutate(context.Context, string, string, string, string, Mutation, time.Time) (*Item, error)
}
type Service struct {
	Repo  Repository
	Roles Roles
	Now   func() time.Time
}

func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
func (s Service) authorize(ctx context.Context, actor string) error {
	if s.Roles == nil {
		return apperror.Internal(errors.New("SLA identity verification unavailable"))
	}
	return s.Roles.RequireRole(ctx, actor, "admin")
}
func (s Service) List(ctx context.Context, actor string, f Filter) (*Page, error) {
	if err := s.authorize(ctx, actor); err != nil {
		return nil, err
	}
	return s.Repo.List(ctx, f, s.now())
}

var commandKey = regexp.MustCompile(`^[A-Za-z0-9._:-]{8,100}$`)

func (s Service) Mutate(ctx context.Context, actor, id, action, key string, m Mutation) (*Item, error) {
	if err := s.authorize(ctx, actor); err != nil {
		return nil, err
	}
	if _, err := uuid.Parse(id); err != nil {
		return nil, apperror.Validation("Invalid work item id")
	}
	m.Reason = strings.TrimSpace(m.Reason)
	if m.ExpectedVersion < 1 || len(m.Reason) < 1 || len(m.Reason) > 500 || !commandKey.MatchString(key) {
		return nil, apperror.Validation("expected_version, reason (1-500 characters), and Idempotency-Key (8-100 characters) are required")
	}
	if action == "assignments" && m.AssigneeID != "" {
		if _, err := uuid.Parse(m.AssigneeID); err != nil {
			return nil, apperror.Validation("Invalid assignee_id")
		}
		if err := s.Roles.RequireRole(ctx, m.AssigneeID, "admin"); err != nil {
			return nil, err
		}
	}
	return s.Repo.Mutate(ctx, id, actor, action, key, m, s.now())
}

// Register adds owner routes to an authenticated admin group. Authorization
// is repeated in Service; handlers only parse and render.
func Register(g *gin.RouterGroup, s Service, log zerolog.Logger) {
	g.GET("/work-items", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		f, e := ParseFilter(c.Request.URL.Query())
		if e != nil {
			httpresponse.HandleError(c, log, e)
			return
		}
		out, e := s.List(c.Request.Context(), middleware.GetUserID(c), f)
		if e != nil {
			httpresponse.HandleError(c, log, e)
			return
		}
		httpresponse.OK(c, 200, out)
	})
	for _, action := range []string{"assignments", "extensions", "activations", "notice-replays"} {
		g.POST("/work-items/:workItemID/"+action, func(c *gin.Context) {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
			var m Mutation
			if e := c.ShouldBindJSON(&m); e != nil {
				httpresponse.HandleError(c, log, apperror.Validation("Invalid work item command"))
				return
			}
			out, e := s.Mutate(c.Request.Context(), middleware.GetUserID(c), c.Param("workItemID"), action, c.GetHeader("Idempotency-Key"), m)
			if e != nil {
				httpresponse.HandleError(c, log, e)
				return
			}
			httpresponse.OK(c, 200, out)
		})
	}
}
