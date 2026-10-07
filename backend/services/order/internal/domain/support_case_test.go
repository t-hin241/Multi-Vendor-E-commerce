package domain_test

import (
	"strings"
	"testing"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/order/internal/domain"
)

func TestSupportCaseTransitions(t *testing.T) {
	allowed := []struct{ from, to domain.SupportCaseStatus }{
		{domain.CaseOpen, domain.CaseInProgress},
		{domain.CaseInProgress, domain.CaseWaitingBuyer},
		{domain.CaseWaitingBuyer, domain.CaseInProgress},
		{domain.CaseWaitingVendor, domain.CaseInProgress},
		{domain.CaseInProgress, domain.CaseResolutionPending},
		{domain.CaseResolutionPending, domain.CaseResolved},
		{domain.CaseResolutionPending, domain.CaseInProgress},
		{domain.CaseResolved, domain.CaseInProgress},
		{domain.CaseResolved, domain.CaseClosed},
	}
	for _, tc := range allowed {
		if !domain.CanTransitionCase(tc.from, tc.to) {
			t.Errorf("%s -> %s must be allowed", tc.from, tc.to)
		}
	}
	refused := []struct{ from, to domain.SupportCaseStatus }{
		{domain.CaseOpen, domain.CaseResolved},
		{domain.CaseOpen, domain.CaseClosed},
		{domain.CaseInProgress, domain.CaseClosed},
		{domain.CaseResolutionPending, domain.CaseClosed},
		{domain.CaseClosed, domain.CaseInProgress},
		{domain.CaseClosed, domain.CaseOpen},
	}
	for _, tc := range refused {
		if domain.CanTransitionCase(tc.from, tc.to) {
			t.Errorf("%s -> %s must be refused", tc.from, tc.to)
		}
	}
}

func TestSupportTextIsPlainAndBounded(t *testing.T) {
	p := domain.DefaultSupportPolicy()
	if got, err := p.ValidateSupportText("  Giao sau < 5 ngày\nvẫn chưa tới  "); err != nil || got != "Giao sau < 5 ngày\nvẫn chưa tới" {
		t.Fatalf("plain text with a lone < is fine, got %q %v", got, err)
	}
	if _, err := p.ValidateSupportText(strings.Repeat("ă", 4000)); err != nil {
		t.Fatalf("4000 characters (not bytes) are allowed: %v", err)
	}
	for _, bad := range []string{"", "   ", strings.Repeat("a", 4001), "<b>hi</b>", "x <img src=x onerror=1>", "<!-- c -->", "bell\x07"} {
		if _, err := p.ValidateSupportText(bad); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}

func TestSupportEligibilityAndDeadlines(t *testing.T) {
	if err := domain.CheckCaseEligibility(domain.CategoryNotReceived, domain.StatusPendingPayment); err == nil {
		t.Fatal("goods complaints need a paid order")
	}
	if err := domain.CheckCaseEligibility(domain.CategoryPaymentIssue, domain.StatusPendingPayment); err != nil {
		t.Fatal("a payment question is allowed before the order is paid")
	}
	if domain.CategoryOther.AffectsMoney() || !domain.CategoryWrongItems.AffectsMoney() {
		t.Fatal("only money-related categories hold the payout")
	}
	if _, err := domain.ParseSupportCategory("refund_me"); err == nil {
		t.Fatal("unknown category")
	}
	p := domain.DefaultSupportPolicy()
	now := time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)
	if due := p.DueAt(domain.CaseWaitingVendor, now); due == nil || !due.Equal(now.Add(48*time.Hour)) {
		t.Fatalf("the shop has 48h, got %v", due)
	}
	for _, s := range []domain.SupportCaseStatus{domain.CaseWaitingBuyer, domain.CaseResolutionPending, domain.CaseResolved, domain.CaseClosed} {
		if p.DueAt(s, now) != nil {
			t.Errorf("no marketplace deadline in %s", s)
		}
	}
	resolved := now.Add(-7*24*time.Hour + time.Minute)
	c := &domain.SupportCase{Status: domain.CaseResolved, ResolvedAt: &resolved}
	if err := p.CanReopen(c, now); err != nil || p.ReopenExpired(c, now) {
		t.Fatalf("still inside the window: %v", err)
	}
	if err := p.CanReopen(c, now.Add(2*time.Minute)); err == nil || err.(*apperror.Error).Code != apperror.CodeConflict {
		t.Fatalf("past the window: %v", err)
	}
}
