package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"shopee/backend/pkg/apperror"
)

// PolicyKind is one marketplace policy document.
type PolicyKind string

const (
	PolicyReturns  PolicyKind = "returns"
	PolicyShipping PolicyKind = "shipping"
	PolicyTerms    PolicyKind = "terms"
	PolicyPrivacy  PolicyKind = "privacy"
)

var PolicyKinds = []PolicyKind{PolicyReturns, PolicyShipping, PolicyTerms, PolicyPrivacy}

func ParsePolicyKind(s string) (PolicyKind, error) {
	for _, k := range PolicyKinds {
		if string(k) == s {
			return k, nil
		}
	}
	return "", apperror.Validation("kind must be returns, shipping, terms or privacy")
}

type PolicyStatus string

const (
	PolicyDraft     PolicyStatus = "draft"
	PolicyPreparing PolicyStatus = "preparing"
	PolicyPublished PolicyStatus = "published"
	PolicyWithdrawn PolicyStatus = "withdrawn"
)

// RuleReadiness is a rule owner's answer for one rule version.
type RuleReadiness struct {
	Ready    bool   `json:"ready"`
	RuleHash string `json:"rule_hash,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// MarketplacePolicy is one version of a marketplace policy. Its content and
// rule references are fixed when the draft is created; publishing only
// changes its status.
type MarketplacePolicy struct {
	ID                string
	Seq               int64
	Kind              PolicyKind
	Version           int
	Title             string
	Summary           string
	Content           string
	Contact           string
	RuleRefs          map[string]string
	EffectiveAt       time.Time
	Status            PolicyStatus
	ContentHash       string
	Readiness         map[string]RuleReadiness
	PublicationReason *string
	CreatedBy         string
	PublishedBy       *string
	PreparingSince    *time.Time
	PublishedAt       *time.Time
	RowVersion        int64
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// ruleCatalog lists the rule references a policy may cite in this version:
// which kind of policy describes it and which service enforces it. A rule
// owned by a service without a readiness contract yet (Payment, Shipment)
// cannot be cited: the text could promise what no code enforces.
var ruleCatalog = map[string]struct {
	Kind  PolicyKind
	Owner string
}{
	"order.returns_window":         {PolicyReturns, "order"},
	"order.return_shipping_refund": {PolicyReturns, "order"},
}

// requiredRules must be cited by a policy of that kind, so the published
// text always names the rule versions Order enforces.
var requiredRules = map[PolicyKind][]string{
	PolicyReturns: {"order.returns_window", "order.return_shipping_refund"},
}

// RuleOwner is the service that enforces a rule reference.
func RuleOwner(key string) string { return ruleCatalog[key].Owner }

var ruleValuePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,99}$`)

// PolicyInput is an admin's new version, before validation.
type PolicyInput struct {
	Kind        string
	Title       string
	Summary     string
	Content     string
	Contact     string
	RuleRefs    map[string]string
	EffectiveAt time.Time
}

// NewMarketplacePolicy validates a draft and fixes its content hash.
func NewMarketplacePolicy(d PolicyInput, createdBy string) (*MarketplacePolicy, error) {
	kind, err := ParsePolicyKind(d.Kind)
	if err != nil {
		return nil, err
	}
	p := &MarketplacePolicy{Kind: kind, Status: PolicyDraft, CreatedBy: createdBy, RuleRefs: map[string]string{}, EffectiveAt: d.EffectiveAt.UTC()}
	if p.Title, err = plainText(d.Title, 200, "title"); err != nil {
		return nil, err
	}
	if p.Summary, err = plainText(d.Summary, 1000, "summary"); err != nil {
		return nil, err
	}
	if p.Content, err = plainText(d.Content, 50000, "content"); err != nil {
		return nil, err
	}
	if p.Contact, err = plainText(d.Contact, 1000, "contact"); err != nil {
		return nil, err
	}
	if d.EffectiveAt.IsZero() {
		return nil, apperror.Validation("effective_at is required")
	}
	for key, value := range d.RuleRefs {
		rule, ok := ruleCatalog[key]
		if !ok {
			return nil, apperror.Validation("Unknown rule reference " + key)
		}
		if rule.Kind != kind {
			return nil, apperror.Validation("Rule " + key + " belongs to the " + string(rule.Kind) + " policy")
		}
		if !ruleValuePattern.MatchString(value) {
			return nil, apperror.Validation("Rule " + key + " needs a version such as window-7d")
		}
		p.RuleRefs[key] = value
	}
	for _, key := range requiredRules[kind] {
		if _, ok := p.RuleRefs[key]; !ok {
			return nil, apperror.Validation("A " + string(kind) + " policy must cite rule " + key)
		}
	}
	p.ContentHash = PolicyHash(p)
	return p, nil
}

// PolicyHash fingerprints what a buyer agrees to: text and rule versions.
func PolicyHash(p *MarketplacePolicy) string {
	keys := make([]string, 0, len(p.RuleRefs))
	for k := range p.RuleRefs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	refs := make([][2]string, 0, len(keys))
	for _, k := range keys {
		refs = append(refs, [2]string{k, p.RuleRefs[k]})
	}
	b, _ := json.Marshal([]any{p.Kind, p.Title, p.Summary, p.Content, p.Contact, refs})
	return TextHash(string(b))
}

func TextHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// CanPublish: a draft or a version still waiting for its rule owners, not
// starting in the past (an order already placed keeps the version it was
// placed under; a version cannot claim it applied earlier).
func (p *MarketplacePolicy) CanPublish(now time.Time) error {
	if p.Status != PolicyDraft && p.Status != PolicyPreparing {
		return apperror.Conflict("Only a draft or a preparing version can be published")
	}
	if p.Status == PolicyDraft && p.EffectiveAt.Before(now.Add(-time.Minute)) {
		return apperror.Validation("effective_at is in the past; create a new draft that starts now or later")
	}
	return nil
}

// AllReady reports whether every cited rule was acknowledged.
func (p *MarketplacePolicy) AllReady() bool {
	for key := range p.RuleRefs {
		if r, ok := p.Readiness[key]; !ok || !r.Ready {
			return false
		}
	}
	return true
}

// ShopPolicyStatus is a shop policy version's review state.
type ShopPolicyStatus string

const (
	ShopPolicyProposed ShopPolicyStatus = "proposed"
	ShopPolicyApproved ShopPolicyStatus = "approved"
	ShopPolicyRejected ShopPolicyStatus = "rejected"
)

// ShopPolicy is a shop's addition to the marketplace policies, public once
// an admin approved it.
type ShopPolicy struct {
	ID             string
	Seq            int64
	VendorID       string
	Version        int
	Content        string
	ContentHash    string
	Status         ShopPolicyStatus
	Source         string
	ProposedBy     *string
	DecidedBy      *string
	DecisionReason *string
	DecidedAt      *time.Time
	CreatedAt      time.Time
}

// protectionCuts are phrases that take away a buyer protection the
// marketplace guarantees. A shop text containing one is refused outright;
// subtler contradictions are for the reviewing admin.
var protectionCuts = []string{
	"không đổi trả", "không nhận đổi trả", "không nhận trả hàng", "không chấp nhận trả hàng", "không chấp nhận đổi trả",
	"miễn đổi trả", "không hoàn tiền", "không hoàn lại tiền", "không hoàn trả tiền", "không bồi hoàn",
	"no refund", "no return", "non-refundable", "all sales final", "all sales are final",
}

// ValidateShopPolicy checks a shop's proposed text: plain text within
// limits that does not lower the marketplace's minimum protection.
func ValidateShopPolicy(content string) (string, error) {
	text, err := plainText(content, 10000, "content")
	if err != nil {
		return "", err
	}
	lower := strings.ToLower(strings.Join(strings.Fields(text), " "))
	for _, phrase := range protectionCuts {
		if strings.Contains(lower, phrase) {
			return "", ReducesProtection(phrase)
		}
	}
	return text, nil
}

var markupPattern = regexp.MustCompile(`<[A-Za-z/!?]`)

// plainText trims text and refuses empty, over-long, HTML or control
// characters: policies are shown as plain text.
func plainText(s string, max int, field string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", apperror.Validation(field + " is required")
	}
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > max {
		return "", apperror.Validation(field + " is too long")
	}
	if markupPattern.MatchString(s) {
		return "", apperror.Validation(field + " must be plain text; HTML is not accepted")
	}
	for _, r := range s {
		if unicode.IsControl(r) && r != '\n' && r != '\t' && r != '\r' {
			return "", apperror.Validation(field + " contains invalid characters")
		}
	}
	return s, nil
}

// Error codes of the policy API.
const (
	CodeReducesProtection apperror.Code = "policy_reduces_protection"
	CodeVersionConflict   apperror.Code = "version_conflict"
	CodePoliciesDisabled  apperror.Code = "policies_disabled"
)

func ReducesProtection(phrase string) *apperror.Error {
	return &apperror.Error{Code: CodeReducesProtection, Status: http.StatusUnprocessableEntity,
		Message: "Nội dung làm giảm bảo vệ tối thiểu của sàn (\"" + phrase + "\"). Shop chỉ được bổ sung, không được bớt quyền của người mua."}
}

func VersionConflict() *apperror.Error {
	return &apperror.Error{Code: CodeVersionConflict, Status: http.StatusConflict, Message: "This policy changed since you loaded it; reload and try again"}
}

func PoliciesDisabled() *apperror.Error {
	return &apperror.Error{Code: CodePoliciesDisabled, Status: http.StatusForbidden, Message: "Versioned policies are not enabled"}
}
