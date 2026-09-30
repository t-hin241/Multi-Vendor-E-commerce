package repository_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/payment/internal/adapter"
	"shopee/backend/services/payment/internal/domain"
	"shopee/backend/services/payment/internal/provider"
	"shopee/backend/services/payment/internal/provider/mock"
	"shopee/backend/services/payment/internal/repository"
	"shopee/backend/services/payment/internal/usecase"
)

const fakeWebhookSecret = "fake-webhook-secret-not-a-real-secret"

type fakeOrders struct {
	mu     sync.Mutex
	orders map[string]*adapter.OrderSnapshot
	held   map[string]string
	down   bool
}

func (f *fakeOrders) GetOrder(_ context.Context, id string) (*adapter.OrderSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	o, ok := f.orders[id]
	if !ok {
		return nil, apperror.NotFound("Order not found")
	}
	cp := *o
	return &cp, nil
}

func (f *fakeOrders) HeldVendorOrders(_ context.Context, ids []string) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return nil, errors.New("test order service down")
	}
	out := map[string]string{}
	for _, id := range ids {
		if reason, ok := f.held[id]; ok {
			out[id] = reason
		}
	}
	return out, nil
}

func (f *fakeOrders) add(buyer string, amount int64) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := uuid.NewString()
	expires := time.Now().Add(20 * time.Minute)
	f.orders[id] = &adapter.OrderSnapshot{ID: id, BuyerID: buyer, Status: "pending_payment", TotalAmount: amount, Currency: "VND",
		InventoryStatus: "held", ReservationExpiresAt: &expires}
	return id
}

type fakeVendors struct{ missing map[string]bool }

func (f fakeVendors) DefaultDestination(_ context.Context, vendorID string) (*adapter.PayoutDestination, error) {
	if f.missing[vendorID] {
		return nil, adapter.ErrNoPayoutDestination
	}
	return &adapter.PayoutDestination{AccountID: uuid.NewString(), Version: 1, BankBIN: "970400", Last4: "1234"}, nil
}

type allowRoles struct{}

func (allowRoles) RequireRole(context.Context, string, string) error { return nil }

// countingProvider wraps the mock and can delay or fail calls.
type countingProvider struct {
	*mock.Provider
	creates     atomic.Int64
	cancels     atomic.Int64
	delay       time.Duration
	failAfter   atomic.Bool // create the link, then report a timeout
	queryFailed atomic.Bool
}

func (p *countingProvider) CreateIntent(ctx context.Context, in provider.CreateIntentInput) (provider.CreateIntentResult, error) {
	p.creates.Add(1)
	time.Sleep(p.delay)
	res, err := p.Provider.CreateIntent(ctx, in)
	if err == nil && p.failAfter.Swap(false) {
		return provider.CreateIntentResult{}, errors.New("test timeout after the link was created")
	}
	res.CheckoutURL = "https://example.test/pay/" + in.Reference
	return res, err
}

func (p *countingProvider) Query(ctx context.Context, ref string) (provider.LinkInfo, error) {
	if p.queryFailed.Load() {
		return provider.LinkInfo{}, errors.New("test provider unreachable")
	}
	return p.Provider.Query(ctx, ref)
}

func (p *countingProvider) Cancel(ctx context.Context, ref, reason string) error {
	p.cancels.Add(1)
	return p.Provider.Cancel(ctx, ref, reason)
}

type env struct {
	pool      *pgxpool.Pool
	prov      *countingProvider
	orders    *fakeOrders
	payments  *usecase.PaymentUseCase
	settle    *usecase.SettlementUseCase
	refunds   *usecase.RefundUseCase
	recon     *usecase.ReconciliationUseCase
	delivered chan repository.OrderOutcome
	now       atomic.Pointer[time.Time]
}

func newEnv(t *testing.T) *env {
	pool := paymentDB(t)
	e := &env{pool: pool, prov: &countingProvider{Provider: mock.New(fakeWebhookSecret)}, orders: &fakeOrders{orders: map[string]*adapter.OrderSnapshot{}, held: map[string]string{}},
		delivered: make(chan repository.OrderOutcome, 100)}
	clock := func() time.Time {
		if p := e.now.Load(); p != nil {
			return *p
		}
		return time.Now()
	}
	tx := repository.Transactions{Pool: pool}
	sync := repository.OrderSync{Pool: pool}
	deliver := func(_ context.Context, out repository.OrderOutcome) error { e.delivered <- out; return nil }
	e.payments = usecase.NewPaymentUseCase(usecase.PaymentDeps{Tx: tx, Intents: repository.NewPaymentIntentRepository(pool), Receipts: repository.NewReceiptRepository(pool),
		Orders: e.orders, Provider: e.prov, Verifier: e.prov.Provider, Simulator: e.prov.Provider, ProviderName: "mock", Log: zerolog.Nop(), Now: clock,
		SyncNow: func(ctx context.Context, id string) { _ = sync.DispatchIntent(ctx, id, deliver) }})
	e.settle = usecase.NewSettlementUseCase(usecase.SettlementDeps{Tx: tx, Settlement: repository.NewSettlementRepository(pool), Payouts: repository.NewPayoutRepository(pool),
		Audit: repository.NewAuditRepository(pool), Roles: allowRoles{}, Orders: e.orders, Vendors: fakeVendors{missing: map[string]bool{}}, Log: zerolog.Nop(), Now: clock})
	e.refunds = usecase.NewRefundUseCase(repository.NewRefundRepository(pool), allowRoles{}, zerolog.Nop()).WithSettlement(tx, e.settle)
	e.recon = usecase.NewReconciliationUseCase(usecase.ReconciliationDeps{Tx: tx, Payments: e.payments, Refunds: e.refunds, Intents: repository.NewPaymentIntentRepository(pool),
		Receipts: repository.NewReceiptRepository(pool), OrderSync: sync, RefundSync: repository.RefundSync{Pool: pool}, Audit: repository.NewAuditRepository(pool), Roles: allowRoles{}, Log: zerolog.Nop()})
	return e
}

func (e *env) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(t.Context(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (e *env) webhook(t *testing.T, intent *domain.PaymentIntent, amount int64, succeeded bool) ([]byte, string) {
	t.Helper()
	payload, sig, err := e.prov.BuildSignedEvent(intent.ProviderIntentID, amount, "VND", succeeded, "declined")
	if err != nil {
		t.Fatal(err)
	}
	return payload, sig
}

func TestConcurrentCreateIntentMakesOnePayableLink(t *testing.T) {
	e := newEnv(t)
	e.prov.delay = 150 * time.Millisecond
	buyer := uuid.NewString()
	order := e.orders.add(buyer, 5000)
	var wg sync.WaitGroup
	var ids sync.Map
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			intent, err := e.payments.CreateIntent(context.Background(), buyer, order)
			var app *apperror.Error
			if err != nil && !(errors.As(err, &app) && app.Code == "payment_in_progress") {
				t.Errorf("unexpected error %v", err)
			}
			if intent != nil {
				ids.Store(intent.ID, true)
			}
		}()
	}
	wg.Wait()
	if e.count(t, `SELECT count(*) FROM payment_intents WHERE order_id = $1`, order) != 1 || e.prov.creates.Load() != 1 {
		t.Fatalf("expected one intent and one provider call, got %d calls", e.prov.creates.Load())
	}
	again, err := e.payments.CreateIntent(t.Context(), buyer, order)
	if err != nil || again.Status != domain.StatusPending {
		t.Fatalf("a later call reuses the open link: %+v %v", again, err)
	}
}

func TestProviderTimeoutAfterLinkIsCancelledBeforeANewLink(t *testing.T) {
	e := newEnv(t)
	buyer := uuid.NewString()
	order := e.orders.add(buyer, 5000)
	e.prov.failAfter.Store(true)
	var app *apperror.Error
	if _, err := e.payments.CreateIntent(t.Context(), buyer, order); !errors.As(err, &app) || app.Code != "payment_provider_unavailable" {
		t.Fatalf("expected provider unavailable, got %v", err)
	}
	var reference string
	if err := e.pool.QueryRow(t.Context(), `SELECT provider_reference FROM payment_intents WHERE order_id = $1 AND status = 'creating'`, order).Scan(&reference); err != nil {
		t.Fatal("the attempt must stay 'creating' when its outcome is unknown")
	}
	// Immediately: still in progress, no second charge path.
	if _, err := e.payments.CreateIntent(t.Context(), buyer, order); !errors.As(err, &app) || app.Code != "payment_in_progress" {
		t.Fatalf("expected in progress, got %v", err)
	}
	later := time.Now().Add(time.Minute)
	e.now.Store(&later)
	intent, err := e.payments.CreateIntent(t.Context(), buyer, order)
	if err != nil || intent.ProviderReference == reference {
		t.Fatalf("expected a fresh attempt, got %+v %v", intent, err)
	}
	if link, _ := e.prov.Provider.Query(t.Context(), reference); link.Status != provider.LinkClosed {
		t.Fatal("the orphan link created by the timed-out call must be cancelled at the provider")
	}
	if e.count(t, `SELECT count(*) FROM payment_intents WHERE order_id = $1 AND status IN ('creating','pending')`, order) != 1 {
		t.Fatal("at most one open intent per order")
	}
}

func TestWebhooksAreRecordedOnceAndAppliedExactlyOnce(t *testing.T) {
	e := newEnv(t)
	buyer := uuid.NewString()
	order := e.orders.add(buyer, 5000)
	intent, err := e.payments.CreateIntent(t.Context(), buyer, order)
	if err != nil {
		t.Fatal(err)
	}
	payload, sig := e.webhook(t, intent, 5000, true)
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := e.payments.ProcessWebhook(context.Background(), payload, sig); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if e.count(t, `SELECT count(*) FROM payment_receipts WHERE payment_intent_id = $1 AND status = 'processed'`, intent.ID) != 1 {
		t.Fatal("one receipt, processed once")
	}
	if e.count(t, `SELECT count(*) FROM payment_intents WHERE id = $1 AND status = 'captured'`, intent.ID) != 1 {
		t.Fatal("intent not captured")
	}
	select {
	case out := <-e.delivered:
		if out.PaymentID != intent.ID || out.Amount != 5000 || out.Outcome != "captured" {
			t.Fatalf("unexpected outcome %+v", out)
		}
	default:
		t.Fatal("the capture must be delivered to Order right away through the outbox")
	}
	if e.count(t, `SELECT count(*) FROM payment_events WHERE payment_intent_id = $1`, intent.ID) != 1 {
		t.Fatal("the legacy dedup table must stay in step for rollback")
	}
	if err := e.payments.ProcessWebhook(t.Context(), payload, "bad-signature"); err == nil {
		t.Fatal("an invalid signature must be refused")
	}
}

func TestWrongAmountAndOutOfOrderEvents(t *testing.T) {
	e := newEnv(t)
	buyer := uuid.NewString()
	intent, err := e.payments.CreateIntent(t.Context(), buyer, e.orders.add(buyer, 5000))
	if err != nil {
		t.Fatal(err)
	}
	payload, sig := e.webhook(t, intent, 4999, true)
	if err := e.payments.ProcessWebhook(t.Context(), payload, sig); err != nil {
		t.Fatal(err)
	}
	if e.count(t, `SELECT count(*) FROM payment_receipts WHERE status = 'rejected' AND outcome = 'amount_mismatch'`) != 1 ||
		e.count(t, `SELECT count(*) FROM payment_intents WHERE id = $1 AND status = 'pending'`, intent.ID) != 1 {
		t.Fatal("a wrong amount must be rejected for review and never capture")
	}
	failed, fsig := e.webhook(t, intent, 5000, false)
	if err := e.payments.ProcessWebhook(t.Context(), failed, fsig); err != nil {
		t.Fatal(err)
	}
	ok, osig := e.webhook(t, intent, 5000, true)
	if err := e.payments.ProcessWebhook(t.Context(), ok, osig); err != nil {
		t.Fatal(err)
	}
	if e.count(t, `SELECT count(*) FROM payment_intents WHERE id = $1 AND status = 'captured'`, intent.ID) != 1 {
		t.Fatal("a success after a failure is money that arrived and must be captured")
	}
	late, lsig := e.webhook(t, intent, 5000, false)
	if err := e.payments.ProcessWebhook(t.Context(), late, lsig); err != nil {
		t.Fatal(err)
	}
	if e.count(t, `SELECT count(*) FROM payment_intents WHERE id = $1 AND status = 'captured'`, intent.ID) != 1 {
		t.Fatal("a stale failure must not override a capture")
	}
}

func TestReceiptSurvivesAFailedApplyAndUnknownIntentIsParked(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	buyer := uuid.NewString()
	intent, err := e.payments.CreateIntent(ctx, buyer, e.orders.add(buyer, 5000))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `CREATE FUNCTION fail_capture() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test crash while applying'; END $$;
		CREATE TRIGGER fail_capture BEFORE UPDATE ON payment_intents FOR EACH ROW EXECUTE FUNCTION fail_capture()`); err != nil {
		t.Fatal(err)
	}
	payload, sig := e.webhook(t, intent, 5000, true)
	if err := e.payments.ProcessWebhook(ctx, payload, sig); err == nil {
		t.Fatal("a failed apply must ask the provider to retry")
	}
	if e.count(t, `SELECT count(*) FROM payment_receipts WHERE status = 'retryable'`) != 1 || e.count(t, `SELECT count(*) FROM payment_order_sync`) != 0 {
		t.Fatal("the receipt must be kept, nothing applied")
	}
	if _, err := e.pool.Exec(ctx, `DROP TRIGGER fail_capture ON payment_intents; UPDATE payment_receipts SET next_attempt_at = now()`); err != nil {
		t.Fatal(err)
	}
	if err := e.payments.Reconcile(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if e.count(t, `SELECT count(*) FROM payment_intents WHERE id = $1 AND status = 'captured'`, intent.ID) != 1 {
		t.Fatal("the worker must re-apply the stored receipt")
	}

	// An event for a link this service does not know yet is parked, then
	// applied once the intent exists.
	ghost := &domain.PaymentIntent{ProviderIntentID: "mock_pi_ghost-ref"}
	gp, gs := e.webhook(t, ghost, 7000, true)
	if err := e.payments.ProcessWebhook(ctx, gp, gs); err != nil {
		t.Fatal(err)
	}
	var receiptID string
	if err := e.pool.QueryRow(ctx, `SELECT id FROM payment_receipts WHERE status = 'parked'`).Scan(&receiptID); err != nil {
		t.Fatal("unknown intent must be parked, not dropped")
	}
	repo := repository.NewPaymentIntentRepository(e.pool)
	late := &domain.PaymentIntent{OrderID: uuid.NewString(), BuyerID: buyer, Amount: 7000, Currency: "VND", Status: domain.StatusPending, Provider: "mock",
		ProviderIntentID: "mock_pi_ghost-ref", ProviderReference: "ghost-ref"}
	if err := repo.Create(ctx, late); err != nil {
		t.Fatal(err)
	}
	if _, err := e.recon.RetryReceipt(ctx, uuid.NewString(), receiptID, "intent created after the webhook"); err != nil {
		t.Fatal(err)
	}
	if e.count(t, `SELECT count(*) FROM payment_intents WHERE id = $1 AND status = 'captured'`, late.ID) != 1 ||
		e.count(t, `SELECT count(*) FROM payment_admin_audit WHERE target_id = $1`, receiptID) != 1 {
		t.Fatal("an audited retry must apply the parked receipt")
	}
}

func TestReconcileClosesExpiredLinksAndFindsMissedPayments(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	buyer := uuid.NewString()
	expired, err := e.payments.CreateIntent(ctx, buyer, e.orders.add(buyer, 5000))
	if err != nil {
		t.Fatal(err)
	}
	paid, err := e.payments.CreateIntent(ctx, buyer, e.orders.add(buyer, 6000))
	if err != nil {
		t.Fatal(err)
	}
	// The buyer paid the second link but its webhook never arrived.
	if _, _, err := e.prov.BuildSignedEvent(paid.ProviderIntentID, 6000, "VND", true, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE payment_intents SET expires_at = now() - interval '10 minutes'`); err != nil {
		t.Fatal(err)
	}
	if err := e.payments.Reconcile(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if e.count(t, `SELECT count(*) FROM payment_intents WHERE id = $1 AND status = 'expired'`, expired.ID) != 1 {
		t.Fatal("an unpaid expired link must be closed")
	}
	if link, _ := e.prov.Provider.Query(ctx, expired.ProviderReference); link.Status != provider.LinkClosed {
		t.Fatal("and cancelled at the provider")
	}
	if e.count(t, `SELECT count(*) FROM payment_intents WHERE id = $1 AND status = 'captured'`, paid.ID) != 1 {
		t.Fatal("a payment found by querying the provider must be captured")
	}
}

func settlementOrder(vendor string, completed time.Time) domain.SettlementOrder {
	return domain.SettlementOrder{VendorOrderID: uuid.NewString(), OrderID: uuid.NewString(), VendorID: vendor, Currency: "VND",
		SubtotalAmount: 100000, ShippingAmount: 20000, CommissionAmount: 10000, CommissionRateBps: 1000,
		CompletedAt: completed, EligibleAt: completed.Add(7 * 24 * time.Hour)}
}

func TestSettlementLedgerAndManualPayouts(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	admin, vendor := uuid.NewString(), uuid.NewString()
	old := time.Now().Add(-30 * 24 * time.Hour)
	first, second, recent := settlementOrder(vendor, old), settlementOrder(vendor, old), settlementOrder(vendor, time.Now())

	// A refund confirmed before Order reports the vendor order.
	intent := &domain.PaymentIntent{OrderID: first.OrderID, BuyerID: uuid.NewString(), Amount: 120000, Currency: "VND", Status: domain.StatusCaptured, Provider: "mock", ProviderIntentID: "mock_pi_" + uuid.NewString()}
	if err := repository.NewPaymentIntentRepository(e.pool).Create(ctx, intent); err != nil {
		t.Fatal(err)
	}
	refund, _, err := e.refunds.Request(ctx, domain.RefundRequest{OrderRefundID: uuid.NewString(), OrderID: first.OrderID, VendorOrderID: &first.VendorOrderID,
		Amount: 30000, Currency: "VND", Reason: "return", RequestedBy: admin})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.refunds.Resolve(ctx, admin, refund.ID, domain.RefundResolution{Outcome: domain.RefundSucceeded, EvidenceReference: "FAKE-BANK-1"}); err != nil {
		t.Fatal(err)
	}
	for _, o := range []domain.SettlementOrder{first, second, recent} {
		if created, err := e.settle.IngestVendorOrder(ctx, o); err != nil || !created {
			t.Fatalf("ingest: %v %v", created, err)
		}
	}
	if created, err := e.settle.IngestVendorOrder(ctx, first); err != nil || created {
		t.Fatal("a replay is acknowledged without new entries")
	}
	changed := first
	changed.SubtotalAmount = 100001
	var app *apperror.Error
	if _, err := e.settle.IngestVendorOrder(ctx, changed); !errors.As(err, &app) || app.Code != apperror.CodeConflict {
		t.Fatal("a different snapshot for the same vendor order must conflict")
	}
	// first: 110000 - 30000 + 3000 reversal = 83000; second: 110000; recent: not eligible yet.
	e.orders.held[second.VendorOrderID] = "return_open"
	batch, skipped, err := e.settle.CreatePayoutBatch(ctx, admin, "fake-batch-key-1", "VND", nil)
	if err != nil || len(batch.Items) != 1 || batch.Items[0].Amount != 83000 || len(skipped) != 0 {
		t.Fatalf("unexpected batch %+v %v %v", batch, skipped, err)
	}
	replay, _, err := e.settle.CreatePayoutBatch(ctx, admin, "fake-batch-key-1", "VND", nil)
	if err != nil || replay.ID != batch.ID {
		t.Fatal("the same key returns the same batch")
	}
	if _, _, err := e.settle.CreatePayoutBatch(ctx, admin, "fake-batch-key-2", "VND", nil); !errors.As(err, &app) || app.Code != apperror.CodeConflict {
		t.Fatal("entries in a pending payout and held orders are not paid again")
	}
	item := batch.Items[0]
	if _, err := e.settle.ResolvePayoutItem(ctx, admin, item.ID, domain.PayoutResolution{Outcome: domain.PayoutItemFailed, Note: "account closed"}); err != nil {
		t.Fatal(err)
	}
	delete(e.orders.held, second.VendorOrderID)
	retry, _, err := e.settle.CreatePayoutBatch(ctx, admin, "fake-batch-key-3", "VND", nil)
	if err != nil || retry.Items[0].Amount != 193000 {
		t.Fatalf("a failed payout releases its entries: %+v %v", retry, err)
	}
	if _, err := e.settle.ResolvePayoutItem(ctx, admin, retry.Items[0].ID, domain.PayoutResolution{Outcome: domain.PayoutItemSucceeded, EvidenceReference: "FAKE-TRANSFER-1"}); err != nil {
		t.Fatal(err)
	}
	// A refund after the payout becomes a debt netted into the next payout.
	late, _, err := e.refunds.Request(ctx, domain.RefundRequest{OrderRefundID: uuid.NewString(), OrderID: first.OrderID, VendorOrderID: &first.VendorOrderID,
		Amount: 10000, Currency: "VND", Reason: "dispute", RequestedBy: admin})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.refunds.Resolve(ctx, admin, late.ID, domain.RefundResolution{Outcome: domain.RefundSucceeded, EvidenceReference: "FAKE-BANK-2"}); err != nil {
		t.Fatal(err)
	}
	balances, err := e.settle.Balances(ctx, admin, "VND", 10, 0)
	if err != nil || len(balances) != 1 {
		t.Fatal(err)
	}
	b := balances[0]
	// Owed: recent 110000 (not eligible) + debt -10000 + reversal 1000.
	if b.PaidOut != 193000 || b.Owed != 101000 || b.Eligible != -9000 {
		t.Fatalf("unexpected balance %+v", b)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE settlement_entries SET amount = 1`); err == nil {
		t.Fatal("the ledger must refuse updates")
	}
	if e.count(t, `SELECT count(*) FROM payment_admin_audit WHERE action LIKE 'payout_%'`) < 3 {
		t.Fatal("payout actions must be audited")
	}
}

func TestPayoutFailsClosedWithoutOrderOrDestination(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	admin, vendor, other := uuid.NewString(), uuid.NewString(), uuid.NewString()
	old := time.Now().Add(-30 * 24 * time.Hour)
	for _, v := range []string{vendor, other} {
		if _, err := e.settle.IngestVendorOrder(ctx, settlementOrder(v, old)); err != nil {
			t.Fatal(err)
		}
	}
	e.orders.down = true
	if _, _, err := e.settle.CreatePayoutBatch(ctx, admin, "fake-batch-key-9", "VND", nil); err == nil {
		t.Fatal("holds unknown: nothing may be paid")
	}
	if e.count(t, `SELECT count(*) FROM payout_batches`) != 0 {
		t.Fatal("a failed batch must leave nothing behind")
	}
	e.orders.down = false
	e.settle.Vendors = fakeVendors{missing: map[string]bool{other: true}}
	batch, skipped, err := e.settle.CreatePayoutBatch(ctx, admin, "fake-batch-key-10", "VND", nil)
	if err != nil || len(batch.Items) != 1 || len(skipped) != 1 || skipped[0].Reason != "no_verified_destination" {
		t.Fatalf("unexpected %+v %+v %v", batch, skipped, err)
	}
	if batch.Items[0].DestinationMask != "970400 ****1234" {
		t.Fatal("only a masked destination is stored")
	}
}
