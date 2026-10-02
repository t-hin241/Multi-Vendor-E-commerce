// Package postgres builds the pgx connection pool each service uses to talk
// to the database schema it owns.
package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPool opens a connection pool against databaseURL and verifies
// connectivity with a bounded-time ping before returning, so a service
// fails fast at startup instead of surfacing DB errors on the first request.
func NewPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("postgres: parse config: %w", err)
	}

	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 30 * time.Minute
	cfg.HealthCheckPeriod = time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: create pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: ping: %w", err)
	}

	return pool, nil
}

// RuntimeRoleProblems lists what the connected role may do beyond reading
// and writing the rows of its own database (data tooling plan 14): a
// service's runtime role must not be a superuser or create roles/databases,
// own objects (it could change the schema), connect to another database,
// or change the migration ledger. Empty means least privilege.
func RuntimeRoleProblems(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	qctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var problems []string
	rows, err := pool.Query(qctx, `
SELECT problem FROM (
  SELECT 'superuser' AS problem FROM pg_roles WHERE rolname = current_user AND rolsuper
  UNION ALL SELECT 'can create roles' FROM pg_roles WHERE rolname = current_user AND rolcreaterole
  UNION ALL SELECT 'can create databases' FROM pg_roles WHERE rolname = current_user AND rolcreatedb
  UNION ALL SELECT 'bypasses row security' FROM pg_roles WHERE rolname = current_user AND rolbypassrls
  UNION ALL SELECT 'replication' FROM pg_roles WHERE rolname = current_user AND rolreplication
  UNION ALL SELECT 'owns ' || count(*) || ' objects in this database (can change the schema)'
    FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
   WHERE n.nspname = 'public' AND c.relowner = (SELECT oid FROM pg_roles WHERE rolname = current_user) HAVING count(*) > 0
  UNION ALL SELECT 'can connect to database ' || datname
    FROM pg_database WHERE datallowconn AND datname <> current_database() AND has_database_privilege(datname, 'CONNECT')
  UNION ALL SELECT 'can change the migration ledger'
   WHERE to_regclass('public.schema_migrations') IS NOT NULL
     AND has_table_privilege('public.schema_migrations', 'INSERT, UPDATE, DELETE')
) p ORDER BY problem`)
	if err != nil {
		return nil, fmt.Errorf("postgres: role check: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("postgres: role check: %w", err)
		}
		problems = append(problems, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: role check: %w", err)
	}
	return problems, nil
}

// CheckRuntimeRole returns the role's problems; in production any problem
// is an error, so a service never runs there as the superuser or as the
// schema owner.
func CheckRuntimeRole(ctx context.Context, pool *pgxpool.Pool, production bool) ([]string, error) {
	problems, err := RuntimeRoleProblems(ctx, pool)
	if err != nil {
		return nil, err
	}
	if production && len(problems) > 0 {
		return problems, fmt.Errorf("postgres: production refuses a runtime database role that %s", strings.Join(problems, ", "))
	}
	return problems, nil
}
