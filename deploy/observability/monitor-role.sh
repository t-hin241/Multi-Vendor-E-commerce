#!/usr/bin/env bash
# Creates or refreshes the read-only monitoring role postgres-exporter uses
# (deploy/postgres-init/monitor-role.psql). Run once when enabling the
# observability stack, and after a restore or a password change.
#
#   bash deploy/observability/monitor-role.sh
#
# MONITOR_DB_PASSWORD comes from the environment, else from .env; it reaches
# psql on stdin only.
set -euo pipefail

cd "$(dirname "$0")/../.."

from_env_file() {
  [ -f .env ] || return 0
  grep -E "^$1=" .env | tail -1 | cut -d= -f2- || true
}

value="${MONITOR_DB_PASSWORD:-$(from_env_file MONITOR_DB_PASSWORD)}"
case "$value" in
  "" | CHANGE_ME) echo "monitor-role: set MONITOR_DB_PASSWORD (environment or .env)" >&2; exit 1 ;;
  *[!A-Za-z0-9_-]*) echo "monitor-role: MONITOR_DB_PASSWORD may contain only letters, digits, _ and -" >&2; exit 1 ;;
esac

user="${POSTGRES_USER:-$(from_env_file POSTGRES_USER)}"
printf '\\set monitor_password %s\n\\i /docker-entrypoint-initdb.d/monitor-role.psql\n' "'$value'" |
  docker compose exec -T postgres psql -X -v ON_ERROR_STOP=1 -U "${user:-shopee}" -d postgres
