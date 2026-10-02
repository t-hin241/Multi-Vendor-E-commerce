#!/usr/bin/env bash
# Proves the database roles (deploy/postgres-init/db-roles.psql) on a
# DISPOSABLE cluster: it drops and recreates the eleven service databases
# and the roles. Runs the real migrations, first as the superuser (an
# existing deployment), then the roles script twice, then checks what the
# runtime roles can and cannot do.
#
#   DB_ROLES_TEST_DISPOSABLE=yes PGHOST=localhost PGPORT=55431 PGUSER=identity_test #     bash deploy/test-db-roles.sh
#
# PGUSER must be a superuser; PGDATABASE (default: PGUSER) must exist.
set -uo pipefail
if [ "${DB_ROLES_TEST_DISPOSABLE:-}" != yes ]; then
  echo "refusing: set DB_ROLES_TEST_DISPOSABLE=yes on a throwaway cluster (this drops databases)" >&2
  exit 2
fi
cd "$(dirname "$0")/.."
H="-h ${PGHOST:-localhost} -p ${PGPORT:-5432}"; ADMIN_DB="${PGDATABASE:-${PGUSER}}"
SU="psql -X -q -v ON_ERROR_STOP=1 $H -U ${PGUSER}"
fail=0; ok(){ echo "  PASS $1"; }; bad(){ echo "  FAIL $1"; fail=1; }
# clean slate
for d in identity vendor catalog inventory cart order payment shipment admin notification review; do $SU -d $ADMIN_DB -c "DROP DATABASE IF EXISTS ${d}_db" 2>/dev/null; done
for r in shopee_migrator identity_app vendor_app catalog_app inventory_app cart_app order_app payment_app shipment_app admin_app notification_app review_app; do $SU -d $ADMIN_DB -c "DROP ROLE IF EXISTS $r" 2>/dev/null; done
$SU -d $ADMIN_DB -c "REVOKE ALL ON DATABASE postgres FROM PUBLIC" >/dev/null; $SU -d $ADMIN_DB -c "GRANT CONNECT, TEMPORARY ON DATABASE postgres TO PUBLIC" >/dev/null
# an existing deployment: catalog and order migrated by the superuser
for d in catalog order; do $SU -d $ADMIN_DB -c "CREATE DATABASE ${d}_db"; for f in $(ls backend/services/$d/migrations/*.up.sql | sort); do $SU -d ${d}_db -f $f >/dev/null || bad "superuser migration $f"; done; done
$SU -d catalog_db -c "CREATE TABLE schema_migrations (version bigint NOT NULL PRIMARY KEY, dirty boolean NOT NULL); INSERT INTO schema_migrations VALUES (14,false)"
vars=""; for v in migrator identity vendor catalog inventory cart order payment shipment admin notification review; do vars="$vars -v ${v}_password=pw${v}0123456789abcdef"; done
run_roles(){ $SU $vars -d postgres -f deploy/postgres-init/db-roles.psql; }
echo "### first run"; run_roles && ok "roles script ran" || bad "roles script"
echo "### second run (idempotent)"; run_roles && ok "rerun" || bad "rerun"
APP(){ PGPASSWORD=pw${1}0123456789abcdef psql -X -q -tA $H -U ${1}_app -d $2 -c "$3" 2>&1; }
MIG(){ PGPASSWORD=pwmigrator0123456789abcdef psql -X -q -tA -v ON_ERROR_STOP=1 $H -U shopee_migrator -d $1 "${@:2}" 2>&1; }
echo "### checks"
o=$($SU -tA -d catalog_db -c "select count(*) from pg_class c join pg_namespace n on n.oid=c.relnamespace where n.nspname='public' and c.relkind in ('r','v','S') and pg_get_userbyid(c.relowner)<>'shopee_migrator'"); [ "$o" = 0 ] && ok "every catalog object owned by the migrator" || bad "objects not owned by migrator: $o"
r=$(APP catalog catalog_db "insert into categories(name, slug) values ('Roles test','roles-test') returning 1"); [ "$r" = 1 ] && ok "catalog_app writes its rows" || bad "catalog_app insert: $r"
r=$(APP catalog catalog_db "select count(*) from categories"); [ "$r" -ge 1 ] 2>/dev/null && ok "catalog_app reads" || bad "catalog_app select: $r"
r=$(APP catalog catalog_db "create table hack(x int)"); echo "$r" | grep -q "permission denied" && ok "catalog_app cannot create tables" || bad "create table: $r"
r=$(APP catalog catalog_db "alter table categories add column hack int"); echo "$r" | grep -q "must be owner" && ok "catalog_app cannot alter tables" || bad "alter: $r"
r=$(APP catalog catalog_db "drop table categories"); echo "$r" | grep -q "must be owner" && ok "catalog_app cannot drop tables" || bad "drop: $r"
r=$(APP catalog catalog_db "truncate categories"); echo "$r" | grep -q "permission denied" && ok "catalog_app cannot truncate" || bad "truncate: $r"
r=$(APP catalog catalog_db "update schema_migrations set dirty=true"); echo "$r" | grep -q "permission denied" && ok "catalog_app cannot touch the migration ledger" || bad "ledger: $r"
r=$(APP catalog order_db "select 1"); echo "$r" | grep -qi "permission denied for database\|not have CONNECT" && ok "catalog_app cannot connect to order_db" || bad "cross-db: $r"
r=$(APP catalog postgres "select 1"); echo "$r" | grep -qi "permission denied for database\|not have CONNECT" && ok "catalog_app cannot connect to postgres" || bad "postgres db: $r"
r=$(APP catalog "$ADMIN_DB" "select 1"); echo "$r" | grep -qi "permission denied for database\|not have CONNECT" && ok "catalog_app cannot connect to the superuser's own database" || bad "admin db: $r"
r=$(APP catalog catalog_db "select rolsuper::text||rolcreatedb::text||rolcreaterole::text from pg_roles where rolname=current_user"); [ "$r" = falsefalsefalse ] && ok "catalog_app has no special attributes" || bad "attrs: $r"
r=$(APP order order_db "select count(*) from orders"); [ "$r" = 0 ] && ok "order_app reads its own db" || bad "order_app: $r"
echo "### new migration by the migrator is reachable by the app"
MIG catalog_db -c "create table roles_probe(id bigserial primary key, v text)" >/dev/null && ok "migrator runs DDL" || bad "migrator DDL"
r=$(APP catalog catalog_db "insert into roles_probe(v) values ('x') returning id"); [ "$r" = 1 ] && ok "default privileges give the app the new table and sequence" || bad "default privs: $r"
echo "### fresh database, full migrations as the migrator"
fresh=1; for f in $(ls backend/services/review/migrations/*.up.sql | sort); do MIG review_db -f $f >/dev/null || { fresh=0; bad "migrator migration $f: $(MIG review_db -f $f | tail -1)"; }; done; [ $fresh = 1 ] && ok "review migrations (with pgcrypto) run as the migrator"
r=$(APP review review_db "select count(*) from reviews"); [ "$r" = 0 ] && ok "review_app reads tables created by the migrator" || bad "review_app: $r"
r=$(MIG review_db -c "select count(*) from schema_migrations"); [ "$r" = 0 ] && ok "migrator uses the pre-created ledger" || bad "ledger for migrator: $r"
echo "### result: $([ $fail = 0 ] && echo ALL PASS || echo FAILURES)"
exit $fail
