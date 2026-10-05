#!/usr/bin/env bash
# Load-test environment (deploy/loadtest/README.md). Its own Compose project
# with freshly generated throwaway secrets, so a development stack, its data
# and its .env are never used or changed.
#
#   bash deploy/loadtest/run.sh copy-db shopee_postgres-data   # once: copy a STOPPED dev database
#   bash deploy/loadtest/run.sh up                             # build, migrate, start
#   bash deploy/loadtest/run.sh fixture                        # shipping, weights, stock on the copy
#   bash deploy/loadtest/run.sh k6 checkout.js [k6 args]       # run a scenario
#   bash deploy/loadtest/run.sh fault inventory 60             # failure drill during a run
#   bash deploy/loadtest/run.sh down                           # remove everything, volumes included
#
# LOADTEST_STATE_DIR (default $TMPDIR/shopee-loadtest) holds the generated
# env file, outside the repository. LOADTEST_SOURCE_SUPERUSER (default
# shopee) is the source cluster's superuser name (POSTGRES_USER).
set -euo pipefail

cd "$(dirname "$0")/../.."

PROJECT=${LOADTEST_PROJECT:-shopee-loadtest}
STATE=${LOADTEST_STATE_DIR:-${TMPDIR:-/tmp}/shopee-loadtest}
ENV_FILE="$STATE/loadtest.env"
FILES=(-f docker-compose.yml -f deploy/observability/compose.observability.yml -f deploy/loadtest/compose.loadtest.yml)
# Everything but the frontend (not under test) and the edge proxy.
STACK=(postgres redis nats minio identity vendor catalog inventory cart order payment shipment admin review notification gateway
  prometheus tempo grafana cadvisor node-exporter postgres-exporter redis-exporter nats-exporter)
SERVICES=(identity vendor catalog inventory cart order payment shipment admin notification review)
MIGRATE_IMAGE=migrate/migrate:v4.19.1@sha256:cc4ad8e19d66791e3689405d9a028ce6e9614f32032db14acda1469f7201d6e4
ALPINE_IMAGE=alpine:3.24.2@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6

compose() { docker compose -p "$PROJECT" --env-file "$ENV_FILE" "${FILES[@]}" "$@"; }
env_value() { grep -E "^$1=" "$ENV_FILE" | tail -1 | cut -d= -f2-; }

# Throwaway secrets: .env.example with every CHANGE_ME replaced.
make_env() {
  [ -f "$ENV_FILE" ] && return 0
  mkdir -p "$STATE" && chmod 700 "$STATE"
  local keys
  keys=$(bash deploy/gen-service-keys.sh)
  {
    while IFS= read -r line; do
      line=${line%$'\r'}
      case "$line" in
        "#"* | "") continue ;;
        *_INTERNAL_KEY=* | INTERNAL_SERVICE_KEYS=*) continue ;;
        *ENCRYPTION_KEY=CHANGE_ME) echo "${line%%=*}=$(openssl rand -base64 32)" ;;
        *=CHANGE_ME) echo "${line%%=*}=lt$(openssl rand -hex 24)" ;;
        *) echo "$line" ;;
      esac
    done < .env.example
    echo "$keys"
    echo "ENV=development"
    echo "PAYMENT_PROVIDER=mock"
    echo "OTEL_EXPORTER_OTLP_ENDPOINT=http://tempo:4318"
    echo "OTEL_TRACES_SAMPLER_ARG=${LOADTEST_TRACE_RATIO:-0.1}"
    echo "PPROF_ENABLED=true"
  } > "$ENV_FILE"
  chmod 600 "$ENV_FILE"
  echo "loadtest: secrets generated in $ENV_FILE"
}

# Copies a stopped cluster's data directory into this project's volume
# (read-only on the source), then gives every role a fresh password.
copy_db() {
  local src=${1:?usage: copy-db <source postgres volume>}
  local dst="${PROJECT}_postgres-data"
  if [ -n "$(docker ps -q --filter "volume=$src")" ]; then
    echo "loadtest: $src is in use by a running container; stop that stack first (a live data directory cannot be copied safely)" >&2
    exit 1
  fi
  if docker volume inspect "$dst" > /dev/null 2>&1; then
    echo "loadtest: $dst exists; run 'down' first to start from a new copy" >&2
    exit 1
  fi
  make_env
  docker volume create --label com.docker.compose.project="$PROJECT" --label com.docker.compose.volume=postgres-data "$dst" > /dev/null
  docker run --rm -v "$src":/from:ro -v "$dst":/to "$ALPINE_IMAGE" sh -c 'cp -a /from/. /to/'
  compose up -d --wait postgres
  local su=${LOADTEST_SOURCE_SUPERUSER:-shopee}
  {
    for name in MIGRATOR IDENTITY VENDOR CATALOG INVENTORY CART ORDER PAYMENT SHIPMENT ADMIN NOTIFICATION REVIEW MONITOR; do
      lower=$(echo "$name" | tr '[:upper:]' '[:lower:]')
      echo "\\set ${lower}_password '$(env_value "${name}_DB_PASSWORD")'"
    done
    echo "\\i /docker-entrypoint-initdb.d/db-roles.psql"
    echo "\\i /docker-entrypoint-initdb.d/monitor-role.psql"
    echo "ALTER ROLE \"$su\" WITH PASSWORD '$(env_value POSTGRES_PASSWORD)';"
  } | compose exec -T postgres psql -X -q -v ON_ERROR_STOP=1 -U "$su" -d postgres
  echo "loadtest: copied $src, roles reset with the generated passwords"
}

psql_db() { compose exec -T postgres psql -X -q -v ON_ERROR_STOP=1 -U "$(env_value POSTGRES_USER)" -d "$1" "${@:2}"; }

# Test fixture on the copy only: every approved shop ships with the test
# carrier (development data configures one shop), so checkouts across many
# shops can be quoted. Run after 'up'; idempotent.
fixture() {
  local carrier
  carrier=$(psql_db shipment_db -Atc "SELECT id FROM carriers WHERE code = 'TEST-EXP' AND is_active")
  [ -n "$carrier" ] || { echo "loadtest: no active TEST-EXP carrier in shipment_db" >&2; exit 1; }
  psql_db vendor_db -Atc "SELECT id FROM vendors WHERE status = 'approved'" |
    awk -v c="$carrier" 'BEGIN { print "INSERT INTO vendor_shipping_methods (vendor_id, carrier_id, is_default) VALUES" }
      { printf "%s('\''%s'\'', '\''%s'\'', NOT EXISTS (SELECT 1 FROM vendor_shipping_methods WHERE vendor_id = '\''%s'\'' AND is_default))", (NR > 1 ? ",\n" : ""), $1, c, $1 }
      END { print "\nON CONFLICT DO NOTHING;" }' |
    psql_db shipment_db
  echo "loadtest: shops with a default shipping method: $(psql_db shipment_db -Atc 'SELECT count(*) FROM vendor_shipping_methods WHERE is_default AND is_active')"
  # Shipping is priced by package weight; scraped products have none.
  psql_db catalog_db -c "INSERT INTO product_packaging (product_id, weight_grams) SELECT id, 500 FROM products
    ON CONFLICT (product_id) DO UPDATE SET weight_grams = COALESCE(product_packaging.weight_grams, 500)"
  echo "loadtest: products with a package weight: $(psql_db catalog_db -Atc 'SELECT count(*) FROM product_packaging WHERE weight_grams > 0')"
  # Scraped stock is ~10 units per item: a run of thousands of orders would
  # measure sold-out carts, not the platform. Restock with a movement each,
  # as the service itself records stock changes.
  psql_db inventory_db -c "WITH bumped AS (
      UPDATE inventory_items SET available_quantity = available_quantity + 100000, updated_at = now()
      WHERE available_quantity < 100000 RETURNING id)
    INSERT INTO stock_movements (inventory_item_id, change_quantity, reason, reference_id)
    SELECT id, 100000, 'restock', 'loadtest-fixture' FROM bumped"
  echo "loadtest: items with stock under 1000: $(psql_db inventory_db -Atc 'SELECT count(*) FROM inventory_items WHERE available_quantity < 1000')"
}

migrate() {
  local network="${PROJECT}_shopee" pw root
  pw=$(env_value MIGRATOR_DB_PASSWORD)
  # Git Bash on Windows: a host path Docker understands, and no rewriting
  # of the container-side /migrations arguments.
  root=$(pwd -W 2> /dev/null || pwd)
  for svc in "${SERVICES[@]}"; do
    MSYS_NO_PATHCONV=1 docker run --rm --network "$network" \
      --mount "type=bind,source=$root/backend/services/$svc/migrations,target=/migrations,readonly" "$MIGRATE_IMAGE" \
      -path=/migrations -database="postgres://shopee_migrator:${pw}@postgres:5432/${svc}_db?sslmode=disable" up 2>&1 |
      sed "s/^/  $svc: /"
  done
}

case "${1:-}" in
  copy-db) shift; copy_db "$@" ;;
  up)
    make_env
    if ! docker volume inspect "${PROJECT}_postgres-data" > /dev/null 2>&1; then
      echo "loadtest: no database yet; run 'copy-db <volume>' first (an empty catalog measures nothing)" >&2
      exit 1
    fi
    compose build "${SERVICES[@]}" gateway
    compose up -d --wait postgres redis nats minio
    migrate
    # A developer machine's disk can be 100x slower to fsync than a VPS's
    # NVMe (pg_test_fsync: Docker Desktop ~100 ms, NVMe ~1 ms), which would
    # make every commit look like the bottleneck. On this throwaway copy,
    # commits do not wait for the disk unless LOADTEST_SYNCHRONOUS_COMMIT=on;
    # the report counts commits per request instead.
    psql_db postgres -c "ALTER SYSTEM SET synchronous_commit = '${LOADTEST_SYNCHRONOUS_COMMIT:-off}'" -c "SELECT pg_reload_conf()" > /dev/null
    compose up -d "${STACK[@]}"
    echo "loadtest: Grafana http://127.0.0.1:${LOADTEST_GRAFANA_PORT:-13001} (admin / GRAFANA_ADMIN_PASSWORD in $ENV_FILE), Prometheus http://127.0.0.1:${LOADTEST_PROMETHEUS_PORT:-19090}"
    ;;
  fixture) fixture ;;
  # Failure drill during a run: stop one service (or the broker) for a
  # while, then start it again; recovery shows in the outbox/lag panels.
  fault)
    svc=${2:?usage: fault <service> <seconds>}
    secs=${3:-60}
    echo "loadtest: $(date -u +%T) stopping $svc for ${secs}s"
    compose stop "$svc" > /dev/null
    sleep "$secs"
    compose start "$svc" > /dev/null
    echo "loadtest: $(date -u +%T) $svc started again"
    ;;
  k6) shift; mkdir -p "${LOADTEST_RESULTS_DIR:-deploy/loadtest/results}"; compose run --rm k6 run -o experimental-prometheus-rw "$@" ;;
  compose) shift; compose "$@" ;;
  down) compose --profile k6 down -v --remove-orphans ;;
  *) sed -n '2,13p' "$0"; exit 2 ;;
esac
