package repository_test

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"io"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/shopaccess"
	"shopee/backend/services/payment/internal/domain"
	"shopee/backend/services/payment/internal/repository"
	"shopee/backend/services/payment/internal/usecase"
)

func TestStatementDuplicateExportWithSingleConnection(t *testing.T) {
	pool := paymentDB(t)
	cfg := pool.Config()
	cfg.MaxConns = 1
	limited, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(limited.Close)
	r := repository.StatementRepository{Pool: limited}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	now := time.Now().UTC()
	s, err := r.Read(ctx, uuid.NewString(), domain.StatementFilter{From: now.Add(-time.Hour), To: now.Add(time.Hour), Currency: "VND"}, nil, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	actor, key := uuid.NewString(), "fake-af14-single-connection"
	first, err := r.SaveExport(ctx, actor, key, *s, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	// Both requests can miss the use case's initial lookup. The losing
	// insert must read the winner without acquiring a second connection.
	replay, err := r.SaveExport(ctx, actor, key, *s, time.Hour)
	if err != nil || replay == nil || replay.ID != first.ID {
		t.Fatalf("duplicate export must not wait for its own connection: %+v %v", replay, err)
	}
}

type statementAccess struct {
	actor, vendor string
	denied        bool
}

func (a *statementAccess) Authorize(_ context.Context, actor, vendor, permission string) (shopaccess.Grant, error) {
	if a.denied || actor != a.actor || vendor != a.vendor || permission != shopaccess.FinanceRead {
		return shopaccess.Grant{}, shopaccess.Denied("")
	}
	return shopaccess.Grant{VendorID: vendor}, nil
}

type statementFiles struct {
	mu      sync.Mutex
	data    []byte
	uploads int
}

func (f *statementFiles) PutStream(_ context.Context, _ string, r io.Reader, _ string) error {
	b, err := io.ReadAll(r)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data = b
	f.uploads++
	return err
}
func (f *statementFiles) DownloadURL(_ context.Context, _ string, ttl time.Duration) (string, error) {
	if ttl > 60*time.Second {
		return "", errors.New("TTL too long")
	}
	return "https://example.invalid/private-fake-download", nil
}
func (f *statementFiles) Delete(context.Context, string) error { return nil }

func TestStatementGoldenLedgerAndImmutableExport(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	vendor, actor := uuid.NewString(), uuid.NewString()
	o := settlementOrder(vendor, time.Now().Add(-30*24*time.Hour))
	if _, err := e.settle.IngestVendorOrder(ctx, o); err != nil {
		t.Fatal(err)
	}
	r := repository.StatementRepository{Pool: e.pool}
	now := time.Now().UTC()
	f := domain.StatementFilter{From: now.Add(-time.Hour), To: now.Add(time.Hour), Currency: "VND"}
	a := &statementAccess{actor: actor, vendor: vendor}
	files := &statementFiles{}
	uc := &usecase.StatementUseCase{Store: r, Storage: files, Shops: a, Enabled: true, Retention: 24 * time.Hour, Log: zerolog.Nop()}
	s, err := uc.Read(ctx, actor, vendor, false, f, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if s.Balances.Opening != 0 || s.Balances.Closing != 110000 || s.Balances.Payable != 110000 || s.Balances.Credit-s.Balances.Debit != 110000 {
		t.Fatalf("golden sale: %+v", s.Balances)
	}
	// Holds exclude the sale and its commission, without counting twice.
	if _, _, err := holdUseCase(e).Acquire(ctx, holdOn(vendor, o.VendorOrderID)); err != nil {
		t.Fatal(err)
	}
	s, err = uc.Read(ctx, actor, vendor, false, f, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if s.Balances.Held != 110000 || s.Balances.Payable != 0 || s.NextCursor == "" {
		t.Fatalf("hold or cursor: %+v", s)
	}
	p2, err := uc.Read(ctx, actor, vendor, false, f, s.NextCursor, 1)
	if err != nil || p2.Entries[0].ID == s.Entries[0].ID {
		t.Fatalf("pagination: %v", err)
	}
	// Two simultaneous submissions must return the same frozen operation.
	var wg sync.WaitGroup
	ops := make(chan *domain.StatementExport, 2)
	errs := make(chan error, 2)
	for range 2 {
		wg.Go(func() {
			op, err := uc.CreateExport(ctx, actor, vendor, "fake-af14-idempotency", "csv", f)
			ops <- op
			errs <- err
		})
	}
	wg.Wait()
	close(ops)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var first *domain.StatementExport
	for op := range ops {
		if first != nil && op.ID != first.ID {
			t.Fatal("concurrent export duplicated")
		}
		first = op
	}
	if first.Snapshot.Balances.Closing != 110000 {
		t.Fatal("snapshot changed")
	}
	// This later correction belongs to the same selected period but not the export.
	if _, err := e.settle.Adjust(ctx, actor, vendor, -200000, "VND", "fake correction after cutoff"); err != nil {
		t.Fatal(err)
	}
	if err := uc.ProcessExport(ctx); err != nil {
		t.Fatal(err)
	}
	exported, err := uc.Export(ctx, actor, vendor, first.ID)
	if err != nil || exported.Status != "ready" || len(exported.Checksum) != 64 {
		t.Fatalf("export result: %+v %v", exported, err)
	}
	rows, err := csv.NewReader(bytes.NewReader(files.data)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	var sum int64
	for _, row := range rows[1:] {
		n, err := strconv.ParseInt(row[3], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		sum += n
		if row[14] != "110000" {
			t.Fatal("CSV differs from API snapshot")
		}
	}
	if sum != 110000 {
		t.Fatalf("CSV sum = %d", sum)
	}
	live, err := uc.Read(ctx, actor, vendor, false, f, "", 100)
	if err != nil || live.Balances.Closing != -90000 || live.Balances.Payable != -200000 {
		t.Fatalf("negative carryover %+v %v", live, err)
	}
	if _, err := uc.CreateExport(ctx, actor, vendor, "fake-af14-idempotency", "csv", domain.StatementFilter{From: f.From.Add(time.Minute), To: f.To, Currency: "VND"}); appCode(err) != "conflict" {
		t.Fatalf("payload conflict %v", err)
	}
	// No activity still carries the opening; currency is never mixed.
	future := domain.StatementFilter{From: now.Add(24 * time.Hour), To: now.Add(48 * time.Hour), Currency: "VND"}
	empty, err := uc.Read(ctx, actor, vendor, false, future, "", 20)
	if err != nil || len(empty.Entries) != 0 || empty.Balances.Opening != -90000 || empty.Balances.Closing != -90000 {
		t.Fatalf("opening: %+v %v", empty, err)
	}
	uc.Enabled = false
	if _, _, err := uc.Download(ctx, actor, vendor, first.ID); err != nil {
		t.Fatal("rollback must retain download", err)
	}
	a.denied = true
	if _, _, err := uc.Download(ctx, actor, vendor, first.ID); appCode(err) != "permission_denied" {
		t.Fatalf("revoked download: %v", err)
	}
	if _, err := uc.Export(ctx, actor, uuid.NewString(), first.ID); appCode(err) != "permission_denied" {
		t.Fatal("cross shop was allowed")
	}
}

func TestStatementPayoutClaimsAndPartialRefundAfterPayout(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	vendor, admin := uuid.NewString(), uuid.NewString()
	o := settlementOrder(vendor, time.Now().Add(-30*24*time.Hour))
	if _, err := e.settle.IngestVendorOrder(ctx, o); err != nil {
		t.Fatal(err)
	}
	r := repository.StatementRepository{Pool: e.pool}
	f := domain.StatementFilter{From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Hour), Currency: "VND"}
	b, _, err := e.settle.CreatePayoutBatch(ctx, admin, "fake-af14-payout", "VND", []string{vendor})
	if err != nil {
		t.Fatal(err)
	}
	s, err := r.Read(ctx, vendor, f, nil, 100, nil)
	if err != nil || s.Balances.Payable != 0 || s.Balances.InPayout != 110000 {
		t.Fatalf("claim: %+v %v", s, err)
	}
	if _, err := e.settle.ResolvePayoutItem(ctx, admin, b.Items[0].ID, domain.PayoutResolution{Outcome: domain.PayoutItemSucceeded, EvidenceReference: "fake-af14-bank-reference"}); err != nil {
		t.Fatal(err)
	}
	p, err := r.Payout(ctx, vendor, b.Items[0].ID)
	if err != nil || p.Amount != 110000 || len(p.EntryRefs) != 3 || p.MaskedDestination == "" {
		t.Fatalf("payout: %+v %v", p, err)
	}
	if _, err := r.Payout(ctx, uuid.NewString(), p.ID); appCode(err) != "not_found" {
		t.Fatalf("cross shop payout: %v", err)
	}
	entries := domain.RefundEntries(o, uuid.NewString(), 50000, time.Now(), 0, 0)
	if err := repository.NewSettlementRepository(e.pool).Append(ctx, entries); err != nil {
		t.Fatal(err)
	}
	s, err = r.Read(ctx, vendor, f, nil, 100, nil)
	if err != nil || s.Balances.Closing != -45000 || s.Balances.Payable != -45000 || s.Balances.InPayout != 0 {
		t.Fatalf("refund debt: %+v %v", s, err)
	}
	var signed int64
	for _, entry := range s.Entries {
		signed += entry.Amount
	}
	if signed+s.Balances.Opening != s.Balances.Closing {
		t.Fatal("ledger did not reconcile")
	}
}

func TestStatementRepeatableReadAndWorkerRevocation(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	vendor, actor := uuid.NewString(), uuid.NewString()
	o := settlementOrder(vendor, time.Now().Add(-30*24*time.Hour))
	if _, err := e.settle.IngestVendorOrder(ctx, o); err != nil {
		t.Fatal(err)
	}
	r := repository.StatementRepository{Pool: e.pool}
	f := domain.StatementFilter{From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Hour), Currency: "VND"}
	s, err := r.Read(ctx, vendor, f, nil, 100, func(context.Context, []string) (map[string]string, error) {
		_, err := e.settle.Adjust(ctx, actor, vendor, 7, "VND", "fake concurrent adjustment")
		return map[string]string{}, err
	})
	if err != nil || s.Balances.Closing != 110000 || len(s.Entries) != 3 {
		t.Fatalf("not a repeatable snapshot: %+v %v", s, err)
	}
	a := &statementAccess{actor: actor, vendor: vendor}
	files := &statementFiles{}
	uc := &usecase.StatementUseCase{Store: r, Storage: files, Shops: a, Enabled: true, Retention: time.Hour, Log: zerolog.Nop()}
	if _, err := uc.CreateExport(ctx, actor, vendor, "fake-af14-revocation", "csv", f); err != nil {
		t.Fatal(err)
	}
	a.denied = true
	if err := uc.ProcessExport(ctx); appCode(err) != "permission_denied" {
		t.Fatalf("worker bypassed revoke: %v", err)
	}
	if files.uploads != 0 {
		t.Fatal("revoked job uploaded a file")
	}
}

func TestStatementVietnamBoundariesCurrencyAndLimit(t *testing.T) {
	pool := paymentDB(t)
	ctx := t.Context()
	vendor := uuid.NewString()
	from, err := time.Parse(time.RFC3339, "2026-01-01T00:00:00+07:00")
	if err != nil {
		t.Fatal(err)
	}
	f := domain.StatementFilter{From: from, To: from.Add(24 * time.Hour), Currency: "VND"}
	for i, row := range []struct {
		at       time.Time
		currency string
		amount   int64
	}{{from.Add(-time.Microsecond), "VND", 10}, {from, "VND", 20}, {f.To.Add(-time.Microsecond), "VND", 30}, {f.To, "VND", 40}, {from, "USD", 999}} {
		if _, err := pool.Exec(ctx, `INSERT INTO settlement_entries(vendor_id,entry_type,amount,currency,source_ref,eligible_at,created_at) VALUES($1,'adjustment',$2,$3,$4,$5,$5)`, vendor, row.amount, row.currency, "fake-boundary-"+strconv.Itoa(i), row.at); err != nil {
			t.Fatal(err)
		}
	}
	r := repository.StatementRepository{Pool: pool}
	s, err := r.Read(ctx, vendor, f, nil, 100, nil)
	if err != nil || s.Balances.Opening != 10 || s.Balances.Credit != 50 || s.Balances.Closing != 60 || len(s.Entries) != 2 {
		t.Fatalf("date/currency boundary: %+v %v", s, err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO settlement_entries(vendor_id,entry_type,amount,currency,source_ref,eligible_at,created_at) SELECT $1,'adjustment',1,'VND','fake-limit-'||n,$2,$2 FROM generate_series(1,100001) n`, vendor, from); err != nil {
		t.Fatal(err)
	}
	a := &statementAccess{actor: uuid.NewString(), vendor: vendor}
	uc := &usecase.StatementUseCase{Store: r, Shops: a, Storage: &statementFiles{}, Enabled: true, Retention: time.Hour}
	if _, err := uc.CreateExport(ctx, a.actor, vendor, "fake-af14-too-large", "csv", f); appCode(err) != "statement_invalid" {
		t.Fatalf("row cap not enforced: %v", err)
	}
}

func TestStatementLegacyGapAndSnapshotRetention(t *testing.T) {
	pool := paymentDB(t)
	ctx := t.Context()
	vendor, actor, batch := uuid.NewString(), uuid.NewString(), uuid.NewString()
	// Simulate a pre-migration legacy payout: no historical currency is invented.
	if _, err := pool.Exec(ctx, `ALTER TABLE payout_items DROP CONSTRAINT payout_destination_reference_required`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO payout_batches(id,provider,idempotency_key,status,created_by) VALUES($1,'fake','fake-af14-legacy','completed',$2)`, batch, actor); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO payout_items(payout_batch_id,vendor_id,amount,destination_mask,status,provider_payout_id) VALUES($1,$2,100,'fake ****1234','succeeded','fake-af14-transfer')`, batch, vendor); err != nil {
		t.Fatal(err)
	}
	r := repository.StatementRepository{Pool: pool}
	f := domain.StatementFilter{From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Hour), Currency: "VND"}
	s, err := r.Read(ctx, vendor, f, nil, 100, nil)
	if err != nil || s.GapCount != 1 || s.BalancesAvailable {
		t.Fatalf("legacy gap: %+v %v", s, err)
	}
	a := &statementAccess{actor: actor, vendor: vendor}
	uc := &usecase.StatementUseCase{Store: r, Shops: a, Storage: &statementFiles{}, Enabled: true, Retention: time.Hour}
	if _, err := uc.CreateExport(ctx, actor, vendor, "fake-af14-gap", "csv", f); appCode(err) != "statement_history_gap" {
		t.Fatalf("gap exported: %v", err)
	}
	// Direct store fixture for an already-expired snapshot tests retention.
	ex, err := r.SaveExport(ctx, actor, "fake-af14-expired", *s, -time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE statement_exports SET snapshot='{}' WHERE id=$1`, ex.ID); err == nil {
		t.Fatal("snapshot was mutable")
	}
	expired, err := r.ExpiredExport(ctx)
	if err != nil || expired.ID != ex.ID {
		t.Fatalf("expiry: %v", err)
	}
	if err := r.MarkExpired(ctx, ex.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := uc.Download(ctx, actor, vendor, ex.ID); appCode(err) != "conflict" {
		t.Fatalf("expired download: %v", err)
	}
}

func TestStatementBatchesLegacyOrderHoldContract(t *testing.T) {
	pool := paymentDB(t)
	ctx := t.Context()
	vendor := uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO settlement_vendor_orders(vendor_order_id,order_id,vendor_id,currency,subtotal_amount,shipping_amount,commission_amount,commission_rate_bps,completed_at,eligible_at,policy_version)
	 SELECT gen_random_uuid(),gen_random_uuid(),$1,'VND',1,0,0,0,now(),now(),'fake-af14-test' FROM generate_series(1,501)`, vendor); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO settlement_entries(vendor_id,vendor_order_id,entry_type,amount,currency,source_ref,eligible_at)
	 SELECT vendor_id,vendor_order_id,'sale',1,'VND','fake-batch:'||vendor_order_id,now() FROM settlement_vendor_orders WHERE vendor_id=$1`, vendor); err != nil {
		t.Fatal(err)
	}
	f := domain.StatementFilter{From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Hour), Currency: "VND"}
	calls, total := 0, 0
	s, err := (repository.StatementRepository{Pool: pool}).Read(ctx, vendor, f, nil, 20, func(_ context.Context, ids []string) (map[string]string, error) {
		calls++
		total += len(ids)
		if len(ids) > 500 {
			t.Fatal("Order contract exceeded")
		}
		return map[string]string{ids[0]: "fake-active-case"}, nil
	})
	if err != nil || calls != 2 || total != 501 || s.Balances.Held != 2 || s.Balances.Payable != 499 {
		t.Fatalf("hold batches: calls=%d total=%d snapshot=%+v error=%v", calls, total, s, err)
	}
}
