package bizreport

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"shopee/backend/pkg/apperror"
)

func TestReportRangeVietnamBoundaries(t *testing.T) {
	r, err := ParseRange("2026-10-01T00:00:00+07:00", "2026-11-01T00:00:00+07:00", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if r.From.Format(time.RFC3339) != "2026-09-30T17:00:00Z" || r.To.Format(time.RFC3339) != "2026-10-31T17:00:00Z" || r.Group != GroupDay {
		t.Fatalf("range: %+v", r)
	}
	for _, tc := range []struct{ from, to, group, vendor string }{
		{"2026-10-01T00:00:00", "2026-11-01T00:00:00Z", "", ""},
		{"2026-10-01T00:00:00Z", "2026-10-01T00:00:00Z", "", ""},
		{"2026-10-01T00:00:00Z", "2026-09-01T00:00:00Z", "", ""},
		{"2024-01-01T00:00:00Z", "2025-01-01T00:00:01Z", "", ""},
		{"2026-10-01T00:00:00Z", "2026-11-01T00:00:00Z", "week", ""},
		{"2026-10-01T00:00:00Z", "2026-11-01T00:00:00Z", "day", "not-a-shop"},
	} {
		_, err := ParseRange(tc.from, tc.to, tc.group, tc.vendor)
		var app *apperror.Error
		if !errors.As(err, &app) || app.Status != 422 || app.Code != "invalid_range" {
			t.Fatalf("%+v: %v", tc, err)
		}
	}
	if _, err := ParseRange("2024-01-01T00:00:00Z", "2025-01-01T00:00:00Z", "month", ""); err != nil {
		t.Fatal("366-day boundary:", err)
	}
}

func TestReportZeroDenominatorIsNull(t *testing.T) {
	r := NewRatio(0, 0)
	raw, err := json.Marshal(r)
	if err != nil || string(raw) != `{"numerator":0,"denominator":0,"rate":null}` {
		t.Fatalf("zero sample: %s %v", raw, err)
	}
	r = NewRatio(1, 4)
	if r.Rate == nil || *r.Rate != 0.25 || r.Numerator != 1 || r.Denominator != 4 {
		t.Fatalf("ratio: %+v", r)
	}
}

func TestDrilldownCursorRetainsCutoffAndTieBreaker(t *testing.T) {
	cutoff := time.Date(2026, 10, 14, 0, 0, 0, 123456000, time.UTC)
	ids := []string{"00000000-0000-0000-0000-000000000003", "00000000-0000-0000-0000-000000000002", "00000000-0000-0000-0000-000000000001"}
	d := Drilldown{Metric: "captured", Cutoff: cutoff, Limit: 2}
	p := NewPage(d, ids, func(id string) Cursor { return Cursor{At: cutoff, ID: id} })
	if len(p.Items) != 2 || p.NextCursor == "" || !p.Cutoff.Equal(cutoff) {
		t.Fatalf("page: %+v", p)
	}
	next, err := ParseDrilldown(Range{}, "captured", cutoff.Format(time.RFC3339Nano), p.NextCursor, "2", []string{"captured"})
	if err != nil {
		t.Fatal(err)
	}
	at, id := next.CursorArgs()
	if !at.Equal(cutoff) || id != ids[1] || !next.Cutoff.Equal(cutoff) {
		t.Fatalf("cursor: %+v", next)
	}
	for _, tc := range []struct{ metric, cutoff, cursor, limit string }{
		{"other", cutoff.Format(time.RFC3339Nano), "", ""}, {"captured", "yesterday", "", ""},
		{"captured", cutoff.Format(time.RFC3339Nano), "!", ""}, {"captured", cutoff.Format(time.RFC3339Nano), "", "0"},
		{"captured", cutoff.Format(time.RFC3339Nano), "", "101"}, {"captured", cutoff.Format(time.RFC3339Nano), Cursor{At: cutoff, ID: "bad"}.Encode(), ""},
	} {
		if _, err := ParseDrilldown(Range{}, tc.metric, tc.cutoff, tc.cursor, tc.limit, []string{"captured"}); err == nil {
			t.Fatalf("accepted %+v", tc)
		}
	}
	last := NewPage(d, []string(nil), func(id string) Cursor { return Cursor{ID: id} })
	if last.Items == nil || last.NextCursor != "" {
		t.Fatalf("empty page: %+v", last)
	}
}
