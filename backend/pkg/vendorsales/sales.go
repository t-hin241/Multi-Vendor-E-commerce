// Package vendorsales implements the versioned selling-permission contract.
package vendorsales

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/eventbus"
	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/serviceauth"
	"shopee/backend/pkg/telemetry"
)

type Status struct {
	VendorID string `json:"vendor_id"`
	Status   string `json:"status"`
	Version  int64  `json:"version"`
}

func (s Status) Valid() bool {
	_, err := uuid.Parse(s.VendorID)
	return err == nil && s.Version > 0 && (s.Status == "pending" || s.Status == "approved" || s.Status == "rejected" || s.Status == "suspended")
}

type Store struct{ Pool *pgxpool.Pool }

func (s Store) Apply(ctx context.Context, v Status) error {
	return applyStatus(ctx, s.Pool, v)
}

// execer is a pool or a transaction.
type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// EventHandler applies vendor.status_changed events in the inbox
// transaction (an older version than the one held is ignored).
func (s Store) EventHandler() eventbus.Handler {
	return func(ctx context.Context, tx pgx.Tx, env eventbus.Envelope) error {
		var v Status
		if err := env.Decode(&v); err != nil {
			return err
		}
		return applyStatus(ctx, tx, v)
	}
}

// ApplyAll applies a page of statuses in one statement, with the same
// version rule as Apply for each row. Reconciliation refreshes every shop's
// storefront freshness bound (confirmed_at) every cycle: one statement per
// shop made a cycle slower than the bound under load, and shops dropped off
// the storefront.
func (s Store) ApplyAll(ctx context.Context, vs []Status) error {
	latest := make(map[string]Status, len(vs))
	for _, v := range vs {
		if !v.Valid() {
			return apperror.Validation("Invalid vendor status event")
		}
		// One row per shop: a statement may not update the same row twice.
		if held, ok := latest[v.VendorID]; !ok || v.Version > held.Version {
			latest[v.VendorID] = v
		}
	}
	if len(latest) == 0 {
		return nil
	}
	// Rows in one fixed order, so concurrent writers of the same shops lock
	// them in the same order and cannot deadlock.
	ids := make([]string, 0, len(latest))
	for id := range latest {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	statuses := make([]string, 0, len(latest))
	versions := make([]int64, 0, len(latest))
	for _, id := range ids {
		v := latest[id]
		statuses = append(statuses, v.Status)
		versions = append(versions, v.Version)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := s.Pool.Exec(ctx, `INSERT INTO vendor_sale_status(vendor_id,status,version)
 SELECT * FROM unnest($1::uuid[], $2::text[], $3::bigint[]) ORDER BY 1
 ON CONFLICT(vendor_id) DO UPDATE SET status=EXCLUDED.status,version=EXCLUDED.version,confirmed_at=now()
 WHERE vendor_sale_status.version<EXCLUDED.version OR (vendor_sale_status.version=EXCLUDED.version AND vendor_sale_status.status=EXCLUDED.status)`, ids, statuses, versions)
	return err
}

func applyStatus(ctx context.Context, q execer, v Status) error {
	if !v.Valid() {
		return apperror.Validation("Invalid vendor status event")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_, err := q.Exec(ctx, `INSERT INTO vendor_sale_status(vendor_id,status,version) VALUES($1,$2,$3)
 ON CONFLICT(vendor_id) DO UPDATE SET status=EXCLUDED.status,version=EXCLUDED.version,confirmed_at=now()
 WHERE vendor_sale_status.version<EXCLUDED.version OR (vendor_sale_status.version=EXCLUDED.version AND vendor_sale_status.status=EXCLUDED.status)`, v.VendorID, v.Status, v.Version)
	return err
}
func (s Store) Handler(log zerolog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		var v Status
		if err := c.ShouldBindJSON(&v); err != nil {
			httpresponse.HandleError(c, log, apperror.Validation("Invalid vendor status event"))
			return
		}
		if err := s.Apply(c.Request.Context(), v); err != nil {
			httpresponse.HandleError(c, log, err)
			return
		}
		httpresponse.OK(c, http.StatusOK, v)
	}
}

// LockApproved fences order creation against the status consumer's exclusive update.
func LockApproved(ctx context.Context, tx pgx.Tx, versions map[string]int64) error {
	ids := make([]string, 0, len(versions))
	for id := range versions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		var status string
		var version int64
		err := tx.QueryRow(ctx, `SELECT status,version FROM vendor_sale_status WHERE vendor_id=$1 FOR SHARE`, id).Scan(&status, &version)
		if err == pgx.ErrNoRows {
			return apperror.Conflict("Shop selling permission is synchronizing; retry checkout")
		}
		if err != nil {
			return err
		}
		if status != "approved" || version != versions[id] {
			return apperror.Conflict("Shop selling permission changed; refresh checkout")
		}
	}
	return nil
}

type Client struct{ URL, Key string }

func (c Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.URL, "/")+path, nil)
	if err != nil {
		return err
	}
	serviceauth.SetRequestHeaders(req, c.Key)
	resp, err := telemetry.NewHTTPClient(3 * time.Second).Do(req)
	if err != nil {
		return fmt.Errorf("vendor status unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("vendor status returned %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 128<<10)).Decode(out)
}
func (c Client) Approved(ctx context.Context, ids []string) (map[string]int64, error) {
	var body struct {
		Data []Status `json:"data"`
	}
	if err := c.get(ctx, "/internal/vendors/sale-status?ids="+url.QueryEscape(strings.Join(ids, ",")), &body); err != nil {
		return nil, apperror.Internal(err)
	}
	versions := map[string]int64{}
	for _, v := range body.Data {
		if v.Valid() && v.Status == "approved" {
			versions[v.VendorID] = v.Version
		}
	}
	for _, id := range ids {
		if versions[id] == 0 {
			return nil, apperror.Conflict("A shop is not approved to sell")
		}
	}
	return versions, nil
}

// Reconcile repairs missed deliveries and refreshes the storefront freshness bound.
func (c Client) Reconcile(ctx context.Context, store Store, log zerolog.Logger) {
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		after := ""
		for {
			var body struct {
				Data []Status `json:"data"`
			}
			err := c.get(ctx, "/internal/vendors/sale-status?after="+url.QueryEscape(after), &body)
			if err != nil {
				log.Warn().Msg("vendor_status_reconciliation_failed")
				break
			}
			if err = store.ApplyAll(ctx, body.Data); err != nil {
				log.Error().Msg("vendor_status_reconciliation_apply_failed")
				break
			}
			if len(body.Data) > 0 {
				after = body.Data[len(body.Data)-1].VendorID
			}
			if len(body.Data) < 100 {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
