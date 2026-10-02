package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrStaleState: a compare-and-set update found the row in another state.
var ErrStaleState = errors.New("repository: state changed concurrently")

type txKey struct{}

type queryer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// connection is the transaction carried by ctx, or the pool.
func connection(ctx context.Context, pool *pgxpool.Pool) queryer {
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return tx
	}
	return pool
}

func inTransaction(ctx context.Context) bool {
	_, ok := ctx.Value(txKey{}).(pgx.Tx)
	return ok
}

// Transactions runs a use case's writes in one database transaction; the
// repositories pick it up from the context. A nested Run joins the outer
// transaction.
type Transactions struct{ Pool *pgxpool.Pool }

func (t Transactions) Run(ctx context.Context, fn func(ctx context.Context) error) (err error) {
	if inTransaction(ctx) {
		return fn(ctx)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	tx, err := t.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer stop()
		if e := tx.Rollback(cleanup); e != nil && !errors.Is(e, pgx.ErrTxClosed) {
			err = errors.Join(err, e)
		}
	}()
	if err := fn(context.WithValue(ctx, txKey{}, tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// WithTx makes tx the transaction of ctx: repository calls and nested
// Transactions.Run join it (event consumers apply an event in the inbox
// transaction).
func WithTx(ctx context.Context, tx pgx.Tx) context.Context {
	return context.WithValue(ctx, txKey{}, tx)
}

// InTransaction reports whether ctx carries a transaction.
func InTransaction(ctx context.Context) bool {
	_, ok := ctx.Value(txKey{}).(pgx.Tx)
	return ok
}
