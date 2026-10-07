package domain_test

import (
	"testing"
	"time"

	"shopee/backend/services/order/internal/domain"
)

func TestRuleReadinessOnlyForEnforcedRules(t *testing.T) {
	cases := []struct {
		key, value string
		ready      bool
	}{
		{domain.RuleReturnsWindow, "window-7d", true},
		{domain.RuleReturnsWindow, "window-365d", true},
		{domain.RuleReturnsWindow, "window-0d", false},
		{domain.RuleReturnsWindow, "window-366d", false},
		{domain.RuleReturnsWindow, "7 days", false},
		{domain.RuleReturnShippingRefund, "none", true},
		{domain.RuleReturnShippingRefund, "seller-fault-outbound", false},
		{"payment.settlement", "v1", false},
	}
	for _, tc := range cases {
		ready, hash, reason := domain.RuleReadiness(tc.key, tc.value)
		if ready != tc.ready || (ready && hash == "") || (!ready && reason == "") {
			t.Errorf("%s=%s: ready=%v hash=%q reason=%q", tc.key, tc.value, ready, hash, reason)
		}
	}
	a, b, _ := domain.RuleReadiness(domain.RuleReturnsWindow, "window-7d")
	_, c, _ := domain.RuleReadiness(domain.RuleReturnsWindow, "window-8d")
	if !a || b == c {
		t.Fatal("each rule version has its own hash")
	}
}

func TestActivePolicyAtTheUTCBoundary(t *testing.T) {
	boundary := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	versions := []domain.PolicyVersion{
		{PolicyID: "v1", Kind: "returns", Version: 1, EffectiveAt: boundary.Add(-30 * 24 * time.Hour)},
		{PolicyID: "v2", Kind: "returns", Version: 2, EffectiveAt: boundary},
	}
	if got := domain.ActiveAt(versions, boundary.Add(-time.Nanosecond))["returns"]; got.Version != 1 {
		t.Fatalf("just before the boundary v1 applies, got v%d", got.Version)
	}
	// 07:00 in Asia/Saigon is the same instant as 00:00 UTC.
	saigon := time.FixedZone("ICT", 7*3600)
	if got := domain.ActiveAt(versions, time.Date(2026, 11, 1, 7, 0, 0, 0, saigon))["returns"]; got.Version != 2 {
		t.Fatalf("at the boundary v2 applies whatever the time zone, got v%d", got.Version)
	}
}

func TestBuildPolicySnapshot(t *testing.T) {
	at := time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)
	fallback := domain.ReturnPolicy{Version: "window-7d", WindowDays: 7}
	s, err := domain.BuildPolicySnapshot(map[string]domain.PolicyVersion{}, fallback, at)
	if err != nil || s.Source != "config" || s.ReturnsWindowDays != 7 || s.ReturnShippingRefund != "none" {
		t.Fatalf("config snapshot: %v %+v", err, s)
	}
	active := map[string]domain.PolicyVersion{
		"returns": {PolicyID: "r", Kind: "returns", Version: 3, ContentHash: "h",
			RuleRefs: map[string]string{domain.RuleReturnsWindow: "window-14d", domain.RuleReturnShippingRefund: "none"}},
		"terms": {PolicyID: "t", Kind: "terms", Version: 2, ContentHash: "h2"},
	}
	s, err = domain.BuildPolicySnapshot(active, fallback, at)
	if err != nil || s.Source != "published" || s.ReturnsWindowDays != 14 || s.ReturnPolicyVersion != "returns-v3" ||
		s.Policies[0].Kind != "returns" || s.Policies[1].Kind != "terms" {
		t.Fatalf("published snapshot: %v %+v", err, s)
	}
	if !domain.SameVersions(map[string]int64{"returns": 3, "terms": 2}, s.VersionsByKind()) ||
		domain.SameVersions(map[string]int64{"returns": 3}, s.VersionsByKind()) {
		t.Fatal("acceptance must match every kind in force")
	}
	v := s.ForVendor(&domain.PolicyVersion{PolicyID: "shop-1", Kind: "shop", Version: 4, ContentHash: "hs"})
	if v.ShopPolicy == nil || v.ShopPolicy.Version != 4 || v.ReturnPolicy().WindowDays != 14 {
		t.Fatalf("vendor snapshot: %+v", v)
	}
	active["returns"] = domain.PolicyVersion{Kind: "returns", Version: 4, RuleRefs: map[string]string{domain.RuleReturnsWindow: "window-14d", domain.RuleReturnShippingRefund: "buyer-friendly"}}
	if _, err := domain.BuildPolicySnapshot(active, fallback, at); err == nil {
		t.Fatal("a returns policy citing an unenforced rule can never be snapshotted")
	}
}
