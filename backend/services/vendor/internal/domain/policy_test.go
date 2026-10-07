package domain_test

import (
	"strings"
	"testing"
	"time"

	"shopee/backend/services/vendorsvc/internal/domain"
)

func returnsInput() domain.PolicyInput {
	return domain.PolicyInput{Kind: "returns", Title: "Đổi trả", Summary: "7 ngày", Content: "Chi tiết", Contact: "support@example.test",
		EffectiveAt: time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC),
		RuleRefs:    map[string]string{"order.returns_window": "window-7d", "order.return_shipping_refund": "none"}}
}

func TestMarketplacePolicyValidation(t *testing.T) {
	p, err := domain.NewMarketplacePolicy(returnsInput(), "admin")
	if err != nil || p.ContentHash == "" || p.Status != domain.PolicyDraft {
		t.Fatalf("valid draft: %v %+v", err, p)
	}
	again, _ := domain.NewMarketplacePolicy(returnsInput(), "other-admin")
	if again.ContentHash != p.ContentHash {
		t.Fatal("the hash covers the content, not who wrote it")
	}
	changed := returnsInput()
	changed.RuleRefs["order.returns_window"] = "window-14d"
	if c, _ := domain.NewMarketplacePolicy(changed, "admin"); c.ContentHash == p.ContentHash {
		t.Fatal("a different rule is a different policy")
	}

	cases := map[string]func(*domain.PolicyInput){
		"html":             func(in *domain.PolicyInput) { in.Content = "<script>x</script>" },
		"unknown kind":     func(in *domain.PolicyInput) { in.Kind = "cookies" },
		"missing contact":  func(in *domain.PolicyInput) { in.Contact = " " },
		"no effective_at":  func(in *domain.PolicyInput) { in.EffectiveAt = time.Time{} },
		"unknown rule":     func(in *domain.PolicyInput) { in.RuleRefs["payment.settlement"] = "v1" },
		"missing rule":     func(in *domain.PolicyInput) { delete(in.RuleRefs, "order.return_shipping_refund") },
		"bad rule version": func(in *domain.PolicyInput) { in.RuleRefs["order.returns_window"] = "Seven Days" },
	}
	for name, mutate := range cases {
		in := returnsInput()
		mutate(&in)
		if _, err := domain.NewMarketplacePolicy(in, "admin"); err == nil {
			t.Errorf("%s must be refused", name)
		}
	}
	terms := returnsInput()
	terms.Kind = "terms"
	if _, err := domain.NewMarketplacePolicy(terms, "admin"); err == nil {
		t.Fatal("a terms policy cannot cite return rules")
	}
	terms.RuleRefs = nil
	if _, err := domain.NewMarketplacePolicy(terms, "admin"); err != nil {
		t.Fatalf("terms without rules: %v", err)
	}
}

func TestPublishWindowAndReadiness(t *testing.T) {
	p, _ := domain.NewMarketplacePolicy(returnsInput(), "admin")
	if err := p.CanPublish(p.EffectiveAt.Add(30 * time.Second)); err != nil {
		t.Fatalf("a small clock skew is tolerated: %v", err)
	}
	if err := p.CanPublish(p.EffectiveAt.Add(2 * time.Minute)); err == nil {
		t.Fatal("a draft cannot claim it applied in the past")
	}
	p.Readiness = map[string]domain.RuleReadiness{"order.returns_window": {Ready: true, RuleHash: "h"}}
	if p.AllReady() {
		t.Fatal("every cited rule must be acknowledged")
	}
	p.Readiness["order.return_shipping_refund"] = domain.RuleReadiness{Ready: true, RuleHash: "h2"}
	if !p.AllReady() {
		t.Fatal("all acknowledged")
	}
	p.Status = domain.PolicyPublished
	if err := p.CanPublish(p.EffectiveAt); err == nil {
		t.Fatal("published once")
	}
}

func TestShopPolicyCannotLowerProtection(t *testing.T) {
	for _, text := range []string{"Hàng đã mua KHÔNG ĐỔI TRẢ", "không   hoàn tiền với hàng sale", "All sales are final.", "Non-refundable items"} {
		if _, err := domain.ValidateShopPolicy(text); err == nil {
			t.Errorf("%q lowers the marketplace protection", text)
		}
	}
	text, err := domain.ValidateShopPolicy("  Shop hỗ trợ đổi size trong 3 ngày và đóng gói chống sốc.  ")
	if err != nil || strings.HasPrefix(text, " ") {
		t.Fatalf("an addition is fine: %v %q", err, text)
	}
	if _, err := domain.ValidateShopPolicy(strings.Repeat("a", 10001)); err == nil {
		t.Fatal("too long")
	}
}
