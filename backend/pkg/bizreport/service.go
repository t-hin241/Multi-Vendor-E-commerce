package bizreport

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/httpresponse"
	"shopee/backend/pkg/middleware"
)

// Source is one owner's report queries. Report and Drill run under the
// owner's own database; vendor-scoped calls always carry Range.VendorID.
type Source interface {
	Report(ctx context.Context, r Range) (any, error)
	Drill(ctx context.Context, d Drilldown) (any, error)
	// DrillMetrics are the figures this owner can list ids for.
	DrillMetrics() []string
}

// RoleChecker re-verifies the admin with Identity (fail closed).
type RoleChecker interface {
	RequireRole(ctx context.Context, userID, role string) error
}

// ShopCheck confirms the actor may read the shop's report (AF-17 shop
// permission, asked of Vendor); a refusal is 403, an unknown answer 503.
type ShopCheck func(ctx context.Context, actorID, vendorID string) error

// Service is the use case in front of an owner's Source: feature flag,
// who may read which scope, and a query deadline. The owner's own route
// guard (AF-19 bundle) runs before it for admins.
type Service struct {
	Source Source
	// Enabled is FEATURE_BUSINESS_REPORTS_ENABLED of the owner.
	Enabled bool
	Admins  RoleChecker
	Shops   ShopCheck
	// Timeout bounds one report (default 2 s, the internal call budget the
	// Admin BFF waits for).
	Timeout time.Duration
}

func (s Service) deadline(ctx context.Context) (context.Context, context.CancelFunc) {
	t := s.Timeout
	if t <= 0 {
		t = 2 * time.Second
	}
	return context.WithTimeout(ctx, t)
}

func (s Service) admin(ctx context.Context, actor string) error {
	if !s.Enabled {
		return Disabled()
	}
	if s.Admins == nil {
		return apperror.Internal(errors.New("admin verification is not configured"))
	}
	return s.Admins.RequireRole(ctx, actor, "admin")
}

func (s Service) shop(ctx context.Context, actor, vendorID string) error {
	if !s.Enabled {
		return Disabled()
	}
	if vendorID == "" {
		return InvalidRange("vendor_id is required")
	}
	if s.Shops == nil {
		return apperror.Internal(errors.New("shop authorization is not configured"))
	}
	return s.Shops(ctx, actor, vendorID)
}

func (s Service) run(ctx context.Context, f func(context.Context) (any, error)) (any, error) {
	ctx, cancel := s.deadline(ctx)
	defer cancel()
	out, err := f(ctx)
	if err == nil {
		return out, nil
	}
	var app *apperror.Error
	if errors.As(err, &app) {
		return nil, app
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return nil, &apperror.Error{Code: "report_timeout", Status: http.StatusServiceUnavailable,
			Message: "The report took too long; narrow the period or retry", Err: err}
	}
	return nil, apperror.Internal(err)
}

// AdminReport: any shop or one (r.VendorID), for an admin.
func (s Service) AdminReport(ctx context.Context, actor string, r Range) (any, error) {
	if err := s.admin(ctx, actor); err != nil {
		return nil, err
	}
	return s.run(ctx, func(ctx context.Context) (any, error) { return s.Source.Report(ctx, r) })
}

func (s Service) AdminDrill(ctx context.Context, actor string, d Drilldown) (any, error) {
	if err := s.admin(ctx, actor); err != nil {
		return nil, err
	}
	return s.run(ctx, func(ctx context.Context) (any, error) { return s.Source.Drill(ctx, d) })
}

// ShopReport: the actor's own shop only; the shop comes from the request
// and is checked with Vendor, never taken on trust.
func (s Service) ShopReport(ctx context.Context, actor string, r Range) (any, error) {
	if err := s.shop(ctx, actor, r.VendorID); err != nil {
		return nil, err
	}
	return s.run(ctx, func(ctx context.Context) (any, error) { return s.Source.Report(ctx, r) })
}

func (s Service) ShopDrill(ctx context.Context, actor string, d Drilldown) (any, error) {
	if err := s.shop(ctx, actor, d.Range.VendorID); err != nil {
		return nil, err
	}
	return s.run(ctx, func(ctx context.Context) (any, error) { return s.Source.Drill(ctx, d) })
}

// Handlers serves the four routes of one owner.
type Handlers struct {
	Service Service
	Log     zerolog.Logger
}

// RegisterAdmin adds GET business-reports and business-reports/drilldown to
// an owner's admin group (behind its role middleware and AF-19 guard).
func (h Handlers) RegisterAdmin(g *gin.RouterGroup) {
	g.GET("/business-reports", h.serve(false, false))
	g.GET("/business-reports/drilldown", h.serve(false, true))
}

// RegisterShop adds the shop-scoped routes; the shop is the :vendorId path
// parameter when the group has one, else the vendor_id query parameter.
func (h Handlers) RegisterShop(g *gin.RouterGroup) {
	g.GET("/business-reports", h.serve(true, false))
	g.GET("/business-reports/drilldown", h.serve(true, true))
}

func (h Handlers) serve(shop, drill bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		vendorID := c.Query("vendor_id")
		if shop {
			if p := c.Param("vendorId"); p != "" {
				vendorID = p
			}
		}
		r, err := ParseRange(c.Query("from"), c.Query("to"), c.Query("group"), vendorID)
		if err != nil {
			httpresponse.HandleError(c, h.Log, err)
			return
		}
		ctx, actor := c.Request.Context(), middleware.GetUserID(c)
		var out any
		switch {
		case drill:
			d, derr := ParseDrilldown(r, c.Query("metric"), c.Query("cutoff"), c.Query("cursor"), c.Query("limit"), h.Service.Source.DrillMetrics())
			if derr != nil {
				httpresponse.HandleError(c, h.Log, derr)
				return
			}
			if shop {
				out, err = h.Service.ShopDrill(ctx, actor, d)
			} else {
				out, err = h.Service.AdminDrill(ctx, actor, d)
			}
		case shop:
			out, err = h.Service.ShopReport(ctx, actor, r)
		default:
			out, err = h.Service.AdminReport(ctx, actor, r)
		}
		if err != nil {
			httpresponse.HandleError(c, h.Log, err)
			return
		}
		c.Header("Cache-Control", "no-store")
		httpresponse.OK(c, http.StatusOK, out)
	}
}

// Snapshot runs fn in one read-only REPEATABLE READ transaction, so every
// figure of a report comes from the same snapshot; asOf is that snapshot's
// time (the report's cutoff).
func Snapshot(ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx, asOf time.Time) error) error {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return fmt.Errorf("begin report transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var asOf time.Time
	if err := tx.QueryRow(ctx, `SELECT now()`).Scan(&asOf); err != nil {
		return err
	}
	return fn(tx, asOf.UTC())
}
