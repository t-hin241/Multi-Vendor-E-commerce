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

// queryTimeout bounds every single statement run outside a transaction.
const queryTimeout = 5 * time.Second

type transactionKey struct{}

type queryer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

// connection returns the transaction carried by ctx, or the pool when the
// call is not part of one.
func connection(ctx context.Context, pool *pgxpool.Pool) queryer {
	if tx, ok := ctx.Value(transactionKey{}).(pgx.Tx); ok {
		return tx
	}
	return pool
}

func inTransaction(ctx context.Context) bool {
	_, ok := ctx.Value(transactionKey{}).(pgx.Tx)
	return ok
}

// Transactions lets the use case own the transaction boundary: every
// repository call made with the ctx passed to fn joins the same transaction.
type Transactions struct{ Pool *pgxpool.Pool }

func (r Transactions) Run(ctx context.Context, fn func(context.Context) error) (err error) {
	if inTransaction(ctx) {
		return fn(ctx)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin cart transaction: %w", err)
	}
	defer func() {
		rollbackCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer stop()
		if rollbackErr := tx.Rollback(rollbackCtx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			err = errors.Join(err, fmt.Errorf("rollback cart transaction: %w", rollbackErr))
		}
	}()
	if err := fn(context.WithValue(ctx, transactionKey{}, tx)); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit cart transaction: %w", err)
	}
	return nil
}

// statementContext applies queryTimeout to a standalone statement; inside a
// transaction the transaction's own deadline already applies.
func statementContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if inTransaction(ctx) {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, queryTimeout)
}
