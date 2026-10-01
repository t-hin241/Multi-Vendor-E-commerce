package domain_test

import (
	"strings"
	"testing"
	"time"

	"shopee/backend/services/notification/internal/domain"
)

const user = "11111111-1111-1111-1111-111111111111"

func TestNewNotificationValidatesAndDeduplicates(t *testing.T) {
	now := time.Now()
	a, err := domain.NewNotification(domain.Request{EventID: "effect-1", Source: "order", UserID: user, Type: domain.TypeOrderPaid, ReferenceID: "order-1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := domain.NewNotification(domain.Request{EventID: "effect-1", Source: "order", UserID: user, Type: domain.TypeOrderPaid, ReferenceID: "order-1"}, now)
	other, _ := domain.NewNotification(domain.Request{EventID: "effect-2", Source: "order", UserID: user, Type: domain.TypeOrderPaid, ReferenceID: "order-1"}, now)
	if a.DedupKey != b.DedupKey || a.DedupKey == other.DedupKey {
		t.Fatalf("same event must share a key, another event must not: %s %s", a.DedupKey, other.DedupKey)
	}
	if a.Status != domain.StatusPending || a.TemplateVersion != "v1" || a.MaxAttempts != domain.DefaultMaxAttempts {
		t.Fatalf("unexpected new notification %+v", a)
	}
	legacy, _ := domain.NewNotification(domain.Request{UserID: user, Type: domain.TypeOrderPaid, ReferenceID: "order-1"}, now)
	if legacy.EventID != "order_paid:order-1" {
		t.Fatalf("without an event id the event is type and reference, got %s", legacy.EventID)
	}
	for _, bad := range []domain.Request{
		{UserID: user, Type: "marketing_blast", ReferenceID: "x"},
		{UserID: user, Type: domain.TypeOrderPaid, ReferenceID: "has space"},
		{UserID: user, Type: domain.TypeOrderPaid, ReferenceID: "x", EventID: "bad id"},
		{UserID: user, Type: domain.TypeOrderPaid, ReferenceID: "x", Source: "Order!"},
	} {
		if _, err := domain.NewNotification(bad, now); err == nil {
			t.Errorf("expected %+v to be refused", bad)
		}
	}
}

func TestBackoffGrowsAndIsCapped(t *testing.T) {
	if domain.Backoff(1) != 30*time.Second || domain.Backoff(2) != time.Minute || domain.Backoff(30) != time.Hour {
		t.Fatal("unexpected backoff")
	}
}

func TestMaskEmail(t *testing.T) {
	if got := domain.MaskEmail("buyer@example.invalid"); got != "b***@example.invalid" {
		t.Fatal(got)
	}
	if got := domain.MaskEmail("not-an-address"); got != "***" {
		t.Fatal(got)
	}
}

// Every type renders; a refund message only claims a confirmed refund and
// no message carries a link or token.
func TestTemplatesRender(t *testing.T) {
	for _, typ := range []domain.Type{domain.TypeOrderPaid, domain.TypeOrderShipped, domain.TypeOrderCompleted, domain.TypeOrderCancelled,
		domain.TypeOrderRefunded, domain.TypeVendorApproved, domain.TypeVendorRejected} {
		n, err := domain.NewNotification(domain.Request{UserID: user, Type: typ, ReferenceID: "0123456789abcdef"}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		subject, body, err := domain.Render(n, "Nguyen\r\nBcc: x@example.invalid")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(subject+body, "01234567") || strings.Contains(body, "http") || strings.ContainsAny(subject, "\r\n") {
			t.Fatalf("%s renders badly: %q %q", typ, subject, body)
		}
		if strings.Contains(body, "\r") || strings.Contains(body, "NguyenBcc") == false {
			t.Fatalf("control characters must be removed from the name: %q", body)
		}
	}
	n, _ := domain.NewNotification(domain.Request{UserID: user, Type: domain.TypeOrderRefunded, ReferenceID: "order-1"}, time.Now())
	if _, body, _ := domain.Render(n, ""); !strings.Contains(body, "đã được xác nhận") {
		t.Fatalf("refund wording: %s", body)
	}
	n.TemplateVersion = "v0"
	if _, _, err := domain.Render(n, ""); err == nil {
		t.Fatal("an unknown template version must not render")
	}
}
