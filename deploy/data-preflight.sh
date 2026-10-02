#!/usr/bin/env bash
# Read-only data preflight for every service database (data tooling plan 14,
# DEV-04): duplicates, broken money totals, refunds/settlement mismatches,
# negative stock, stale holds, seed/demo data, parked work. It only reports;
# fixes go through a reviewed operation or migration, never hand-written SQL
# across services. Each database runs in a READ ONLY transaction.
#
#   bash deploy/data-preflight.sh                 # all databases
#   bash deploy/data-preflight.sh order payment   # some
#   bash deploy/data-preflight.sh --production    # seed data also fails
#   bash deploy/data-preflight.sh --report out.tsv
#
# Exit status: 1 when a "blocking" check (or, with --production, a "seed"
# check) is above zero; 2 when a check could not run.
#
# By default psql runs inside the Compose postgres container as
# POSTGRES_USER. Another connection: PSQL_CMD="psql -h <host> -U <user>";
# other database names: <SERVICE>_DB=name (e.g. ORDER_DB=order_db_restored).
set -euo pipefail

production=false report="" services=()
while [ $# -gt 0 ]; do
  case "$1" in
    --production) production=true; shift ;;
    --report) report="${2:?--report needs a file}"; shift 2 ;;
    -*) echo "unknown option: $1" >&2; exit 2 ;;
    *) services+=("$1"); shift ;;
  esac
done
[ ${#services[@]} -gt 0 ] || services=(identity vendor catalog inventory cart order payment shipment notification review)

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"
user="${POSTGRES_USER:-$( [ -f .env ] && grep -E '^POSTGRES_USER=' .env | tail -1 | cut -d= -f2- || true)}"
read -ra psql_cmd <<<"${PSQL_CMD:-docker compose exec -T postgres psql -U ${user:-shopee}}"

rows=()
failed=0
for svc in "${services[@]}"; do
  sql="deploy/data-preflight/${svc}.sql"
  [ -f "$sql" ] || { echo "no preflight for $svc" >&2; exit 2; }
  var="$(echo "$svc" | tr '[:lower:]' '[:upper:]')_DB"
  db="${!var:-${svc}_db}"
  # The checks are written for the newest schema in this repository: read
  # the target's migration ledger first instead of assuming it.
  latest=$(ls "backend/services/$svc/migrations" | sed -n 's/^\([0-9]*\)_.*\.up\.sql$/\1/p' | sort -n | tail -1 | sed 's/^0*//')
  if ! at=$(printf 'SELECT version::text || CASE WHEN dirty THEN %s ELSE %s END FROM schema_migrations;\n' "' dirty'" "''" |
      "${psql_cmd[@]}" -X -q -tA -v ON_ERROR_STOP=1 -d "$db" 2>&1); then
    echo "$db: cannot read the migration ledger: $(echo "$at" | grep -m1 ERROR || echo "$at" | tail -1)" >&2
    failed=1
    continue
  fi
  at="$(echo "$at" | tr -d '\r' | head -1)"
  if [ "${at%% *}" != "$latest" ]; then
    rows+=("${db}"$'\t'"blocking"$'\t'"schema_not_at_latest_migration"$'\t'"1"$'\t'"ledger at ${at:-empty}, repository at ${latest}; migrate first (other checks skipped)")
    continue
  fi
  if ! out=$( { printf 'BEGIN TRANSACTION READ ONLY;\nSET LOCAL statement_timeout = %s;\nSET LOCAL lock_timeout = %s;\n' "'120s'" "'5s'"; cat "$sql"; printf ';\nCOMMIT;\n'; } |
      "${psql_cmd[@]}" -X -q -tA -F $'\t' -P pager=off -v ON_ERROR_STOP=1 -d "$db" 2>&1); then
    echo "$db: check failed to run: $(echo "$out" | grep -m1 ERROR || echo "$out" | tail -1)" >&2
    failed=1
    continue
  fi
  while IFS=$'\t' read -r severity check count meaning; do
    [ -n "$severity" ] || continue
    rows+=("${db}"$'\t'"${severity}"$'\t'"${check}"$'\t'"${count}"$'\t'"${meaning}")
  done <<<"$(echo "$out" | grep -E $'^(blocking|seed|warning|info)\t')"
done

status=0
printf '%-16s %-9s %-40s %8s  %s\n' DATABASE SEVERITY CHECK ROWS MEANING
for r in "${rows[@]}"; do
  IFS=$'\t' read -r db severity check count meaning <<<"$r"
  mark=""
  if [ "$count" != 0 ]; then
    if [ "$severity" = blocking ] || { [ "$severity" = seed ] && $production; }; then
      mark=" <-- FAIL"; status=1
    fi
  fi
  printf '%-16s %-9s %-40s %8s  %s%s\n' "$db" "$severity" "$check" "$count" "$meaning" "$mark"
done
if [ -n "$report" ]; then
  { printf 'database\tseverity\tcheck\trows\tmeaning\n'; printf '%s\n' "${rows[@]}"; } >"$report"
  echo "report: $report"
fi
[ $failed = 0 ] || exit 2
exit $status
