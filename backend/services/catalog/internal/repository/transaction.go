package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type transactionKey struct{}
type queryer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func connection(ctx context.Context, pool *pgxpool.Pool) queryer {
	if tx, ok := ctx.Value(transactionKey{}).(pgx.Tx); ok {
		return tx
	}
	return pool
}
func lockProduct(ctx context.Context) string {
	if _, ok := ctx.Value(transactionKey{}).(pgx.Tx); ok {
		return " FOR NO KEY UPDATE"
	}
	return ""
}

type Transactions struct{ Pool *pgxpool.Pool }

func (r Transactions) Run(ctx context.Context, fn func(context.Context) error) (err error) {
	if _, ok := ctx.Value(transactionKey{}).(pgx.Tx); ok {
		return fn(ctx)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin catalog transaction: %w", err)
	}
	defer func() {
		rollbackCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer stop()
		if rollbackErr := tx.Rollback(rollbackCtx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			err = errors.Join(err, fmt.Errorf("rollback catalog transaction: %w", rollbackErr))
		}
	}()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared(19030703)`); err != nil {
		return err
	}
	if err := fn(context.WithValue(ctx, transactionKey{}, tx)); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit catalog transaction: %w", err)
	}
	return nil
}

func begin(ctx context.Context, pool *pgxpool.Pool) (pgx.Tx, error) {
	if tx, ok := ctx.Value(transactionKey{}).(pgx.Tx); ok {
		return tx.Begin(ctx)
	}
	return pool.Begin(ctx)
}

// WithTx makes tx the transaction of ctx: repository calls and nested
// Transactions.Run join it (event consumers apply an event in the inbox
// transaction).
func WithTx(ctx context.Context, tx pgx.Tx) context.Context {
	return context.WithValue(ctx, transactionKey{}, tx)
}

// InTransaction reports whether ctx carries a transaction.
func InTransaction(ctx context.Context) bool {
	_, ok := ctx.Value(transactionKey{}).(pgx.Tx)
	return ok
}
