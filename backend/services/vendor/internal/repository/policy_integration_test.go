package repository_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/events"
	"shopee/backend/services/vendorsvc/internal/domain"
	"shopee/backend/services/vendorsvc/internal/repository"
	"shopee/backend/services/vendorsvc/internal/usecase"
)

// rulesStub answers readiness like a rule owner; ready flips it.
type rulesStub struct {
	mu    sync.Mutex
	ready bool
	asked int
}

func (r *rulesStub) Readiness(_ context.Context, owner, key, value string) domain.RuleReadiness {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.asked++
	if owner != "order" || !r.ready {
		return domain.RuleReadiness{Reason: "order unreachable"}
	}
	return domain.RuleReadiness{Ready: true, RuleHash: "hash-" + key + "-" + value}
}

func policySetup(t *testing.T) (*fixture, *usecase.PolicyUseCase, *rulesStub, *time.Time) {
	f := setup(t)
	now := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	rules := &rulesStub{}
	uc := &usecase.PolicyUseCase{Policies: repository.PolicyRepository{Pool: f.db}, Vendors: f.vendors, Audit: f.audit,
		Notices: repository.NotificationOutbox{Pool: f.db}, Rules: rules, Ops: f.ops, Enabled: true, Log: zerolog.Nop(),
		Now: func() time.Time { return now }}
	return f, uc, rules, &now
}

func returnsDraft(effective time.Time, window string) domain.PolicyInput {
	return domain.PolicyInput{Kind: "returns", Title: "Chính sách đổi trả", Summary: "Trả hàng trong 7 ngày", Content: "Nội dung chi tiết",
		Contact: "support@example.test · 8:00–17:00", EffectiveAt: effective,
		RuleRefs: map[string]string{"order.returns_window": window, "order.return_shipping_refund": "none"}}
}

func expectAppCode(t *testing.T, err error, code apperror.Code) {
	t.Helper()
	var app *apperror.Error
	if !errors.As(err, &app) || app.Code != code {
		t.Fatalf("expected %s, got %v", code, err)
	}
}

// AF-02: a version is published only once its rule owner acknowledged the
// rules it cites; until then it is preparing and the previous version
// stays in force. Publishing queues the event and the shop notices, and
// the version can never change again.
func TestIntegrationPolicyPublishesOnlyAfterRuleOwnersAcknowledge(t *testing.T) {
	f, uc, rules, now := policySetup(t)
	ctx := t.Context()
	shop := f.shop(t)
	if _, err := f.uc.Approve(ctx, shop.ID, f.admin); err != nil {
		t.Fatal(err)
	}

	first, err := uc.CreateDraft(ctx, f.admin, returnsDraft(*now, "window-7d"))
	if err != nil {
		t.Fatal(err)
	}
	if first.Version != 1 || first.Status != domain.PolicyDraft || len(first.ContentHash) != 64 {
		t.Fatalf("unexpected draft %+v", first)
	}
	if _, _, err := uc.Publish(ctx, f.admin, first.ID, first.RowVersion+1, "stale"); err == nil {
		t.Fatal("a stale expected_version must be refused")
	}
	p, published, err := uc.Publish(ctx, f.admin, first.ID, first.RowVersion, "Ra mắt chính sách")
	if err != nil || published || p.Status != domain.PolicyPreparing || p.Readiness["order.returns_window"].Ready {
		t.Fatalf("an owner that did not answer keeps the version preparing: %v %v %+v", err, published, p)
	}
	if active, _ := uc.ActivePolicies(ctx); len(active) != 0 {
		t.Fatal("a preparing version is not public")
	}

	rules.ready = true
	if n, err := uc.RetryPreparing(ctx, 10); err != nil || n != 1 {
		t.Fatalf("the worker publishes once the owner acknowledges: %d %v", n, err)
	}
	active, err := uc.ActivePolicies(ctx)
	if err != nil || len(active) != 1 || active[0].ID != first.ID || active[0].Readiness["order.returns_window"].RuleHash == "" {
		t.Fatalf("expected the version in force with its ACKs, got %v %+v", err, active)
	}
	var payload []byte
	var notices int
	if err := f.db.QueryRow(ctx, `SELECT payload FROM policy_outbox WHERE policy_id = $1`, first.ID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var event events.PolicyPublication
	if err := json.Unmarshal(payload, &event); err != nil || event.Kind != "returns" || event.RuleRefs["order.returns_window"] != "window-7d" ||
		event.ContentHash != first.ContentHash {
		t.Fatalf("the event names the version and its rules: %s", payload)
	}
	if err := f.db.QueryRow(ctx, `SELECT count(*) FROM vendor_notification_outbox WHERE type = 'marketplace_policy_updated'`).Scan(&notices); err != nil || notices != 1 {
		t.Fatalf("approved shops are told once, got %d %v", notices, err)
	}

	// Published versions are immutable, also against direct SQL.
	if _, err := f.db.Exec(ctx, `UPDATE marketplace_policy_versions SET content = 'x' WHERE id = $1`, first.ID); err == nil {
		t.Fatal("a published version must not change")
	}
	if _, err := f.db.Exec(ctx, `DELETE FROM marketplace_policy_versions WHERE id = $1`, first.ID); err == nil {
		t.Fatal("a published version must not be deleted")
	}

	// A version scheduled one second after now is not in force yet; at its
	// effective_at it is, and the first stays readable by version.
	next, err := uc.CreateDraft(ctx, f.admin, returnsDraft(now.Add(time.Second), "window-14d"))
	if err != nil || next.Version != 2 {
		t.Fatalf("next version: %v %+v", err, next)
	}
	if _, published, err := uc.Publish(ctx, f.admin, next.ID, next.RowVersion, "Kéo dài thời hạn"); err != nil || !published {
		t.Fatalf("publish v2: %v %v", err, published)
	}
	if active, _ := uc.ActivePolicies(ctx); active[0].Version != 1 {
		t.Fatalf("v2 starts in one second, v1 is still in force: %+v", active[0])
	}
	*now = now.Add(time.Second)
	if active, _ := uc.ActivePolicies(ctx); active[0].Version != 2 {
		t.Fatalf("v2 is in force exactly at its effective_at: %+v", active[0])
	}
	if old, err := uc.PolicyVersion(ctx, "returns", 1); err != nil || old.ID != first.ID {
		t.Fatalf("an old version stays readable: %v", err)
	}

	past, err := uc.CreateDraft(ctx, f.admin, returnsDraft(now.Add(-time.Hour), "window-7d"))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = uc.Publish(ctx, f.admin, past.ID, past.RowVersion, "Back-dated")
	expectAppCode(t, err, apperror.CodeValidation)
	if _, err := uc.Withdraw(ctx, f.admin, past.ID, past.RowVersion, "Sai ngày"); err != nil {
		t.Fatal(err)
	}

	var audits int
	if err := f.db.QueryRow(ctx, `SELECT count(*) FROM policy_audit_logs`).Scan(&audits); err != nil || audits < 6 {
		t.Fatalf("drafts, requests, publications and withdrawals are audited, got %d %v", audits, err)
	}
}

func TestIntegrationShopPolicyNeedsReviewAndCannotLowerProtection(t *testing.T) {
	f, uc, _, _ := policySetup(t)
	ctx := t.Context()
	shop := f.shop(t)
	if _, err := f.uc.Approve(ctx, shop.ID, f.admin); err != nil {
		t.Fatal(err)
	}

	_, err := uc.ProposeShopPolicy(ctx, f.owner, shop.ID, "Hàng sale KHÔNG  hoàn tiền dưới mọi hình thức")
	expectAppCode(t, err, domain.CodeReducesProtection)
	_, err = uc.ProposeShopPolicy(ctx, f.other, shop.ID, "Đóng gói cẩn thận")
	expectAppCode(t, err, apperror.CodeForbidden)

	proposal, err := uc.ProposeShopPolicy(ctx, f.owner, shop.ID, "Shop đóng gói chống sốc và giao trong 24 giờ làm việc.")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := uc.ProposeShopPolicy(ctx, f.owner, shop.ID, "Bản khác"); err == nil {
		t.Fatal("one proposal waits for review at a time")
	}
	if p, _ := uc.PublicShopPolicy(ctx, shop.ID); p != nil {
		t.Fatal("an unreviewed proposal is not public")
	}
	_, err = uc.DecideShopPolicy(ctx, f.admin, proposal.ID, false, "")
	expectAppCode(t, err, apperror.CodeValidation)
	if _, err := uc.DecideShopPolicy(ctx, f.admin, proposal.ID, true, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := uc.DecideShopPolicy(ctx, f.admin, proposal.ID, false, "again"); err == nil {
		t.Fatal("a proposal is decided once")
	}
	public, err := uc.PublicShopPolicy(ctx, shop.ID)
	if err != nil || public == nil || public.ID != proposal.ID {
		t.Fatalf("the approved policy is public: %v %+v", err, public)
	}
	var queued, notices, audits int
	if err := f.db.QueryRow(ctx, `SELECT (SELECT count(*) FROM policy_outbox WHERE policy_id = $1),
		(SELECT count(*) FROM vendor_notification_outbox WHERE vendor_id = $2 AND type = 'shop_policy_approved'),
		(SELECT count(*) FROM vendor_audit_logs WHERE vendor_id = $2 AND action = 'shop_policy_approved')`, proposal.ID, shop.ID).
		Scan(&queued, &notices, &audits); err != nil {
		t.Fatal(err)
	}
	if queued != 1 || notices != 1 || audits != 1 {
		t.Fatalf("approval publishes, notifies and audits together: %d %d %d", queued, notices, audits)
	}
	if _, err := f.db.Exec(ctx, `UPDATE shop_policy_versions SET content = 'x' WHERE id = $1`, proposal.ID); err == nil {
		t.Fatal("a decided shop policy must not change")
	}
}

// Free text written before versioning becomes a proposal for review,
// never an approved policy.
func TestIntegrationLegacyShopTextBecomesAProposal(t *testing.T) {
	db := testDB(t, "../../migrations")
	ctx := t.Context()
	if _, err := db.Exec(ctx, `INSERT INTO vendors (user_id, shop_name, description, policy_text) VALUES (gen_random_uuid(), 'Old shop', 'd', '  Đổi trong 3 ngày  ')`); err != nil {
		t.Fatal(err)
	}
	// Re-run the seeding statement of the migration on the new shop.
	if _, err := db.Exec(ctx, `INSERT INTO shop_policy_versions (vendor_id, version, content, content_hash, source)
		SELECT id, 1, left(btrim(policy_text), 10000), encode(sha256(convert_to(left(btrim(policy_text), 10000), 'UTF8')), 'hex'), 'legacy'
		FROM vendors WHERE btrim(coalesce(policy_text, '')) <> ''`); err != nil {
		t.Fatal(err)
	}
	var status, source, content string
	if err := db.QueryRow(ctx, `SELECT status, source, content FROM shop_policy_versions`).Scan(&status, &source, &content); err != nil {
		t.Fatal(err)
	}
	if status != "proposed" || source != "legacy" || content != "Đổi trong 3 ngày" {
		t.Fatalf("got %s %s %q", status, source, content)
	}
	if _, err := db.Exec(ctx, `DELETE FROM shop_policy_versions`); err == nil {
		t.Fatal("policy rows are never deleted")
	}
}
