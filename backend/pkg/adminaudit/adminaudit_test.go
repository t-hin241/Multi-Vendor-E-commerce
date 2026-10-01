package adminaudit_test

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"shopee/backend/pkg/adminaudit"
	"shopee/backend/pkg/apperror"
)

func TestParseFilterValidatesEveryField(t *testing.T) {
	bad := []string{
		"actor_id=not-a-uuid",
		"entity_type=Order",
		"entity_id=has space",
		"action=DROP TABLE",
		"request_id=short",
		"from=yesterday",
		"limit=0",
		"limit=101",
		"cursor_id=abc",
		"cursor_time=2026-01-01T00:00:00Z&cursor_id=a'b",
	}
	for _, raw := range bad {
		q, _ := url.ParseQuery(raw)
		if _, err := adminaudit.ParseFilter(q); err == nil {
			t.Errorf("%s: expected a validation error", raw)
		}
	}
	q, _ := url.ParseQuery("actor_id=11111111-1111-1111-1111-111111111111&entity_type=order&entity_id=abc-1&action=refund_requested&request_id=req-12345678&from=2026-01-01T00:00:00Z&limit=10&cursor_time=2026-02-01T00:00:00.123456Z&cursor_id=~")
	f, err := adminaudit.ParseFilter(q)
	if err != nil {
		t.Fatal(err)
	}
	if f.Limit != 10 || f.CursorTime == nil || f.CursorTime.Nanosecond() != 123456000 || f.CursorID != adminaudit.CursorMax {
		t.Fatalf("unexpected filter %+v", f)
	}
	again, err := adminaudit.ParseFilter(f.Values())
	if err != nil || again.ActorID != f.ActorID || !again.CursorTime.Equal(*f.CursorTime) || again.Limit != 10 {
		t.Fatalf("filter does not round trip: %+v %v", again, err)
	}
}

func TestBuildQueryUsesPlaceholdersOnly(t *testing.T) {
	cursor := time.Now()
	sql, args := adminaudit.BuildQuery("SELECT 1", adminaudit.Filter{
		ActorID: "x", Action: "approved'; --", CursorTime: &cursor, CursorID: "abc", Limit: 500,
	})
	if strings.Contains(sql, "approved") || len(args) != 4 {
		t.Fatalf("values must be bound, not inlined: %s %v", sql, args)
	}
	if !strings.Contains(sql, "LIMIT 50") {
		t.Fatalf("an out of range limit falls back to the default: %s", sql)
	}
	if !strings.Contains(sql, `a.id COLLATE "C" < $4`) {
		t.Fatalf("cursor must compare ids byte-wise: %s", sql)
	}
}

type roles struct{ err error }

func (r roles) RequireRole(context.Context, string, string) error { return r.err }

func TestSearchFailsClosed(t *testing.T) {
	if _, err := (adminaudit.Source{Name: "x"}).Search(context.Background(), "a", adminaudit.Filter{}); err == nil {
		t.Fatal("no role checker must refuse")
	}
	_, err := (adminaudit.Source{Name: "x", Roles: roles{apperror.Forbidden("no")}}).Search(context.Background(), "a", adminaudit.Filter{})
	var app *apperror.Error
	if !errors.As(err, &app) || app.Code != apperror.CodeForbidden {
		t.Fatalf("non-admin must be forbidden, got %v", err)
	}
}
