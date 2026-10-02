// Package usecase is the Admin service's read side: an operations
// dashboard and an audit search assembled from the domain services' own
// admin APIs. It keeps no business rules and changes nothing; every action
// stays in the service that owns the data.
package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/admin/internal/adapter"
)

// Reader reads one admin API of a domain service as the signed-in admin.
type Reader interface {
	Get(ctx context.Context, baseURL, path string, query url.Values, authorization string) (json.RawMessage, error)
}

// RoleVerifier re-verifies the admin with Identity.
type RoleVerifier interface {
	RequireRole(ctx context.Context, userID, role string) error
}

// Upstream is a domain service by name and base URL.
type Upstream struct {
	Name string
	URL  string
}

type Service struct {
	Upstreams []Upstream
	Reader    Reader
	Roles     RoleVerifier
	// Timeout bounds each service read.
	Timeout time.Duration
	Log     zerolog.Logger
	Now     func() time.Time
}

// SourceStatus says whether a service answered, and when.
type SourceStatus struct {
	Name        string     `json:"name"`
	Status      string     `json:"status"` // ok | unavailable
	FetchedAt   *time.Time `json:"fetched_at,omitempty"`
	GeneratedAt *time.Time `json:"generated_at,omitempty"`
	Error       string     `json:"error,omitempty"`
}

const (
	statusOK          = "ok"
	statusUnavailable = "unavailable"
)

func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// requireAdmin fails closed: without Identity nothing is read.
func (s Service) requireAdmin(ctx context.Context, adminID string) error {
	if s.Roles == nil {
		return apperror.Internal(errors.New("admin verification is not configured"))
	}
	if err := s.Roles.RequireRole(ctx, adminID, "admin"); err != nil {
		var app *apperror.Error
		if errors.As(err, &app) {
			return app
		}
		return apperror.Internal(err)
	}
	return nil
}

func (s Service) upstream(name string) (Upstream, bool) {
	for _, u := range s.Upstreams {
		if u.Name == name {
			return u, true
		}
	}
	return Upstream{}, false
}

// call is one read to run in parallel.
type call struct {
	source string
	// upstream is the service to ask when source names something else
	// than the service (e.g. "order-events").
	upstream string
	path     string
	query    url.Values
}

type result struct {
	data   json.RawMessage
	status SourceStatus
}

// fetch runs the calls in parallel, each under the timeout, and reports
// every failure as unavailable with a short reason (never the upstream
// body, which may carry internal detail).
func (s Service) fetch(ctx context.Context, authorization string, calls []call) map[string]result {
	out := make(map[string]result, len(calls))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, c := range calls {
		name := c.upstream
		if name == "" {
			name = c.source
		}
		u, ok := s.upstream(name)
		if !ok {
			continue
		}
		wg.Add(1)
		go func(c call, u Upstream) {
			defer wg.Done()
			timeout := s.Timeout
			if timeout <= 0 {
				timeout = 4 * time.Second
			}
			cctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			data, err := s.Reader.Get(cctx, u.URL, c.path, c.query, authorization)
			r := result{status: SourceStatus{Name: c.source, Status: statusOK}}
			if err != nil {
				r.status.Status, r.status.Error = statusUnavailable, describe(err)
				s.Log.Warn().Str("source", c.source).Str("reason", r.status.Error).Msg("admin_source_unavailable")
			} else {
				now := s.now()
				r.data, r.status.FetchedAt = data, &now
			}
			mu.Lock()
			out[c.source] = r
			mu.Unlock()
		}(c, u)
	}
	wg.Wait()
	return out
}

func describe(err error) string {
	var refusal *adapter.Refusal
	if errors.As(err, &refusal) {
		return "refused (" + refusal.Code + ")"
	}
	if errors.Is(err, adapter.ErrUnavailable) {
		return err.Error()
	}
	return "unavailable"
}
