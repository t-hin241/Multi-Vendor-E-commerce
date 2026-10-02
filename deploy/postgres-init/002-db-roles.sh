#!/bin/sh
# New volume only (docker-entrypoint-initdb.d, after 001 created the
# databases): creates the migrator and the per-service runtime roles from
# the passwords Compose gives this container. Existing cluster:
# deploy/db-roles.sh runs the same db-roles.psql.
set -eu

script=""
for name in MIGRATOR IDENTITY VENDOR CATALOG INVENTORY CART ORDER PAYMENT SHIPMENT ADMIN NOTIFICATION REVIEW; do
  eval "value=\${${name}_DB_PASSWORD:-}"
  case "$value" in
    "") echo "002-db-roles: ${name}_DB_PASSWORD is not set" >&2; exit 1 ;;
    *[!A-Za-z0-9_-]*) echo "002-db-roles: ${name}_DB_PASSWORD may contain only letters, digits, _ and -" >&2; exit 1 ;;
  esac
  lower=$(echo "$name" | tr '[:upper:]' '[:lower:]')
  script="${script}\\set ${lower}_password '${value}'
"
done
script="${script}\\i /docker-entrypoint-initdb.d/db-roles.psql
"

# Passwords reach psql on stdin, never as command-line arguments.
printf '%s' "$script" | psql -X -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres
