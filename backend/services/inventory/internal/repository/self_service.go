package repository

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/shopaccess"
	"shopee/backend/services/inventory/internal/domain"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type StockSelfService struct{ Pool *pgxpool.Pool }

func (r StockSelfService) Policy(ctx context.Context, vendor string) (domain.StockPolicy, error) {
	p := domain.StockPolicy{Limits: domain.StockLimits{Receipt: 10000}}
	err := connection(ctx, r.Pool).QueryRow(ctx, `SELECT enabled,receipt_limit,version FROM inventory_vendor_policy WHERE vendor_id=$1`, vendor).Scan(&p.Enabled, &p.Limits.Receipt, &p.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	return p, err
}
func (r StockSelfService) LockPolicy(ctx context.Context, vendor string) (domain.StockPolicy, error) {
	q := connection(ctx, r.Pool)
	// All AF-13 commands serialize policy changes with operations, including
	// first enable (where no policy row exists yet). No HTTP under this lock.
	if _, err := q.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('stock-policy:'||$1,0))`, vendor); err != nil {
		return domain.StockPolicy{}, err
	}
	return r.Policy(ctx, vendor)
}
func payloadHash(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// Command receipts also protect policy/settings/admin decisions from retries.
func (r StockSelfService) Replay(ctx context.Context, scope, key string, payload any, out any) (bool, error) {
	hash, err := payloadHash(payload)
	if err != nil {
		return false, err
	}
	q := connection(ctx, r.Pool)
	if _, err = q.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, scope+":"+key); err != nil {
		return false, err
	}
	var stored string
	var b []byte
	err = q.QueryRow(ctx, `SELECT payload_hash,receipt FROM inventory_self_service_receipts WHERE scope=$1 AND operation_key=$2`, scope, key).Scan(&stored, &b)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if stored != hash {
		return false, domain.StockConflict("idempotency_conflict", "Idempotency-Key was used with a different payload")
	}
	return true, json.Unmarshal(b, out)
}
func (r StockSelfService) SaveReceipt(ctx context.Context, scope, key string, payload, out any) error {
	hash, err := payloadHash(payload)
	if err != nil {
		return err
	}
	b, err := json.Marshal(out)
	if err != nil {
		return err
	}
	_, err = connection(ctx, r.Pool).Exec(ctx, `INSERT INTO inventory_self_service_receipts(scope,operation_key,payload_hash,receipt) VALUES($1,$2,$3,$4)`, scope, key, hash, b)
	return err
}
func (r StockSelfService) SetPolicy(ctx context.Context, vendor string, c domain.PolicyChange) (domain.StockPolicy, error) {
	p := domain.StockPolicy{Enabled: c.Enabled, Limits: c.Limits}
	err := connection(ctx, r.Pool).QueryRow(ctx, `INSERT INTO inventory_vendor_policy(vendor_id,enabled,receipt_limit,version) VALUES($1,$2,$3,1)
 ON CONFLICT(vendor_id) DO UPDATE SET enabled=$2,receipt_limit=$3,version=inventory_vendor_policy.version+1 RETURNING version`, vendor, c.Enabled, c.Limits.Receipt).Scan(&p.Version)
	return p, err
}
func (r StockSelfService) SetAlerts(ctx context.Context, item string, c domain.AlertSettings) (domain.AlertSettings, error) {
	err := connection(ctx, r.Pool).QueryRow(ctx, `UPDATE inventory_items SET low_stock_threshold=$2,alert_enabled=$3 WHERE id=$1 AND version=$4 RETURNING version`, item, c.LowStockThreshold, c.Enabled, c.ExpectedVersion).Scan(&c.ExpectedVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, domain.StockConflict("version_conflict", "Stock changed; reload before saving alert settings")
	}
	return c, err
}

func (r StockSelfService) Adjust(ctx context.Context, vendor, actor, key string, a domain.StockAdjustment, p domain.StockPolicy) (out domain.AdjustmentReceipt, err error) {
	q := connection(ctx, r.Pool)
	hash, err := payloadHash(struct {
		Actor string
		Input domain.StockAdjustment
	}{actor, a})
	if err != nil {
		return out, err
	}
	var stored string
	var b []byte
	err = q.QueryRow(ctx, `SELECT payload_hash,receipt FROM stock_adjustment_operations WHERE vendor_id=$1 AND operation_key=$2`, vendor, key).Scan(&stored, &b)
	if err == nil {
		if stored != hash {
			return out, domain.StockConflict("idempotency_conflict", "Idempotency-Key was used with a different payload")
		}
		err = json.Unmarshal(b, &out)
		return out, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	if !p.Enabled {
		return out, apperror.Forbidden("Stock self-service is not enabled for this shop")
	}
	var version, available, reserved int64
	err = q.QueryRow(ctx, `SELECT available_quantity,reserved_quantity,version FROM inventory_items WHERE id=$1 AND vendor_id=$2 FOR UPDATE`, a.ItemID, vendor).Scan(&available, &reserved, &version)
	if err != nil {
		return out, err
	}
	if version != a.ExpectedVersion {
		return out, domain.StockConflict("version_conflict", "Stock changed; count again before retrying")
	}
	delta, planErr := domain.PlanAdjustment(a, available, reserved)
	status := "applied"
	if planErr != nil {
		var app *apperror.Error
		if !errors.As(planErr, &app) || app.Code != "reserved_stock_conflict" {
			return out, planErr
		}
		status = "reserved_stock_conflict"
	} else if delta > p.Limits.Receipt {
		status = "approval_required"
	}
	out = domain.AdjustmentReceipt{Status: status, Available: available, Reserved: reserved, OnHand: available + reserved, Version: version, PolicyVersion: p.Version}
	err = q.QueryRow(ctx, `INSERT INTO stock_adjustment_operations(vendor_id,item_id,actor_user_id,operation_key,payload_hash,policy_version,kind,quantity_delta,reason,evidence_ref,expected_version,status,receipt,physical_count)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'{}',$13) RETURNING id`, vendor, a.ItemID, actor, key, hash, p.Version, a.Kind, delta, a.Reason, a.EvidenceRef, a.ExpectedVersion, status, a.PhysicalCount).Scan(&out.OperationID)
	if err != nil {
		return out, err
	}
	out.StatusURL = "/api/inventory/vendor/stock-adjustments/" + out.OperationID
	if status == "applied" {
		if err = r.apply(ctx, a.ItemID, actor, a.Kind, a.Reason, a.EvidenceRef, delta, &out); err != nil {
			return out, err
		}
	}
	return out, r.saveOperation(ctx, out)
}
func (r StockSelfService) apply(ctx context.Context, item, actor, kind, reason, evidence string, delta int64, out *domain.AdjustmentReceipt) error {
	q := connection(ctx, r.Pool)
	err := q.QueryRow(ctx, `UPDATE inventory_items SET available_quantity=available_quantity+$2,updated_at=now() WHERE id=$1 RETURNING available_quantity,reserved_quantity,version`, item, delta).Scan(&out.Available, &out.Reserved, &out.Version)
	if err != nil {
		return err
	}
	out.OnHand = out.Available + out.Reserved
	return q.QueryRow(ctx, `INSERT INTO stock_movements(inventory_item_id,change_quantity,reason,reference_id,actor_user_id,operation_key,membership_version,source,adjustment_reason,evidence_ref)
 VALUES($1,$2,$3,$4,$5,'adjustment:'||$4,$6,'vendor_self_service',$7,$8) RETURNING id`, item, delta, kind, out.OperationID, actor, shopaccess.UsedMembershipVersion(ctx), reason, evidence).Scan(&out.MovementID)
}
func (r StockSelfService) saveOperation(ctx context.Context, out domain.AdjustmentReceipt) error {
	b, err := json.Marshal(out)
	if err != nil {
		return err
	}
	_, err = connection(ctx, r.Pool).Exec(ctx, `UPDATE stock_adjustment_operations SET status=$2,receipt=$3 WHERE id=$1`, out.OperationID, out.Status, b)
	return err
}
func (r StockSelfService) Operation(ctx context.Context, id string) (domain.AdjustmentWork, domain.AdjustmentReceipt, error) {
	var w domain.AdjustmentWork
	var out domain.AdjustmentReceipt
	var b []byte
	err := connection(ctx, r.Pool).QueryRow(ctx, `SELECT id,vendor_id,actor_user_id,item_id,kind,quantity_delta,reason,evidence_ref,expected_version,policy_version,status,created_at,receipt,physical_count,first_approved_by FROM stock_adjustment_operations WHERE id=$1`, id).Scan(&w.ID, &w.VendorID, &w.ActorID, &w.ItemID, &w.Kind, &w.QuantityDelta, &w.Reason, &w.EvidenceRef, &w.ExpectedVersion, &w.PolicyVersion, &w.Status, &w.CreatedAt, &b, &w.PhysicalCount, &w.FirstApprovedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return w, out, apperror.NotFound("Stock operation not found")
	}
	if err != nil {
		return w, out, err
	}
	err = json.Unmarshal(b, &out)
	return w, out, err
}
func (r StockSelfService) Decide(ctx context.Context, id, actor, decision string, expected, secondApproval int64, p domain.StockPolicy) (domain.AdjustmentReceipt, error) {
	w, out, err := r.Operation(ctx, id)
	if err != nil {
		return out, err
	}
	if decision == "resolve" && w.Status == "reserved_stock_conflict" {
		out.Status = "resolved"
		return out, r.saveOperation(ctx, out)
	}
	if w.Status != "approval_required" {
		return out, domain.StockConflict("state_conflict", "Operation is not awaiting approval")
	}
	if decision == "reject" {
		out.Status = "rejected"
		return out, r.saveOperation(ctx, out)
	}
	if decision != "approve" {
		return out, domain.StockConflict("state_conflict", "Only shortage work items can be resolved")
	}
	if !p.Enabled || p.Version != w.PolicyVersion {
		return out, domain.StockConflict("policy_conflict", "Shop policy changed; reject this operation and request a new count")
	}
	var available, reserved, version int64
	err = connection(ctx, r.Pool).QueryRow(ctx, `SELECT available_quantity,reserved_quantity,version FROM inventory_items WHERE id=$1 FOR UPDATE`, w.ItemID).Scan(&available, &reserved, &version)
	if err != nil {
		return out, err
	}
	if version != expected || version != w.ExpectedVersion {
		return out, domain.StockConflict("version_conflict", "Stock changed; reject this operation and count again")
	}
	// Stored count operations have already been reduced to a checked delta.
	a := w.StockAdjustment
	a.Kind = "correction"
	a.PhysicalCount = nil
	if _, err = domain.PlanAdjustment(a, available, reserved); err != nil {
		return out, err
	}
	if secondApproval > 0 && w.QuantityDelta >= secondApproval {
		if w.FirstApprovedBy == nil {
			if _, err = connection(ctx, r.Pool).Exec(ctx, `UPDATE stock_adjustment_operations SET first_approved_by=$2 WHERE id=$1`, id, actor); err != nil {
				return out, err
			}
			out.FirstApprovedBy = &actor
			return out, r.saveOperation(ctx, out)
		}
		if *w.FirstApprovedBy == actor {
			return out, domain.StockConflict("second_approval_required", "A different admin must provide the second approval")
		}
	}
	out.Status = "applied"
	if err = r.apply(ctx, w.ItemID, actor, w.Kind, w.Reason, w.EvidenceRef, w.QuantityDelta, &out); err != nil {
		return out, err
	}
	return out, r.saveOperation(ctx, out)
}

type stockCursor struct {
	At time.Time `json:"at"`
	ID string    `json:"id"`
}

func parseStockCursor(raw string) (stockCursor, error) {
	if raw == "" {
		return stockCursor{At: time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC), ID: "ffffffff-ffff-ffff-ffff-ffffffffffff"}, nil
	}
	var c stockCursor
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || json.Unmarshal(b, &c) != nil || c.At.IsZero() {
		return c, domain.StockInputError("Invalid cursor")
	}
	// The id is passed as text and compared only after SQL casts the UUID.
	if _, err := uuid.Parse(c.ID); err != nil {
		return c, domain.StockInputError("Invalid cursor")
	}
	return c, nil
}
func nextStockCursor(at time.Time, id string) string {
	b, _ := json.Marshal(stockCursor{At: at, ID: id})
	return base64.RawURLEncoding.EncodeToString(b)
}
func (r StockSelfService) Movements(ctx context.Context, item, cursor string, limit int) (domain.StockPage[domain.StockMovement], error) {
	out := domain.StockPage[domain.StockMovement]{Items: []domain.StockMovement{}}
	c, err := parseStockCursor(cursor)
	if err != nil {
		return out, err
	}
	rows, err := connection(ctx, r.Pool).Query(ctx, `SELECT id,inventory_item_id,change_quantity,reserved_change,reason,actor_user_id,coalesce(source,'legacy'),coalesce(adjustment_reason,''),coalesce(evidence_ref,''),created_at FROM stock_movements
 WHERE inventory_item_id=$1 AND (created_at,id)<($2,$3::uuid) ORDER BY created_at DESC,id DESC LIMIT $4`, item, c.At, c.ID, limit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var m domain.StockMovement
		if err = rows.Scan(&m.ID, &m.ItemID, &m.QuantityDelta, &m.ReservedDelta, &m.Kind, &m.ActorID, &m.Source, &m.Reason, &m.EvidenceRef, &m.CreatedAt); err != nil {
			return out, err
		}
		out.Items = append(out.Items, m)
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		last := out.Items[limit-1]
		out.NextCursor = nextStockCursor(last.CreatedAt, last.ID)
	}
	return out, rows.Err()
}
func (r StockSelfService) Work(ctx context.Context, cursor string, limit int) (domain.StockPage[domain.AdjustmentWork], error) {
	out := domain.StockPage[domain.AdjustmentWork]{Items: []domain.AdjustmentWork{}}
	c, err := parseStockCursor(cursor)
	if err != nil {
		return out, err
	}
	rows, err := connection(ctx, r.Pool).Query(ctx, `SELECT id,vendor_id,actor_user_id,item_id,kind,quantity_delta,reason,evidence_ref,expected_version,policy_version,status,created_at,physical_count,first_approved_by FROM stock_adjustment_operations WHERE status IN ('approval_required','reserved_stock_conflict') AND (created_at,id)<($1,$2::uuid) ORDER BY created_at DESC,id DESC LIMIT $3`, c.At, c.ID, limit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var w domain.AdjustmentWork
		if err = rows.Scan(&w.ID, &w.VendorID, &w.ActorID, &w.ItemID, &w.Kind, &w.QuantityDelta, &w.Reason, &w.EvidenceRef, &w.ExpectedVersion, &w.PolicyVersion, &w.Status, &w.CreatedAt, &w.PhysicalCount, &w.FirstApprovedBy); err != nil {
			return out, err
		}
		out.Items = append(out.Items, w)
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		last := out.Items[limit-1]
		out.NextCursor = nextStockCursor(last.CreatedAt, last.ID)
	}
	return out, rows.Err()
}

// Claim without holding a transaction over Notification's HTTP request. A
// lease makes crash recovery safe; Notification deduplicates the episode id.
type StockAlert struct {
	ID, ItemID, VendorID string
	Attempt              int
}

func (r StockSelfService) ClaimAlert(ctx context.Context) (StockAlert, error) {
	var a StockAlert
	err := r.Pool.QueryRow(ctx, `UPDATE stock_alert_episode SET attempts=attempts+1,next_attempt_at=now()+interval '1 minute'
 WHERE id=(SELECT id FROM stock_alert_episode WHERE delivered_at IS NULL AND attempts<5 AND next_attempt_at<=now() ORDER BY next_attempt_at,id LIMIT 1 FOR UPDATE SKIP LOCKED)
 RETURNING id,item_id,vendor_id,attempts`).Scan(&a.ID, &a.ItemID, &a.VendorID, &a.Attempt)
	return a, err
}
func (r StockSelfService) FinishAlert(ctx context.Context, a StockAlert, success bool) error {
	tag, err := r.Pool.Exec(ctx, `UPDATE stock_alert_episode SET delivered_at=CASE WHEN $3 THEN now() ELSE NULL END,next_attempt_at=now()+make_interval(secs=>power(2,attempts)::int+floor(random()*5)::int) WHERE id=$1 AND attempts=$2 AND delivered_at IS NULL`, a.ID, a.Attempt, success)
	if err == nil && tag.RowsAffected() != 1 {
		return fmt.Errorf("stock alert lease changed")
	}
	return err
}
func (r StockSelfService) ParkAlert(ctx context.Context, a StockAlert) error {
	_, err := r.Pool.Exec(ctx, `UPDATE stock_alert_episode SET attempts=5 WHERE id=$1 AND attempts=$2 AND delivered_at IS NULL`, a.ID, a.Attempt)
	return err
}

func (r StockSelfService) AlertWork(ctx context.Context, cursor string, limit int) (domain.StockPage[domain.StockAlertWork], error) {
	out := domain.StockPage[domain.StockAlertWork]{Items: []domain.StockAlertWork{}}
	c, err := parseStockCursor(cursor)
	if err != nil {
		return out, err
	}
	rows, err := connection(ctx, r.Pool).Query(ctx, `SELECT id,item_id,vendor_id,attempts,created_at FROM stock_alert_episode WHERE delivered_at IS NULL AND attempts>=5 AND (created_at,id)<($1,$2::uuid) ORDER BY created_at DESC,id DESC LIMIT $3`, c.At, c.ID, limit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var a domain.StockAlertWork
		if err = rows.Scan(&a.ID, &a.ItemID, &a.VendorID, &a.Attempts, &a.CreatedAt); err != nil {
			return out, err
		}
		out.Items = append(out.Items, a)
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		a := out.Items[limit-1]
		out.NextCursor = nextStockCursor(a.CreatedAt, a.ID)
	}
	return out, rows.Err()
}
func (r StockSelfService) RetryAlert(ctx context.Context, id string) error {
	tag, err := connection(ctx, r.Pool).Exec(ctx, `UPDATE stock_alert_episode SET attempts=0,next_attempt_at=now() WHERE id=$1 AND attempts>=5 AND delivered_at IS NULL`, id)
	if err == nil && tag.RowsAffected() != 1 {
		return domain.StockConflict("state_conflict", "Alert is not awaiting review")
	}
	return err
}
