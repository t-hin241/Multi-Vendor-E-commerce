package usecase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"shopee/backend/pkg/adminaccess"
	"shopee/backend/pkg/apperror"
	"shopee/backend/pkg/shopaccess"
	"shopee/backend/services/payment/internal/domain"
	"shopee/backend/services/payment/internal/repository"
)

type StatementStore interface {
	Read(context.Context, string, domain.StatementFilter, *domain.StatementCursor, int, repository.HoldLookup) (*domain.Statement, error)
	Export(context.Context, string, string) (*domain.StatementExport, error)
	ExportByKey(context.Context, string, string) (*domain.StatementExport, error)
	SaveExport(context.Context, string, string, domain.Statement, time.Duration) (*domain.StatementExport, error)
	ClaimExport(context.Context) (*domain.StatementExport, error)
	StreamExport(context.Context, string, func(func() (domain.StatementEntry, bool, error)) error) error
	SealExport(context.Context, *domain.StatementExport, string) error
	FinishExport(context.Context, *domain.StatementExport, string, bool) error
	ExpiredExport(context.Context) (*domain.StatementExport, error)
	MarkExpired(context.Context, string) error
	Payout(context.Context, string, string) (*domain.StatementPayout, error)
}
type StatementStorage interface {
	PutStream(context.Context, string, io.Reader, string) error
	DownloadURL(context.Context, string, time.Duration) (string, error)
	Delete(context.Context, string) error
}
type StatementShops interface {
	Authorize(context.Context, string, string, string) (shopaccess.Grant, error)
}
type StatementAdmins interface {
	Require(context.Context, string, string) (int64, error)
}

type StatementUseCase struct {
	Store     StatementStore
	Storage   StatementStorage
	Shops     StatementShops
	Admins    StatementAdmins
	Holds     repository.HoldLookup
	Enabled   bool
	Retention time.Duration
	Log       zerolog.Logger
}

func statementError(err error) error {
	if err == nil {
		return nil
	}
	var app *apperror.Error
	if errors.As(err, &app) && app.Status < 500 {
		return err
	}
	return &apperror.Error{Code: "statements_unavailable", Message: "Financial statements are temporarily unavailable", Status: http.StatusServiceUnavailable, Err: err}
}
func (uc *StatementUseCase) authorize(ctx context.Context, actor, vendor string, admin bool) error {
	if _, err := uuid.Parse(vendor); err != nil {
		return domain.StatementInvalid("vendor_id must be a UUID")
	}
	if actor == "" {
		return apperror.Unauthorized("Authentication required")
	}
	if admin {
		if uc.Admins == nil {
			return statementError(errors.New("admin authorization unavailable"))
		}
		_, err := uc.Admins.Require(ctx, actor, adminaccess.FinanceRead)
		return err
	}
	if uc.Shops == nil {
		return shopaccess.Unavailable(errors.New("shop authorization unavailable"))
	}
	_, err := uc.Shops.Authorize(ctx, actor, vendor, shopaccess.FinanceRead)
	return err
}

func (uc *StatementUseCase) Capability(ctx context.Context, actor, vendor string, admin bool) (bool, error) {
	if err := uc.authorize(ctx, actor, vendor, admin); err != nil {
		return false, err
	}
	return uc.Enabled, nil
}
func (uc *StatementUseCase) Read(ctx context.Context, actor, vendor string, admin bool, f domain.StatementFilter, raw string, limit int) (*domain.Statement, error) {
	if err := uc.authorize(ctx, actor, vendor, admin); err != nil {
		return nil, err
	}
	if !uc.Enabled {
		return nil, apperror.NotFound("Vendor statements are disabled")
	}
	if err := f.Validate(); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 100 {
		return nil, domain.StatementInvalid("limit must be 1-100")
	}
	c, err := domain.ParseStatementCursor(raw, vendor, f)
	if err != nil {
		return nil, err
	}
	s, err := uc.Store.Read(ctx, vendor, f, c, limit, uc.Holds)
	return s, statementError(err)
}
func sameExport(e *domain.StatementExport, actor string, f domain.StatementFilter) error {
	if e.RequesterID != actor || !e.Snapshot.Filter.From.Equal(f.From) || !e.Snapshot.Filter.To.Equal(f.To) || e.Snapshot.Filter.Currency != f.Currency {
		return apperror.Conflict("Idempotency-Key already used with another requester or period")
	}
	return nil
}
func (uc *StatementUseCase) CreateExport(ctx context.Context, actor, vendor, key, format string, f domain.StatementFilter) (*domain.StatementExport, error) {
	if err := uc.authorize(ctx, actor, vendor, false); err != nil {
		return nil, err
	}
	if !uc.Enabled {
		return nil, apperror.NotFound("New statement exports are disabled")
	}
	if err := f.Validate(); err != nil {
		return nil, err
	}
	if format != "csv" {
		return nil, domain.StatementInvalid("format must be csv")
	}
	if err := domain.ValidateIdempotencyKey(key); err != nil {
		return nil, domain.StatementInvalid("Idempotency-Key must be 8-128 printable characters")
	}
	e, err := uc.Store.ExportByKey(ctx, vendor, key)
	if err == nil {
		return e, sameExport(e, actor, f)
	}
	var app *apperror.Error
	if !errors.As(err, &app) || app.Status != 404 {
		return nil, statementError(err)
	}
	if uc.Storage == nil {
		return nil, statementError(errors.New("private storage not configured"))
	}
	s, err := uc.Store.Read(ctx, vendor, f, nil, domain.StatementMaxRows, uc.Holds)
	if err != nil {
		return nil, statementError(err)
	}
	if s.NextCursor != "" {
		return nil, domain.StatementInvalid("Export exceeds 100000 rows; split the period")
	}
	if !s.BalancesAvailable {
		return nil, &apperror.Error{Code: "statement_history_gap", Message: "Historical currency is missing; reconciliation is required before export", Status: 503}
	}
	// Recheck after the potentially long read, before persisting the operation.
	if err := uc.authorize(ctx, actor, vendor, false); err != nil {
		return nil, err
	}
	e, err = uc.Store.SaveExport(ctx, actor, key, *s, uc.Retention)
	if err != nil {
		return nil, statementError(err)
	}
	return e, sameExport(e, actor, f)
}
func (uc *StatementUseCase) Export(ctx context.Context, actor, vendor, id string) (*domain.StatementExport, error) {
	if err := uc.authorize(ctx, actor, vendor, false); err != nil {
		return nil, err
	}
	if _, err := uuid.Parse(id); err != nil {
		return nil, domain.StatementInvalid("operation_id must be a UUID")
	}
	e, err := uc.Store.Export(ctx, vendor, id)
	if err != nil {
		return nil, statementError(err)
	}
	if !time.Now().Before(e.ExpiresAt) {
		e.Status = "expired"
	}
	return e, nil
}
func (uc *StatementUseCase) Download(ctx context.Context, actor, vendor, id string) (string, time.Time, error) {
	e, err := uc.Export(ctx, actor, vendor, id)
	if err != nil {
		return "", time.Time{}, err
	}
	if e.Status != "ready" {
		return "", time.Time{}, apperror.Conflict("Export is not ready or has expired")
	}
	if uc.Storage == nil {
		return "", time.Time{}, statementError(errors.New("private storage unavailable"))
	}
	ttl := min(60*time.Second, time.Until(e.ExpiresAt))
	if ttl < time.Second {
		return "", time.Time{}, apperror.Conflict("Export has expired")
	}
	u, err := uc.Storage.DownloadURL(ctx, e.ObjectKey, ttl)
	return u, time.Now().Add(ttl), statementError(err)
}
func (uc *StatementUseCase) Payout(ctx context.Context, actor, vendor, id string, admin bool) (*domain.StatementPayout, error) {
	if err := uc.authorize(ctx, actor, vendor, admin); err != nil {
		return nil, err
	}
	if !uc.Enabled {
		return nil, apperror.NotFound("Vendor statements are disabled")
	}
	if _, err := uuid.Parse(id); err != nil {
		return nil, domain.StatementInvalid("payout id must be a UUID")
	}
	p, err := uc.Store.Payout(ctx, vendor, id)
	if err == nil && p.Currency == "" {
		return nil, &apperror.Error{Code: "statement_history_gap", Message: "Historical payout currency is unavailable", Status: http.StatusServiceUnavailable}
	}
	return p, statementError(err)
}

// ProcessExport runs one bounded attempt. Every retry streams the saved rows
// under the same operation key, so a crash cannot introduce newer entries.
func (uc *StatementUseCase) ProcessExport(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	e, err := uc.Store.ClaimExport(ctx)
	if err != nil {
		var app *apperror.Error
		if errors.As(err, &app) && app.Status == 404 {
			return nil
		}
		return err
	}
	err = uc.authorize(ctx, e.RequesterID, e.VendorID, false)
	checksum := ""
	if err == nil {
		checksum, err = uc.upload(ctx, e)
	}
	if err != nil {
		uc.Log.Warn().Str("operation_id", e.ID).Msg("statement_export_attempt_failed")
	}
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer finishCancel()
	return errors.Join(err, uc.Store.FinishExport(finishCtx, e, checksum, err == nil))
}
func (uc *StatementUseCase) upload(ctx context.Context, e *domain.StatementExport) (string, error) {
	if uc.Storage == nil {
		return "", errors.New("private storage unavailable")
	}
	hash := sha256.New()
	if err := uc.Store.StreamExport(ctx, e.ID, func(next func() (domain.StatementEntry, bool, error)) error {
		return domain.WriteStatementCSV(hash, e.Snapshot, next)
	}); err != nil {
		return "", err
	}
	checksum := hex.EncodeToString(hash.Sum(nil))
	// Seal the bytes before touching S3. A retry under a changed CSV encoder
	// must never overwrite an earlier object with different content.
	if err := uc.Store.SealExport(ctx, e, checksum); err != nil {
		return "", err
	}
	r, w := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := uc.Store.StreamExport(ctx, e.ID, func(next func() (domain.StatementEntry, bool, error)) error {
			return domain.WriteStatementCSV(w, e.Snapshot, next)
		})
		_ = w.CloseWithError(err)
		done <- err
	}()
	err := uc.Storage.PutStream(ctx, e.ObjectKey, r, "text/csv; charset=utf-8")
	_ = r.CloseWithError(err)
	if streamErr := <-done; err != nil || streamErr != nil {
		return "", errors.Join(err, streamErr)
	}
	return checksum, nil
}
func (uc *StatementUseCase) cleanup(ctx context.Context) error {
	if uc.Storage == nil {
		return nil
	}
	e, err := uc.Store.ExpiredExport(ctx)
	if err != nil {
		var app *apperror.Error
		if errors.As(err, &app) && app.Status == 404 {
			return nil
		}
		return err
	}
	if err := uc.Storage.Delete(ctx, e.ObjectKey); err != nil {
		return err
	}
	return uc.Store.MarkExpired(ctx, e.ID)
}
func (uc *StatementUseCase) Run(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// The flag closes new requests; already accepted operations drain.
			if uc.Storage != nil {
				if err := uc.ProcessExport(ctx); err != nil {
					uc.Log.Error().Err(err).Msg("statement_export_failed")
				}
			}
			cleanCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := uc.cleanup(cleanCtx)
			cancel()
			if err != nil {
				uc.Log.Error().Err(err).Msg("statement_export_cleanup_failed")
			}
		}
	}
}

func statementTime(t time.Time) *time.Time { return &t }
