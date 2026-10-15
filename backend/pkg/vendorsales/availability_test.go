package vendorsales

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"shopee/backend/pkg/apperror"
)

func TestVacationClientSeparatesModerationFromNewSales(t *testing.T) {
	const shop = "00000000-0000-4000-8000-000000000001"
	for _, tc := range []struct {
		name, field string
		paused      bool
	}{{"legacy producer", "", false}, {"open", `,"accepting_orders":true`, false}, {"vacation", `,"accepting_orders":false`, true}} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("availability") != "1" || r.URL.Query().Get("ids") != shop {
					t.Error("missing availability-aware query")
				}
				fmt.Fprintf(w, `{"data":[{"vendor_id":"%s","status":"approved","version":3%s}]}`, shop, tc.field)
			}))
			defer s.Close()
			c := Client{URL: s.URL, Key: "fake-test-key"}
			versions, err := c.Approved(t.Context(), []string{shop})
			if err != nil || versions[shop] != 3 {
				t.Fatalf("moderation: %v %v", versions, err)
			}
			versions, err = c.Accepting(t.Context(), []string{shop})
			if tc.paused {
				var app *apperror.Error
				if !errors.As(err, &app) || app.Code != "vendor_not_accepting_orders" || app.Status != 409 {
					t.Fatalf("pause: %v", err)
				}
			} else if err != nil || versions[shop] != 3 {
				t.Fatalf("open: %v %v", versions, err)
			}
		})
	}
}
