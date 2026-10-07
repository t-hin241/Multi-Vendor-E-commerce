package repository

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"shopee/backend/pkg/casesla"
)

func NewCaseSLAStore(pool *pgxpool.Pool) casesla.Store {
	return casesla.Store{Pool: pool, LockResource: func(ctx context.Context, tx pgx.Tx, kind, id string) error {
		if kind != "support" {
			return nil
		}
		var found string
		return tx.QueryRow(ctx, `SELECT id::text FROM support_cases WHERE id=$1 FOR UPDATE`, id).Scan(&found)
	}, AssignResource: func(ctx context.Context, tx pgx.Tx, i *casesla.Item) error {
		if i.ResourceType != "support" {
			return nil
		}
		_, e := tx.Exec(ctx, `UPDATE support_cases SET assignee_id=$2,version=version+1,updated_at=now() WHERE id=$1`, i.ResourceID, i.AssigneeID)
		return e
	}}
}
