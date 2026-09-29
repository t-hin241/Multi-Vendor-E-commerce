package authjwt_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"shopee/backend/pkg/authjwt"
)

func TestRemoteSessionVerifierFailsClosed(t *testing.T) {
	for _, status := range []int{204, 401, 403, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Identity-Service-Key") != "synthetic-key" {
					t.Error("missing service auth")
				}
				w.WriteHeader(status)
			}))
			defer server.Close()
			verify := authjwt.RemoteVerifier(server.URL, "synthetic-key")
			err := verify(context.Background(), &authjwt.Claims{UserID: "user", Role: "buyer", SessionID: "session"})
			if status == 204 && err != nil {
				t.Fatal(err)
			}
			if status == 401 && !errors.Is(err, authjwt.ErrInvalidToken) {
				t.Fatal("expected unauthorized")
			}
			if status >= 403 && !errors.Is(err, authjwt.ErrVerificationUnavailable) {
				t.Fatal("expected fail closed")
			}
			if verify(context.Background(), &authjwt.Claims{UserID: "legacy", Role: "buyer"}) == nil {
				t.Fatal("legacy token accepted")
			}
		})
	}
}
