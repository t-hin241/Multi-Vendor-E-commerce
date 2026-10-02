#!/usr/bin/env bash
# Creates or refreshes the database roles on a running cluster (the
# migrator and one runtime role per service, see
# deploy/postgres-init/db-roles.psql). Idempotent; run it once when
# switching an existing deployment to per-service roles, and again after a
# restore or a password change. A new volume does this by itself
# (postgres-init/002-db-roles.sh).
#
#   bash deploy/db-roles.sh
#
# Passwords come from the environment, else from .env:
# MIGRATOR_DB_PASSWORD and <SERVICE>_DB_PASSWORD (generate with
# deploy/gen-db-passwords.sh). They reach psql on stdin only.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"

from_env_file() {
  [ -f .env ] || return 0
  grep -E "^$1=" .env | tail -1 | cut -d= -f2- || true
}

script=""
for name in MIGRATOR IDENTITY VENDOR CATALOG INVENTORY CART ORDER PAYMENT SHIPMENT ADMIN NOTIFICATION REVIEW; do
  var="${name}_DB_PASSWORD"
  value="${!var:-$(from_env_file "$var")}"
  case "$value" in
    "") echo "db-roles: $var is not set (environment or .env)" >&2; exit 1 ;;
    *[!A-Za-z0-9_-]*) echo "db-roles: $var may contain only letters, digits, _ and -" >&2; exit 1 ;;
  esac
  script+="\\set ${name,,}_password '${value}'"$'\n'
done
script+='\i /docker-entrypoint-initdb.d/db-roles.psql'$'\n'

user="${POSTGRES_USER:-$(from_env_file POSTGRES_USER)}"
printf '%s' "$script" | docker compose exec -T postgres psql -X -v ON_ERROR_STOP=1 -U "${user:-shopee}" -d postgres
