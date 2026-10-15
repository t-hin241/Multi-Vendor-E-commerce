package usecase_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/bizreport"
	"shopee/backend/services/admin/internal/adapter"
	"shopee/backend/services/admin/internal/usecase"
)

type businessReader struct {
	mu      sync.Mutex
	seen    map[string]url.Values
	mode    string
	started chan string
}

func (r *businessReader) Get(ctx context.Context, base, path string, q url.Values, auth string) (json.RawMessage, error) {
	source := strings.TrimPrefix(base, "http://")
	if auth != "Bearer fake-report-token" || path != "/api/"+map[string]string{"order": "orders", "payment": "payments", "shipment": "shipments"}[source]+"/admin/business-reports" {
		return nil, errors.New("incorrect owner route or authorization")
	}
	r.mu.Lock()
	r.seen[source] = q
	r.mu.Unlock()
	if r.started != nil {
		r.started <- source
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	if source == "payment" {
		switch r.mode {
		case "timeout":
			<-ctx.Done()
			return nil, ctx.Err()
		case "refused":
			return nil, &adapter.Refusal{Status: 403, Code: "permission_denied"}
		case "error":
			return nil, errors.New("synthetic owner error")
		case "version":
			return json.RawMessage(`{"definition_version":"v999","as_of":"2026-10-01T01:00:00Z"}`), nil
		case "malformed":
			return json.RawMessage(`invalid`), nil
		}
	}
	return json.RawMessage(`{"definition_version":"business-report-v1","as_of":"2026-10-01T01:00:00Z","source":"` + source + `","count":7}`), nil
}

func TestBusinessReportBFFPartialFailuresAndFilters(t *testing.T) {
	for _, mode := range []string{"timeout", "refused", "error", "version", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			r := &businessReader{seen: map[string]url.Values{}, mode: mode}
			s := usecase.Service{Reader: r, Roles: roles{}, BusinessReports: true, Timeout: 10 * time.Millisecond, Log: zerolog.Nop(), Upstreams: []usecase.Upstream{{Name: "order", URL: "http://order"}, {Name: "payment", URL: "http://payment"}, {Name: "shipment", URL: "http://shipment"}}}
			period, err := bizreport.ParseRange("2026-10-01T00:00:00+07:00", "2026-10-02T00:00:00+07:00", "month", "00000000-0000-4000-8000-000000000001")
			if err != nil {
				t.Fatal(err)
			}
			out, err := s.BusinessReport(t.Context(), "admin", "Bearer fake-report-token", period)
			if err != nil {
				t.Fatal(err)
			}
			if out.Payment != nil || out.Order == nil || out.Shipment == nil || len(out.Sources) != 3 {
				t.Fatalf("partial result: %+v", out)
			}
			for _, source := range out.Sources {
				if source.Name == "payment" {
					if source.Status != "unavailable" || source.Error == "" {
						t.Fatalf("missing failure: %+v", source)
					}
				} else if source.Status != "ok" || source.GeneratedAt == nil {
					t.Fatalf("healthy source: %+v", source)
				}
			}
			for source, q := range r.seen {
				if q.Get("vendor_id") != period.VendorID || q.Get("from") != "2026-09-30T17:00:00Z" || q.Get("group") != "month" {
					t.Fatalf("%s filters=%v", source, q)
				}
			}
			s.Roles = roles{err: apperror.Forbidden("not admin")}
			if _, err := s.BusinessReport(t.Context(), "buyer", "Bearer fake-report-token", period); err == nil {
				t.Fatal("non-admin read report")
			}
			s.BusinessReports = false
			if _, err := s.BusinessReport(t.Context(), "admin", "Bearer fake-report-token", period); err == nil {
				t.Fatal("disabled BFF served report")
			}
		})
	}
}
