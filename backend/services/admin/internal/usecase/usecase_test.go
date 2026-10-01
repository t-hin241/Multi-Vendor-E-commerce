package usecase_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"shopee/backend/pkg/adminaudit"
	"shopee/backend/pkg/apperror"
	"shopee/backend/services/admin/internal/adapter"
	"shopee/backend/services/admin/internal/usecase"
)

type roles struct{ err error }

func (r roles) RequireRole(context.Context, string, string) error { return r.err }

// fakeReader answers like the services: ops counters by path, and audit
// searches applying the forwarded filter to in-memory rows. Service fans out
// Get calls concurrently, so recorded state is guarded by mu.
type fakeReader struct {
	mu    sync.Mutex
	down  map[string]bool
	ops   map[string]string
	audit map[string][]adminaudit.Entry
	auth  []string
}

func (f *fakeReader) Get(_ context.Context, baseURL, path string, q url.Values, authorization string) (json.RawMessage, error) {
	f.mu.Lock()
	f.auth = append(f.auth, authorization)
	f.mu.Unlock()
	source := strings.TrimPrefix(baseURL, "http://")
	if f.down[source] {
		return nil, fmt.Errorf("%w: timed out", adapter.ErrUnavailable)
	}
	if strings.HasSuffix(path, "/audit-events") {
		filter, err := adminaudit.ParseFilter(q)
		if err != nil {
			return nil, &adapter.Refusal{Status: 400, Code: "validation_error"}
		}
		rows := append([]adminaudit.Entry(nil), f.audit[source]...)
		sort.Slice(rows, func(i, j int) bool {
			if !rows[i].OccurredAt.Equal(rows[j].OccurredAt) {
				return rows[i].OccurredAt.After(rows[j].OccurredAt)
			}
			return rows[i].ID > rows[j].ID
		})
		var out []adminaudit.Entry
		for _, e := range rows {
			if filter.CursorTime != nil && !(e.OccurredAt.Before(*filter.CursorTime) || (e.OccurredAt.Equal(*filter.CursorTime) && e.ID < filter.CursorID)) {
				continue
			}
			if filter.Action != "" && e.Action != filter.Action {
				continue
			}
			if len(out) < filter.Limit {
				out = append(out, e)
			}
		}
		raw, _ := json.Marshal(map[string]any{"source": source, "entries": out})
		return raw, nil
	}
	return json.RawMessage(f.ops[source]), nil
}

func service(r *fakeReader, names ...string) usecase.Service {
	s := usecase.Service{Reader: r, Roles: roles{}, Log: zerolog.Nop(), Timeout: time.Second}
	for _, n := range names {
		s.Upstreams = append(s.Upstreams, usecase.Upstream{Name: n, URL: "http://" + n})
	}
	return s
}

func tile(t *testing.T, d *usecase.Dashboard, key string) usecase.Tile {
	t.Helper()
	for _, x := range d.Tiles {
		if x.Key == key {
			return x
		}
	}
	t.Fatalf("no tile %s", key)
	return usecase.Tile{}
}

// ADM-03: a service that does not answer shows as unavailable, never as
// zero, and the others still show.
func TestDashboardMarksUnavailableSourcesInsteadOfZero(t *testing.T) {
	r := &fakeReader{down: map[string]bool{"payment": true}, ops: map[string]string{
		"vendor":    `{"pending_applications": 3, "parked_status_events": 0, "oldest_pending_event_seconds": 1.5}`,
		"catalog":   `{"pending_review": 0, "status_parked": 0, "cleanup_parked": 1}`,
		"inventory": `{"overdue": 0, "expiry_parked": 0, "events_parked": 0, "cache_parked": 0, "order_mismatches": 0, "reserved_mismatches": 0}`,
		"order":     `{"pending": 1, "parked": 2, "oldest_pending": null, "parked_effects": [], "counts": {"awaiting_shipment": 5, "payment_exceptions": 0, "return_refunds_failed": 0}, "generated_at": "2026-10-01T10:00:00Z"}`,
		"shipment":  `{"counts": {"fulfillment_lag": 0, "tracking_stale": 0, "interception_pending": 0, "order_events_review": 0}, "lists": {}}`,
	}}
	s := service(r, "vendor", "catalog", "inventory", "order", "payment", "shipment")
	d, err := s.Dashboard(t.Context(), "admin-1", "Bearer test-token")
	if err != nil {
		t.Fatal(err)
	}
	if v := tile(t, d, "vendors_pending"); v.Count == nil || *v.Count != 3 || v.Status != "attention" {
		t.Fatalf("vendors tile: %+v", v)
	}
	if v := tile(t, d, "awaiting_shipment"); v.Count == nil || *v.Count != 5 || v.Status != "ok" {
		t.Fatalf("workload tile is informational: %+v", v)
	}
	if v := tile(t, d, "order_jobs_failed"); v.Count == nil || *v.Count != 2 {
		t.Fatalf("parked effects: %+v", v)
	}
	for _, key := range []string{"captures_not_applied", "refunds_pending", "payments_stuck"} {
		if v := tile(t, d, key); v.Count != nil || v.Status != "unavailable" {
			t.Fatalf("%s must be unavailable, not a number: %+v", key, v)
		}
	}
	for _, src := range d.Sources {
		if src.Name == "payment" && (src.Status != "unavailable" || src.Error == "") {
			t.Fatalf("payment source: %+v", src)
		}
		if src.Name == "order" && (src.GeneratedAt == nil || src.FetchedAt == nil) {
			t.Fatalf("order freshness missing: %+v", src)
		}
	}
	for _, a := range r.auth {
		if a != "Bearer test-token" {
			t.Fatal("the admin's own token must be forwarded")
		}
	}
	// A counter an older service version does not report is unavailable too.
	if v := tile(t, d, "return_refunds_failed"); v.Count == nil {
		t.Fatalf("reported counter: %+v", v)
	}
	r.ops["catalog"] = `{"status_parked": 0, "cleanup_parked": 0}`
	d, _ = s.Dashboard(t.Context(), "admin-1", "Bearer test-token")
	if v := tile(t, d, "products_pending"); v.Status != "unavailable" {
		t.Fatalf("missing counter must not read as zero: %+v", v)
	}
}

// Reviews are optional: without the service their tiles are left out
// rather than shown as unavailable; with it they read its counters.
func TestReviewTilesFollowTheReviewService(t *testing.T) {
	r := &fakeReader{ops: map[string]string{"review": `{"counts": {"open_reports": 2, "image_cleanup_parked": 0}}`}}
	d, err := service(r, "order").Dashboard(t.Context(), "admin-1", "Bearer x")
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range d.Tiles {
		if x.Key == "review_reports_open" || x.Key == "review_uploads_stuck" {
			t.Fatalf("review tiles without the review service: %+v", x)
		}
	}
	d, _ = service(r, "order", "review").Dashboard(t.Context(), "admin-1", "Bearer x")
	if v := tile(t, d, "review_reports_open"); v.Count == nil || *v.Count != 2 || v.Status != "attention" {
		t.Fatalf("open reports: %+v", v)
	}
}

func TestReadsNeedAVerifiedAdmin(t *testing.T) {
	r := &fakeReader{}
	s := service(r, "order")
	s.Roles = roles{apperror.Forbidden("no")}
	var app *apperror.Error
	if _, err := s.Dashboard(t.Context(), "buyer-1", "Bearer x"); !errors.As(err, &app) || app.Code != apperror.CodeForbidden {
		t.Fatalf("expected forbidden, got %v", err)
	}
	if _, err := s.SearchAudit(t.Context(), "buyer-1", "Bearer x", adminaudit.Filter{}, "", ""); !errors.As(err, &app) || app.Code != apperror.CodeForbidden {
		t.Fatalf("expected forbidden, got %v", err)
	}
	s.Roles = nil
	if _, err := s.Dashboard(t.Context(), "admin-1", "Bearer x"); err == nil {
		t.Fatal("without Identity nothing is read")
	}
	if len(r.auth) != 0 {
		t.Fatal("no service may be called for an unverified caller")
	}
}

// ADM-04: paging through the merged audit returns every row exactly once,
// in order, even with rows of several services at the same instant.
func TestAuditPagingMergesServicesWithoutGapsOrRepeats(t *testing.T) {
	base := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	r := &fakeReader{audit: map[string][]adminaudit.Entry{}}
	var want []string
	for i, src := range []string{"order", "payment", "vendor"} {
		for j := 0; j < 4; j++ {
			at := base.Add(time.Duration(j%2) * time.Minute) // many ties
			id := fmt.Sprintf("0000000%d-0000-0000-0000-00000000000%d", i, j)
			r.audit[src] = append(r.audit[src], adminaudit.Entry{ID: id, OccurredAt: at, Action: "x", EntityType: "t", EntityID: id})
			want = append(want, src+"/"+id)
		}
	}
	s := service(r, "order", "payment", "vendor")
	var got []string
	cursor := ""
	for pages := 0; pages < 20; pages++ {
		page, err := s.SearchAudit(t.Context(), "admin-1", "Bearer x", adminaudit.Filter{Limit: 5}, cursor, "")
		if err != nil {
			t.Fatal(err)
		}
		for i, e := range page.Entries {
			got = append(got, e.Source+"/"+e.ID)
			if i > 0 && e.OccurredAt.After(page.Entries[i-1].OccurredAt) {
				t.Fatal("entries must be newest first")
			}
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d rows, got %d: %v", len(want), len(got), got)
	}
	seen := map[string]bool{}
	for _, g := range got {
		if seen[g] {
			t.Fatalf("row %s returned twice", g)
		}
		seen[g] = true
	}

	// A service that is down makes the page incomplete, not empty.
	r.down = map[string]bool{"payment": true}
	page, err := s.SearchAudit(t.Context(), "admin-1", "Bearer x", adminaudit.Filter{Limit: 50}, "", "")
	if err != nil || page.Complete || len(page.Entries) != 8 {
		t.Fatalf("partial page: complete=%v entries=%d err=%v", page.Complete, len(page.Entries), err)
	}
	if _, err := s.SearchAudit(t.Context(), "admin-1", "Bearer x", adminaudit.Filter{}, "not-a-cursor", ""); err == nil {
		t.Fatal("a forged cursor must be refused")
	}
	if _, err := s.SearchAudit(t.Context(), "admin-1", "Bearer x", adminaudit.Filter{}, "", "billing"); err == nil {
		t.Fatal("an unknown source must be refused")
	}
}
