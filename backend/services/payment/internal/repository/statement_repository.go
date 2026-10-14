package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"shopee/backend/pkg/apperror"
	"shopee/backend/services/payment/internal/domain"
)

type StatementRepository struct{ Pool *pgxpool.Pool }

// HoldLookup preserves the existing Order hold safety check until the hold
// ledger backfill is complete. No provider call or money mutation happens here.
type HoldLookup func(context.Context, []string) (map[string]string, error)

func (r StatementRepository) Read(ctx context.Context, vendor string, f domain.StatementFilter, cursor *domain.StatementCursor, limit int, holds HoldLookup) (*domain.Statement, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, err := r.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	s := &domain.Statement{VendorID: vendor, Filter: f, Entries: []domain.StatementEntry{}}
	if err := tx.QueryRow(ctx, `SELECT transaction_timestamp()`).Scan(&s.AsOf); err != nil {
		return nil, err
	}
	extra := []string{}
	if holds != nil {
		rows, err := tx.Query(ctx, `SELECT DISTINCT vendor_order_id::text FROM settlement_entries e WHERE vendor_id=$1 AND currency=$2 AND vendor_order_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM payout_item_entries l JOIN payout_items i ON i.id=l.payout_item_id WHERE l.entry_id=e.id AND i.status IN ('pending','succeeded'))`, vendor, f.Currency)
		if err != nil {
			return nil, err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return nil, err
		}
		// Order's compatibility contract accepts at most 500 references.
		for start := 0; start < len(ids); start += 500 {
			held, err := holds(ctx, ids[start:min(start+500, len(ids))])
			if err != nil {
				return nil, err
			}
			for id := range held {
				extra = append(extra, id)
			}
		}
	}
	// Refunds and negative adjustments remain payable even on held orders,
	// exactly as Entry.Payable; claims always exclude their entries.
	err = tx.QueryRow(ctx, `WITH classified AS (
	 SELECT e.*,
	 EXISTS(SELECT 1 FROM payout_item_entries l JOIN payout_items i ON i.id=l.payout_item_id WHERE l.entry_id=e.id AND i.status IN ('pending','succeeded')) claimed,
	 EXISTS(SELECT 1 FROM payout_item_entries l JOIN payout_items i ON i.id=l.payout_item_id WHERE l.entry_id=e.id AND i.status='pending') pending,
	 (EXISTS(SELECT 1 FROM settlement_holds h WHERE h.vendor_order_id=e.vendor_order_id AND h.status='active')
	 OR EXISTS(SELECT 1 FROM payment_refunds r WHERE r.vendor_order_id=e.vendor_order_id AND r.status IN ('awaiting_provider_refund','pending'))
	 OR COALESCE(e.vendor_order_id::text=ANY($5::text[]),false)) held,
	 (e.entry_type='refund' OR (e.entry_type='adjustment' AND e.amount<0)) immediate
	 FROM settlement_entries e WHERE vendor_id=$1 AND currency=$2
	 ) SELECT
	 COALESCE(sum(amount) FILTER(WHERE created_at<$3),0),
	 COALESCE(sum(amount) FILTER(WHERE created_at >=$3 AND created_at<$4 AND amount>0),0),
	 COALESCE(-sum(amount) FILTER(WHERE created_at >=$3 AND created_at<$4 AND amount<0),0),
	 COALESCE(sum(amount) FILTER(WHERE created_at<$4),0),
	 COALESCE(sum(amount) FILTER(WHERE entry_type<>'payout' AND NOT claimed AND NOT immediate AND held),0),
	 COALESCE(sum(amount) FILTER(WHERE entry_type<>'payout' AND NOT claimed AND NOT immediate AND NOT held AND eligible_at>now()),0),
	 COALESCE(sum(amount) FILTER(WHERE entry_type<>'payout' AND NOT claimed AND (immediate OR (NOT held AND eligible_at<=now()))),0),
	 COALESCE(sum(amount) FILTER(WHERE pending),0), max(created_at)
	 FROM classified`, vendor, f.Currency, f.From, f.To, extra).Scan(&s.Balances.Opening, &s.Balances.Credit, &s.Balances.Debit, &s.Balances.Closing, &s.Balances.Held, &s.Balances.NotEligible, &s.Balances.Payable, &s.Balances.InPayout, &s.Watermark)
	if err != nil {
		return nil, err
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM payout_items WHERE vendor_id=$1 AND status='succeeded' AND currency IS NULL`, vendor).Scan(&s.GapCount); err != nil {
		return nil, err
	}
	s.BalancesAvailable = s.GapCount == 0
	var at *time.Time
	var id *string
	if cursor != nil {
		at, id = &cursor.At, &cursor.ID
	}
	rows, err := tx.Query(ctx, `SELECT id, vendor_order_id, entry_type, amount, currency, source_ref, created_at, COALESCE(occurred_at,created_at), occurred_at IS NULL, eligible_at FROM settlement_entries
	 WHERE vendor_id=$1 AND currency=$2 AND created_at >=$3 AND created_at<$4
	 AND ($5::timestamptz IS NULL OR (created_at,id)<($5,$6::uuid)) ORDER BY created_at DESC,id DESC LIMIT $7`, vendor, f.Currency, f.From, f.To, at, id, limit+1)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var e domain.StatementEntry
		if err := rows.Scan(&e.ID, &e.VendorOrderID, &e.Type, &e.Amount, &e.Currency, &e.SourceRef, &e.RecordedAt, &e.OccurredAt, &e.OccurredEstimated, &e.EligibleAt); err != nil {
			rows.Close()
			return nil, err
		}
		s.Entries = append(s.Entries, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(s.Entries) > limit {
		s.Entries = s.Entries[:limit]
		last := s.Entries[limit-1]
		s.NextCursor = (domain.StatementCursor{VendorID: vendor, Filter: f, At: last.RecordedAt, ID: last.ID}).Encode()
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

func exportURL(vendor, id string) string {
	return "/api/payments/vendor/" + vendor + "/statement-exports/" + id
}

func scanExport(row pgx.Row) (*domain.StatementExport, error) {
	var e domain.StatementExport
	var snapshot []byte
	err := row.Scan(&e.ID, &e.VendorID, &e.RequesterID, &e.IdempotencyKey, &e.Status, &snapshot, &e.ObjectKey, &e.Checksum, &e.ExpiresAt, &e.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.NotFound("Statement export not found")
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(snapshot, &e.Snapshot); err != nil {
		return nil, err
	}
	e.StatusURL = exportURL(e.VendorID, e.ID)
	return &e, nil
}

const exportColumns = `id,vendor_id,requester_id,idempotency_key,status,snapshot,object_key,checksum,expires_at,attempts`

func (r StatementRepository) Export(ctx context.Context, vendor, id string) (*domain.StatementExport, error) {
	return scanExport(r.Pool.QueryRow(ctx, `SELECT `+exportColumns+` FROM statement_exports WHERE vendor_id=$1 AND id=$2`, vendor, id))
}

func (r StatementRepository) ExportByKey(ctx context.Context, vendor, key string) (*domain.StatementExport, error) {
	return scanExport(r.Pool.QueryRow(ctx, `SELECT `+exportColumns+` FROM statement_exports WHERE vendor_id=$1 AND idempotency_key=$2`, vendor, key))
}

// SaveExport materializes exactly the rows visible to Read's read-only repeatable
// read transaction. A delayed commit with an older created_at cannot enter a retry.
func (r StatementRepository) SaveExport(ctx context.Context, actor, key string, s domain.Statement, retention time.Duration) (*domain.StatementExport, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	id := uuid.NewString()
	entries := s.Entries
	s.Entries = []domain.StatementEntry{}
	s.NextCursor = ""
	snapshot, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	filter, err := json.Marshal(s.Filter)
	if err != nil {
		return nil, err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO statement_exports(id,vendor_id,requester_id,idempotency_key,filters,cutoff,snapshot,object_key,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT(vendor_id,idempotency_key) DO NOTHING`, id, s.VendorID, actor, key, filter, s.AsOf, snapshot, "statements/"+id+".csv", s.AsOf.Add(retention))
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		// The conflicting insert has committed. Read its receipt on this
		// transaction's connection; acquiring another can exhaust the pool
		// when concurrent duplicate requests are holding all connections.
		return scanExport(tx.QueryRow(ctx, `SELECT `+exportColumns+` FROM statement_exports WHERE vendor_id=$1 AND idempotency_key=$2`, s.VendorID, key))
	}
	_, err = tx.CopyFrom(ctx, pgx.Identifier{"statement_export_entries"}, []string{"export_id", "position", "entry"}, pgx.CopyFromSlice(len(entries), func(i int) ([]any, error) { b, err := json.Marshal(entries[i]); return []any{id, i, b}, err }))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return r.Export(ctx, s.VendorID, id)
}

func (r StatementRepository) ClaimExport(ctx context.Context) (*domain.StatementExport, error) {
	return scanExport(r.Pool.QueryRow(ctx, `UPDATE statement_exports SET status='processing',attempts=attempts+1,lease_until=now()+interval '5 minutes'
	 WHERE id=(SELECT id FROM statement_exports WHERE status IN ('pending','processing') AND next_attempt_at<=now() AND (lease_until IS NULL OR lease_until<now()) AND expires_at>now() AND attempts<5 ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING `+exportColumns))
}

func (r StatementRepository) StreamExport(ctx context.Context, id string, consume func(func() (domain.StatementEntry, bool, error)) error) error {
	rows, err := r.Pool.Query(ctx, `SELECT entry FROM statement_export_entries WHERE export_id=$1 ORDER BY position`, id)
	if err != nil {
		return err
	}
	defer rows.Close()
	return consume(func() (domain.StatementEntry, bool, error) {
		var e domain.StatementEntry
		if !rows.Next() {
			return e, false, rows.Err()
		}
		var b []byte
		if err := rows.Scan(&b); err != nil {
			return e, false, err
		}
		err := json.Unmarshal(b, &e)
		return e, err == nil, err
	})
}

func (r StatementRepository) FinishExport(ctx context.Context, e *domain.StatementExport, checksum string, success bool) error {
	status := "pending"
	if success {
		status = "ready"
	} else if e.Attempts >= 5 {
		status = "failed"
	}
	tag, err := r.Pool.Exec(ctx, `UPDATE statement_exports SET status=$3,checksum=CASE WHEN $4<>'' THEN $4 ELSE checksum END,lease_until=NULL,next_attempt_at=now()+(($5+floor(random()*5))*interval '1 second') WHERE id=$1 AND attempts=$2 AND status='processing'`, e.ID, e.Attempts, status, checksum, e.Attempts*e.Attempts*5)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("statement export lease lost")
	}
	return nil
}

func (r StatementRepository) SealExport(ctx context.Context, e *domain.StatementExport, checksum string) error {
	tag, err := r.Pool.Exec(ctx, `UPDATE statement_exports SET checksum=$3 WHERE id=$1 AND attempts=$2 AND status='processing' AND (checksum='' OR checksum=$3)`, e.ID, e.Attempts, checksum)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return apperror.Conflict("Export checksum changed or worker lease was lost")
	}
	return nil
}

// ExpiredExport returns one operation for idempotent object cleanup, including a
// partially uploaded file. A crashed fifth attempt is made terminal as well.
func (r StatementRepository) ExpiredExport(ctx context.Context) (*domain.StatementExport, error) {
	if _, err := r.Pool.Exec(ctx, `UPDATE statement_exports SET status='failed' WHERE status='processing' AND attempts>=5 AND lease_until<now()`); err != nil {
		return nil, err
	}
	return scanExport(r.Pool.QueryRow(ctx, `SELECT `+exportColumns+` FROM statement_exports WHERE expires_at<=now() AND status<>'expired' AND (lease_until IS NULL OR lease_until<now()) ORDER BY expires_at LIMIT 1`))
}

func (r StatementRepository) MarkExpired(ctx context.Context, id string) error {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM statement_export_entries WHERE export_id=$1`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE statement_exports SET status='expired' WHERE id=$1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r StatementRepository) Payout(ctx context.Context, vendor, id string) (*domain.StatementPayout, error) {
	var p domain.StatementPayout
	err := r.Pool.QueryRow(ctx, `SELECT i.id,i.payout_batch_id,i.amount,COALESCE(i.currency,''),i.status,COALESCE(i.destination_mask,''),COALESCE((SELECT array_agg(l.entry_id::text ORDER BY l.entry_id) FROM payout_item_entries l WHERE l.payout_item_id=i.id),'{}'::text[]) FROM payout_items i WHERE i.vendor_id=$1 AND i.id=$2`, vendor, id).Scan(&p.ID, &p.BatchID, &p.Amount, &p.Currency, &p.Status, &p.MaskedDestination, &p.EntryRefs)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.NotFound("Payout not found")
	}
	return &p, err
}
