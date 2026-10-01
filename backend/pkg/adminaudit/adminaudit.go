// Package adminaudit is the read side of the admin audit trail. Every
// service keeps its own authoritative audit rows, written in the same
// transaction as the change they record; this package only lets an admin
// search them with one filter and one keyset cursor, so the Admin service
// can merge several services' results without loading whole tables.
package adminaudit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
)

const (
	DefaultLimit = 50
	MaxLimit     = 100
	// CursorMax sorts after every row id (ids are uuids or digits), so a
	// cursor with it includes every row at the cursor time.
	CursorMax = "~"
)

// Entry is one audited action in the shape every service reports it.
// Changes holds only the fields the action changed (for example a status
// or a rate), never secrets or contact details.
type Entry struct {
	ID         string          `json:"id"`
	Source     string          `json:"source"`
	OccurredAt time.Time       `json:"occurred_at"`
	ActorID    *string         `json:"actor_id,omitempty"`
	Action     string          `json:"action"`
	EntityType string          `json:"entity_type"`
	EntityID   string          `json:"entity_id"`
	Reason     *string         `json:"reason,omitempty"`
	RequestID  *string         `json:"request_id,omitempty"`
	Changes    json.RawMessage `json:"changes,omitempty"`
}

// Filter narrows a search. Rows come newest first; CursorTime/CursorID
// return the rows strictly after (older than) that position.
type Filter struct {
	ActorID    string
	EntityType string
	EntityID   string
	Action     string
	RequestID  string
	From, To   *time.Time
	CursorTime *time.Time
	CursorID   string
	Limit      int
}

var (
	uuidPattern     = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	namePattern     = regexp.MustCompile(`^[a-z_]{1,60}$`)
	entityIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,100}$`)
	requestPattern  = regexp.MustCompile(`^[A-Za-z0-9._:-]{8,64}$`)
	cursorIDPattern = regexp.MustCompile(`^([0-9a-zA-Z-]{1,64}|~)?$`)
)

// ParseFilter validates query parameters: actor_id, entity_type,
// entity_id, action, request_id, from, to (RFC3339), cursor_time
// (RFC3339 with nanoseconds), cursor_id and limit (1-100).
func ParseFilter(q url.Values) (Filter, error) {
	f := Filter{
		ActorID: q.Get("actor_id"), EntityType: q.Get("entity_type"), EntityID: q.Get("entity_id"),
		Action: q.Get("action"), RequestID: q.Get("request_id"), CursorID: q.Get("cursor_id"), Limit: DefaultLimit,
	}
	checks := []struct {
		value   string
		pattern *regexp.Regexp
		field   string
	}{
		{f.ActorID, uuidPattern, "actor_id must be a user id"},
		{f.EntityType, namePattern, "entity_type must be lowercase letters and underscores"},
		{f.EntityID, entityIDPattern, "entity_id must be at most 100 letters, digits or ._:-"},
		{f.Action, namePattern, "action must be lowercase letters and underscores"},
		{f.RequestID, requestPattern, "request_id must be 8-64 letters, digits or ._:-"},
	}
	for _, c := range checks {
		if c.value != "" && !c.pattern.MatchString(c.value) {
			return Filter{}, apperror.Validation(c.field)
		}
	}
	if !cursorIDPattern.MatchString(f.CursorID) {
		return Filter{}, apperror.Validation("cursor_id is invalid")
	}
	for _, p := range []struct {
		name string
		dst  **time.Time
	}{{"from", &f.From}, {"to", &f.To}, {"cursor_time", &f.CursorTime}} {
		raw := q.Get(p.name)
		if raw == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return Filter{}, apperror.Validation(p.name + " must be an RFC3339 time")
		}
		*p.dst = &t
	}
	if f.CursorID != "" && f.CursorTime == nil {
		return Filter{}, apperror.Validation("cursor_id needs cursor_time")
	}
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > MaxLimit {
			return Filter{}, apperror.Validation("limit must be between 1 and 100")
		}
		f.Limit = n
	}
	return f, nil
}

// Values encodes the filter back into query parameters (for a caller that
// forwards a search to a service).
func (f Filter) Values() url.Values {
	q := url.Values{}
	set := func(k, v string) {
		if v != "" {
			q.Set(k, v)
		}
	}
	set("actor_id", f.ActorID)
	set("entity_type", f.EntityType)
	set("entity_id", f.EntityID)
	set("action", f.Action)
	set("request_id", f.RequestID)
	set("cursor_id", f.CursorID)
	for k, t := range map[string]*time.Time{"from": f.From, "to": f.To, "cursor_time": f.CursorTime} {
		if t != nil {
			q.Set(k, t.UTC().Format(time.RFC3339Nano))
		}
	}
	if f.Limit > 0 {
		q.Set("limit", strconv.Itoa(f.Limit))
	}
	return q
}

// Querier is satisfied by a pgx pool or transaction.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// RoleChecker re-verifies the caller with Identity (identityclient.Client).
type RoleChecker interface {
	RequireRole(ctx context.Context, userID, role string) error
}

// Source is one service's audit trail. SQL is a SELECT returning, in this
// order: id text, occurred_at timestamptz, actor_id text, action text,
// entity_type text, entity_id text, reason text, request_id text and
// changes jsonb (null where a column does not apply).
type Source struct {
	Name  string
	SQL   string
	DB    Querier
	Roles RoleChecker
}

// Search returns one page of the service's audit rows for an admin. Reading
// the audit never changes anything, and it fails closed when the admin
// cannot be verified.
func (s Source) Search(ctx context.Context, actorID string, f Filter) ([]Entry, error) {
	if s.Roles == nil {
		return nil, apperror.Internal(errors.New("admin verification is not configured"))
	}
	if err := s.Roles.RequireRole(ctx, actorID, "admin"); err != nil {
		var app *apperror.Error
		if errors.As(err, &app) {
			return nil, app
		}
		return nil, apperror.Internal(err)
	}
	sql, args := BuildQuery(s.SQL, f)
	rows, err := s.DB.Query(ctx, sql, args...)
	if err != nil {
		return nil, apperror.Internal(fmt.Errorf("audit search %s: %w", s.Name, err))
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		e := Entry{Source: s.Name}
		var changes []byte
		if err := rows.Scan(&e.ID, &e.OccurredAt, &e.ActorID, &e.Action, &e.EntityType, &e.EntityID, &e.Reason, &e.RequestID, &changes); err != nil {
			return nil, apperror.Internal(fmt.Errorf("audit search %s: %w", s.Name, err))
		}
		if len(changes) > 0 {
			e.Changes = changes
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, apperror.Internal(fmt.Errorf("audit search %s: %w", s.Name, err))
	}
	return out, nil
}

// BuildQuery wraps a source SELECT with the filter, newest first. Ids are
// compared byte-wise so the order matches the cursor across databases.
func BuildQuery(base string, f Filter) (string, []any) {
	var where []string
	var args []any
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, strings.ReplaceAll(cond, "?", "$"+strconv.Itoa(len(args))))
	}
	if f.ActorID != "" {
		add("a.actor_id = ?", strings.ToLower(f.ActorID))
	}
	if f.EntityType != "" {
		add("a.entity_type = ?", f.EntityType)
	}
	if f.EntityID != "" {
		add("a.entity_id = ?", f.EntityID)
	}
	if f.Action != "" {
		add("a.action = ?", f.Action)
	}
	if f.RequestID != "" {
		add("a.request_id = ?", f.RequestID)
	}
	if f.From != nil {
		add("a.occurred_at >= ?", *f.From)
	}
	if f.To != nil {
		add("a.occurred_at < ?", *f.To)
	}
	if f.CursorTime != nil {
		args = append(args, *f.CursorTime, f.CursorID)
		t, id := "$"+strconv.Itoa(len(args)-1), "$"+strconv.Itoa(len(args))
		where = append(where, "(a.occurred_at < "+t+" OR (a.occurred_at = "+t+" AND a.id COLLATE \"C\" < "+id+"))")
	}
	limit := f.Limit
	if limit < 1 || limit > MaxLimit {
		limit = DefaultLimit
	}
	sql := "SELECT a.id, a.occurred_at, a.actor_id, a.action, a.entity_type, a.entity_id, a.reason, a.request_id, a.changes FROM (" +
		base + ") AS a(id, occurred_at, actor_id, action, entity_type, entity_id, reason, request_id, changes)"
	if len(where) > 0 {
		sql += " WHERE " + strings.Join(where, " AND ")
	}
	sql += " ORDER BY a.occurred_at DESC, a.id COLLATE \"C\" DESC LIMIT " + strconv.Itoa(limit)
	return sql, args
}

// Register adds GET path to an admin route group (the group already
// requires an admin token).
func Register(r gin.IRoutes, path string, s Source, log zerolog.Logger) {
	r.GET(path, func(c *gin.Context) {
		f, err := ParseFilter(c.Request.URL.Query())
		if err != nil {
			httpresponse.HandleError(c, log, err)
			return
		}
		entries, err := s.Search(c.Request.Context(), middleware.GetUserID(c), f)
		if err != nil {
			httpresponse.HandleError(c, log, err)
			return
		}
		c.Header("Cache-Control", "no-store")
		httpresponse.OK(c, http.StatusOK, gin.H{"source": s.Name, "entries": entries})
	})
}
