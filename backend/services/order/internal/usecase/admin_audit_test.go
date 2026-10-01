package usecase_test

import (
	"errors"
	"testing"

	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/order/internal/domain"
	"shopee/backend/services/order/internal/usecase"
)

// The use case refuses a non-admin itself, so a route that forgot its
// middleware still cannot act, and nothing is audited for a refused action.
func TestAdminActions_RefuseActorsWithoutTheAdminRole(t *testing.T) {
	f := newCheckoutFixture()
	order, vos := deliveredOrder(t, f)
	f.identity.denied["buyer-1"] = true
	actions := map[string]func() error{
		"cancel": func() error { _, err := f.uc.AdminCancel(t.Context(), "buyer-1", order.ID, "test"); return err },
		"refund": func() error {
			_, err := f.uc.AdminRequestRefund(t.Context(), "buyer-1", refundInput(order.ID, vos["vendor-a"].ID, "", domain.RefundReasonDispute, 1))
			return err
		},
		"commission": func() error { _, err := f.uc.SetCommissionRule(t.Context(), "buyer-1", 1, "test"); return err },
		"replay": func() error {
			_, err := f.uc.ReplayEffect(t.Context(), "buyer-1", "00000000-0000-0000-0000-000000000001", "test")
			return err
		},
		"retry": func() error { _, err := f.uc.RetryReturnRefund(t.Context(), "buyer-1", "missing", "test"); return err },
		"decide": func() error {
			_, err := f.uc.AdminDecideReturn(t.Context(), "buyer-1", "missing", true, "")
			return err
		},
	}
	for name, act := range actions {
		var app *apperror.Error
		if err := act(); !errors.As(err, &app) || app.Code != apperror.CodeForbidden {
			t.Errorf("%s: expected forbidden, got %v", name, err)
		}
	}
	if len(f.audit.actions) != 0 {
		t.Fatalf("refused actions must not be audited: %+v", f.audit.actions)
	}
}

func TestAdminActions_FailClosedWithoutIdentityOrAudit(t *testing.T) {
	f := newCheckoutFixture()
	order := placedOrder(t, f)
	unverified := usecase.NewOrderUseCase(usecase.Deps{Orders: f.orders, VendorOrders: f.vendorOrders, Audit: f.audit, Log: zerolog.Nop()})
	if _, err := unverified.AdminCancel(t.Context(), "admin-1", order.ID, "test"); err == nil {
		t.Fatal("an admin action must not run without Identity re-verification")
	}

	f.audit.fail = errors.New("audit store down")
	_, err := f.uc.SetCommissionRule(t.Context(), "admin-1", 1500, "Fee change")
	expectCode(t, err, apperror.CodeInternal)
}

func TestAdminActions_AreAuditedWithReasonAndChanges(t *testing.T) {
	f := newCheckoutFixture()
	order := placedOrder(t, f)
	if _, err := f.uc.AdminCancel(t.Context(), "admin-1", order.ID, "Buyer asked by phone"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.uc.SetCommissionRule(t.Context(), "admin-1", 1500, "Fee change"); err != nil {
		t.Fatal(err)
	}
	if f.audit.count("order_cancelled") != 1 || f.audit.count("commission_rule_set") != 1 {
		t.Fatalf("expected one audit row per action: %+v", f.audit.actions)
	}
	for _, a := range f.audit.actions {
		if a.ActorID != "admin-1" || a.Reason == nil || len(a.Changes) == 0 {
			t.Fatalf("audit row lacks actor, reason or changes: %+v", a)
		}
	}
	rate := f.audit.actions[1].Changes["rate_bps"].([]any)
	if rate[0] != 1000 || rate[1] != 1500 {
		t.Fatalf("commission change must record before and after, got %v", rate)
	}

	if _, err := f.uc.SetCommissionRule(t.Context(), "admin-1", 1500, "  "); err == nil {
		t.Fatal("a reason is required")
	}
}

// ADM-02: a resend after a timeout with the same key returns the refund
// already requested instead of creating a second one.
func TestAdminRefund_IdempotencyKeyPreventsDuplicateRefunds(t *testing.T) {
	f := newCheckoutFixture()
	order, vos := deliveredOrder(t, f)
	in := refundInput(order.ID, vos["vendor-a"].ID, "", domain.RefundReasonDispute, 1000)
	in.IdempotencyKey = "refund-key-0001"

	first, err := f.uc.AdminRequestRefund(t.Context(), "admin-1", in)
	if err != nil {
		t.Fatal(err)
	}
	again, err := f.uc.AdminRequestRefund(t.Context(), "admin-1", in)
	if err != nil || again.ID != first.ID {
		t.Fatalf("resend must return the same refund, got %v %v", again, err)
	}
	if n := len(f.refunds.refunds); n != 1 || f.audit.count("refund_requested") != 1 {
		t.Fatalf("expected one refund and one audit row, got %d refunds", n)
	}

	in.Amount = 2000
	_, err = f.uc.AdminRequestRefund(t.Context(), "admin-1", in)
	expectCode(t, err, apperror.CodeConflict)

	in.IdempotencyKey = "bad key"
	_, err = f.uc.AdminRequestRefund(t.Context(), "admin-1", in)
	expectCode(t, err, apperror.CodeValidation)
}

func TestReplayEffect_NeedsReasonAndIsHarmlessTwice(t *testing.T) {
	f := newCheckoutFixture()
	order := placedOrder(t, f)
	if err := f.effects.Enqueue(t.Context(), domain.NewNotifyEffect(order.ID, "buyer-1", "order_test_notice")); err != nil {
		t.Fatal(err)
	}
	effect := f.effects.effects[len(f.effects.effects)-1]
	effect.Status = domain.EffectParked

	if _, err := f.uc.ReplayEffect(t.Context(), "admin-1", effect.ID, ""); err == nil {
		t.Fatal("a replay needs a reason")
	}
	replayed, err := f.uc.ReplayEffect(t.Context(), "admin-1", effect.ID, "Notification service is back")
	if err != nil || !replayed {
		t.Fatalf("expected the parked effect replayed, got %v %v", replayed, err)
	}
	replayed, err = f.uc.ReplayEffect(t.Context(), "admin-1", effect.ID, "Notification service is back")
	if err != nil || replayed {
		t.Fatalf("a second replay changes nothing, got %v %v", replayed, err)
	}
	if f.audit.count("effect_replayed") != 1 {
		t.Fatalf("only the effective replay is audited: %+v", f.audit.actions)
	}
}
