package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"time"

	"shopee/backend/pkg/apperror"
)

// Rules Order enforces and may acknowledge for a published policy (AF-02).
const (
	// RuleReturnsWindow: how many days after delivery (completion) a return
	// may be requested, "window-<days>d".
	RuleReturnsWindow = "order.returns_window"
	// RuleReturnShippingRefund: which shipping fee a return refunds. Only
	// "none" (legacy: shipping is never refunded on a return) is
	// implemented; any other value is not ready until Order refunds it.
	RuleReturnShippingRefund = "order.return_shipping_refund"

	ReturnShippingNone = "none"

	// ruleImplementation names the code that enforces the rules; a rule
	// hash changes when the implementation does.
	ruleImplementation = "order-policy-rules-v1"
)

var returnsWindowPattern = regexp.MustCompile(`^window-([0-9]{1,3})d$`)

// ParseReturnsWindow reads "window-7d" (1 to 365 days).
func ParseReturnsWindow(value string) (int, bool) {
	m := returnsWindowPattern.FindStringSubmatch(value)
	if m == nil {
		return 0, false
	}
	days, err := strconv.Atoi(m[1])
	if err != nil || days < 1 || days > 365 {
		return 0, false
	}
	return days, true
}

// RuleReadiness tells Vendor whether Order enforces this rule version. A
// ready answer carries a hash of the rule and its implementation.
func RuleReadiness(key, value string) (ready bool, ruleHash, reason string) {
	switch key {
	case RuleReturnsWindow:
		if _, ok := ParseReturnsWindow(value); !ok {
			return false, "", "Order enforces return windows written window-<1..365>d"
		}
	case RuleReturnShippingRefund:
		if value != ReturnShippingNone {
			return false, "", "Order does not refund shipping on returns yet; only \"none\" is enforced"
		}
	default:
		return false, "", "Order does not own rule " + key
	}
	sum := sha256.Sum256([]byte(ruleImplementation + "|" + key + "=" + value))
	return true, hex.EncodeToString(sum[:]), ""
}

// PolicyVersion is a published policy version as Order knows it (read
// model of vendor.policy_published). Immutable.
type PolicyVersion struct {
	PolicyID    string
	Scope       string // marketplace | shop
	VendorID    string
	Kind        string
	Version     int64
	ContentHash string
	RuleRefs    map[string]string
	EffectiveAt time.Time
}

// ValidatePolicyVersion checks an incoming publication.
func ValidatePolicyVersion(v PolicyVersion) error {
	switch {
	case v.PolicyID == "" || v.Kind == "" || v.Version < 1 || v.ContentHash == "" || v.EffectiveAt.IsZero():
		return apperror.Validation("Invalid policy publication")
	case v.Scope == "shop" && v.VendorID == "":
		return apperror.Validation("A shop policy names its shop")
	case v.Scope != "shop" && v.Scope != "marketplace":
		return apperror.Validation("Invalid policy scope")
	}
	if v.Scope == "marketplace" && v.Kind == "returns" {
		if ready, _, _ := RuleReadiness(RuleReturnsWindow, v.RuleRefs[RuleReturnsWindow]); !ready {
			return apperror.Validation("A returns policy must cite an enforced return window")
		}
		if ready, _, _ := RuleReadiness(RuleReturnShippingRefund, v.RuleRefs[RuleReturnShippingRefund]); !ready {
			return apperror.Validation("A returns policy must cite an enforced return shipping rule")
		}
	}
	return nil
}

// ActiveAt picks, per kind, the version in force at t: the latest
// effective_at not after t.
func ActiveAt(versions []PolicyVersion, t time.Time) map[string]PolicyVersion {
	out := map[string]PolicyVersion{}
	for _, v := range versions {
		if v.EffectiveAt.After(t) {
			continue
		}
		if current, ok := out[v.Kind]; !ok || v.EffectiveAt.After(current.EffectiveAt) {
			out[v.Kind] = v
		}
	}
	return out
}

// PolicyRef names one version a buyer agreed to.
type PolicyRef struct {
	Kind        string `json:"kind"`
	PolicyID    string `json:"policy_id"`
	Version     int64  `json:"version"`
	ContentHash string `json:"content_hash"`
}

func refOf(v PolicyVersion) PolicyRef {
	return PolicyRef{Kind: v.Kind, PolicyID: v.PolicyID, Version: v.Version, ContentHash: v.ContentHash}
}

// OrderPolicySnapshot is what an order was placed under. Source "config"
// means no returns policy was published yet and the configured rules
// applied (still explicit, so later changes of the config do not move it).
type OrderPolicySnapshot struct {
	Source               string            `json:"source"`
	Policies             []PolicyRef       `json:"policies"`
	Rules                map[string]string `json:"rules"`
	ReturnsWindowDays    int               `json:"returns_window_days"`
	ReturnShippingRefund string            `json:"return_shipping_refund"`
	ReturnPolicyVersion  string            `json:"return_policy_version"`
	TakenAt              time.Time         `json:"taken_at"`
}

// VendorPolicySnapshot is the part a vendor order enforces: the shop's
// approved policy (if any) and the return rules.
type VendorPolicySnapshot struct {
	ShopPolicy           *PolicyRef `json:"shop_policy,omitempty"`
	ReturnsWindowDays    int        `json:"returns_window_days"`
	ReturnShippingRefund string     `json:"return_shipping_refund"`
	ReturnPolicyVersion  string     `json:"return_policy_version"`
}

// BuildPolicySnapshot fixes the marketplace versions in force at t and the
// return rules they cite. Without a published returns policy the
// configured rule (fallback) applies.
func BuildPolicySnapshot(active map[string]PolicyVersion, fallback ReturnPolicy, t time.Time) (*OrderPolicySnapshot, error) {
	s := &OrderPolicySnapshot{Source: "config", Policies: []PolicyRef{}, Rules: map[string]string{}, ReturnsWindowDays: fallback.WindowDays,
		ReturnShippingRefund: ReturnShippingNone, ReturnPolicyVersion: fallback.Version, TakenAt: t.UTC()}
	kinds := make([]string, 0, len(active))
	for k := range active {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		s.Policies = append(s.Policies, refOf(active[k]))
	}
	if returns, ok := active["returns"]; ok {
		days, ok := ParseReturnsWindow(returns.RuleRefs[RuleReturnsWindow])
		shipping := returns.RuleRefs[RuleReturnShippingRefund]
		if !ok || shipping != ReturnShippingNone {
			return nil, apperror.Internal(errUnenforcedPolicy)
		}
		s.Source, s.ReturnsWindowDays, s.ReturnShippingRefund = "published", days, shipping
		s.ReturnPolicyVersion = "returns-v" + strconv.FormatInt(returns.Version, 10)
		s.Rules[RuleReturnsWindow], s.Rules[RuleReturnShippingRefund] = returns.RuleRefs[RuleReturnsWindow], shipping
	}
	return s, nil
}

var errUnenforcedPolicy = errors.New("the active returns policy cites a rule Order does not enforce")

// VersionsByKind is what the buyer is shown and must confirm.
func (s *OrderPolicySnapshot) VersionsByKind() map[string]int64 {
	out := map[string]int64{}
	for _, p := range s.Policies {
		out[p.Kind] = p.Version
	}
	return out
}

// SameVersions compares the versions a buyer accepted with those in force.
func SameVersions(accepted, current map[string]int64) bool {
	if len(accepted) != len(current) {
		return false
	}
	for k, v := range current {
		if accepted[k] != v {
			return false
		}
	}
	return true
}

// ForVendor derives a vendor order's snapshot.
func (s *OrderPolicySnapshot) ForVendor(shop *PolicyVersion) *VendorPolicySnapshot {
	v := &VendorPolicySnapshot{ReturnsWindowDays: s.ReturnsWindowDays, ReturnShippingRefund: s.ReturnShippingRefund,
		ReturnPolicyVersion: s.ReturnPolicyVersion}
	if shop != nil {
		ref := refOf(*shop)
		v.ShopPolicy = &ref
	}
	return v
}

// ReturnPolicy is the return rule the vendor order was sold under.
func (v *VendorPolicySnapshot) ReturnPolicy() ReturnPolicy {
	return ReturnPolicy{Version: v.ReturnPolicyVersion, WindowDays: v.ReturnsWindowDays}
}

// CodePolicyChanged: the policies in force changed since the buyer
// reviewed them.
const CodePolicyChanged apperror.Code = "policy_changed"

func PolicyChanged() *apperror.Error {
	return coded(CodePolicyChanged, http.StatusConflict,
		"The marketplace policies changed since you reviewed your order. Please review them and confirm again.")
}
