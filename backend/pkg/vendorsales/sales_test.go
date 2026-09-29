package vendorsales

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestApprovedRequiresEveryLiveShop(t *testing.T) {
	const first = "00000000-0000-0000-0000-000000000001"
	const second = "00000000-0000-0000-0000-000000000002"
	for _, tc := range []struct {
		name, body string
		status     int
		ok         bool
	}{
		{"approved", `{"data":[{"vendor_id":"` + first + `","status":"approved","version":3},{"vendor_id":"` + second + `","status":"approved","version":2}]}`, 200, true},
		{"missing", `{"data":[{"vendor_id":"` + first + `","status":"approved","version":3}]}`, 200, false},
		{"suspended", `{"data":[{"vendor_id":"` + first + `","status":"approved","version":3},{"vendor_id":"` + second + `","status":"suspended","version":2}]}`, 200, false},
		{"outage", `{}`, 503, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Identity-Service-Key") != "test-key" {
					t.Error("missing authentication")
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer s.Close()
			_, err := (Client{URL: s.URL, Key: "test-key"}).Approved(t.Context(), []string{first, second})
			if (err == nil) != tc.ok {
				t.Fatal("unexpected selling decision", err)
			}
		})
	}
}
