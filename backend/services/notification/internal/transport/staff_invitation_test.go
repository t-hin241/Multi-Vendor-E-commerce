package transport

import (
	"bytes"
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/serviceauth"
	"shopee/backend/services/notification/internal/domain"
)

type capturedInvitations struct {
	sent []domain.StaffInvitation
	fail bool
}

func (c *capturedInvitations) SendInvitation(_ context.Context, m domain.StaffInvitation) error {
	if c.fail {
		return errors.New("mail relay down")
	}
	c.sent = append(c.sent, m)
	return nil
}

func TestStaffInvitationDeliveryIsInternalAndValidated(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	const key = "fake-test-internal-key-not-a-real-secret"
	good := `{"delivery_id":"00000000-0000-4000-8000-000000000001","email":"clerk@example.test","shop_name":"Test shop",
		"url":"https://shop.example.invalid/staff-invitations/accept#token=synthetic","expires_at":"2026-10-14T09:00:00Z"}`
	var logs bytes.Buffer
	sender := &capturedInvitations{}
	r := gin.New()
	RegisterStaffInvitation(r, serviceauth.SharedKey(key), sender, zerolog.New(&logs))
	send := func(body string, withKey bool) int {
		req := httptest.NewRequest("POST", "/internal/notifications/staff-invitations", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if withKey {
			req.Header.Set(serviceauth.Header, key)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}
	if code := send(good, false); code != 403 {
		t.Fatalf("unauthenticated caller: %d", code)
	}
	for _, bad := range []string{
		strings.Replace(good, "#token=synthetic", "?token=synthetic", 1),
		strings.Replace(good, "00000000-0000-4000-8000-000000000001", "x", 1),
		strings.Replace(good, `"email":"clerk@example.test",`, "", 1),
	} {
		if code := send(bad, true); code != 400 {
			t.Fatalf("invalid delivery accepted: %d", code)
		}
	}
	if code := send(good, true); code != 204 || len(sender.sent) != 1 || sender.sent[0].ShopName != "Test shop" {
		t.Fatalf("invitation not sent: %d", len(sender.sent))
	}
	sender.fail = true
	if code := send(good, true); code != 503 {
		t.Fatalf("failed send must ask Vendor to retry: %d", code)
	}
	if strings.Contains(logs.String(), "synthetic") || strings.Contains(logs.String(), "clerk@") {
		t.Fatal("link or address logged")
	}
}
