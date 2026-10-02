package postgres

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// POSTGRES_ROLE_TEST_ADMIN_URL: a superuser on a disposable cluster (the
// test creates and drops its own databases and roles).
func adminPool(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	url := os.Getenv("POSTGRES_ROLE_TEST_ADMIN_URL")
	if url == "" {
		t.Skip("POSTGRES_ROLE_TEST_ADMIN_URL is not configured")
	}
	pool, err := NewPool(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool, url
}

func exec(t *testing.T, pool *pgxpool.Pool, sql string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

func urlFor(t *testing.T, admin, user, password, db string) string {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(admin)
	if err != nil {
		t.Fatal(err)
	}
	c := cfg.ConnConfig
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable", user, password, c.Host, c.Port, db)
}

func TestRuntimeRoleProblems(t *testing.T) {
	admin, adminURL := adminPool(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	own, other, role, password := "rolecheck_own_"+suffix, "rolecheck_other_"+suffix, "rolecheck_app_"+suffix, "pw"+suffix
	exec(t, admin, "CREATE DATABASE "+own)
	exec(t, admin, "CREATE DATABASE "+other)
	exec(t, admin, "CREATE ROLE "+role+" LOGIN PASSWORD '"+password+"'")
	t.Cleanup(func() {
		for _, sql := range []string{"DROP DATABASE IF EXISTS " + own + " WITH (FORCE)", "DROP DATABASE IF EXISTS " + other + " WITH (FORCE)", "DROP ROLE IF EXISTS " + role} {
			_, _ = admin.Exec(context.Background(), sql)
		}
	})
	// Every database of the cluster that PUBLIC may connect to is closed for
	// the test and reopened afterwards.
	rows, err := admin.Query(t.Context(), `SELECT datname FROM pg_database
		WHERE datallowconn AND has_database_privilege('public', datname, 'CONNECT')`)
	if err != nil {
		t.Fatal(err)
	}
	var open []string
	for rows.Next() {
		var db string
		if err := rows.Scan(&db); err != nil {
			t.Fatal(err)
		}
		open = append(open, db)
	}
	rows.Close()
	for _, db := range open {
		exec(t, admin, `REVOKE CONNECT, TEMPORARY ON DATABASE "`+db+`" FROM PUBLIC`)
	}
	t.Cleanup(func() {
		for _, db := range open {
			if db != own && db != other {
				_, _ = admin.Exec(context.Background(), `GRANT CONNECT, TEMPORARY ON DATABASE "`+db+`" TO PUBLIC`)
			}
		}
	})
	exec(t, admin, "GRANT CONNECT ON DATABASE "+own+" TO "+role)

	ownAdmin, err := NewPool(t.Context(), urlFor(t, adminURL, mustUser(t, adminURL), mustPassword(t, adminURL), own))
	if err != nil {
		t.Fatal(err)
	}
	defer ownAdmin.Close()
	exec(t, ownAdmin, "CREATE TABLE items (id bigserial PRIMARY KEY, v text)")
	exec(t, ownAdmin, "CREATE TABLE schema_migrations (version bigint PRIMARY KEY, dirty boolean NOT NULL)")
	exec(t, ownAdmin, "GRANT USAGE ON SCHEMA public TO "+role)
	exec(t, ownAdmin, "GRANT SELECT, INSERT, UPDATE, DELETE ON items TO "+role)

	// The superuser is refused in production.
	superProblems, err := CheckRuntimeRole(t.Context(), ownAdmin, true)
	if err == nil || !contains(superProblems, "superuser") {
		t.Fatalf("superuser: problems %v, err %v", superProblems, err)
	}

	app, err := NewPool(t.Context(), urlFor(t, adminURL, role, password, own))
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if problems, err := CheckRuntimeRole(t.Context(), app, true); err != nil || len(problems) != 0 {
		t.Fatalf("least privilege: problems %v, err %v", problems, err)
	}

	// Each excess privilege is reported.
	exec(t, admin, "GRANT CONNECT ON DATABASE "+other+" TO "+role)
	exec(t, ownAdmin, "GRANT UPDATE ON schema_migrations TO "+role)
	exec(t, ownAdmin, "CREATE TABLE owned_by_app (x int)")
	exec(t, ownAdmin, "ALTER TABLE owned_by_app OWNER TO "+role)
	problems, err := CheckRuntimeRole(t.Context(), app, true)
	if err == nil {
		t.Fatal("production must refuse the widened role")
	}
	for _, want := range []string{"can connect to database " + other, "can change the migration ledger", "owns 1 objects in this database (can change the schema)"} {
		if !contains(problems, want) {
			t.Errorf("missing problem %q in %v", want, problems)
		}
	}
	if _, err := CheckRuntimeRole(t.Context(), app, false); err != nil {
		t.Fatalf("development only warns: %v", err)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func mustUser(t *testing.T, url string) string {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	return cfg.ConnConfig.User
}

func mustPassword(t *testing.T, url string) string {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	return cfg.ConnConfig.Password
}
