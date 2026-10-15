#!/usr/bin/env bash
# AF-10 rollout rehearsal (PW-049, deploy/variant-pricing-runbook.md) on a
# disposable copy: its own Compose project, generated throwaway secrets and
# source trees exported from git, so the development stack, its data, its
# .env and the working tree are never used or changed.
#
#   bash deploy/rehearsal/variant-pricing.sh copy-db shopee-loadtest_postgres-data  # once: copy a STOPPED cluster
#   bash deploy/rehearsal/variant-pricing.sh base-up          # build + migrate + start BASE_REF
#   bash deploy/rehearsal/variant-pricing.sh e2e legacy       # pilot shop + SKU orders/carts on the old release
#   bash deploy/rehearsal/variant-pricing.sh backup           # dumps + restore check + versions/digests
#   bash deploy/rehearsal/variant-pricing.sh migrate-target   # Catalog -> Cart -> Order, flag off
#   bash deploy/rehearsal/variant-pricing.sh deploy-target    # Catalog -> Cart -> Order -> frontend, flag off
#   bash deploy/rehearsal/variant-pricing.sh preflight        # read-only checks (deploy/variant-pricing-preflight.sql)
#   bash deploy/rehearsal/variant-pricing.sh e2e flag-off     # smoke with the flag off
#   bash deploy/rehearsal/variant-pricing.sh flag on|off      # recreate Catalog with the capability
#   bash deploy/rehearsal/variant-pricing.sh e2e pilot        # two SKU prices to capture, delivery, return, refund
#   bash deploy/rehearsal/variant-pricing.sh ui <slug> <min> <max>  # storefront range/SKU price, screenshots
#   bash deploy/rehearsal/variant-pricing.sh e2e outage       # broker outage drill (stops/starts NATS itself)
#   bash deploy/rehearsal/variant-pricing.sh e2e rollback     # after 'flag off': fail closed, history kept
#                                                             # (uses 'down-guard' on scratch restores)
#   bash deploy/rehearsal/variant-pricing.sh down             # remove everything, volumes included
#
# REHEARSAL_STATE_DIR (default $TMPDIR/shopee-pw049) holds the env file,
# exported sources, dumps and records, outside the repository. BASE_REF and
# TARGET_REF pick the releases (default: before AF-10, AF-10 tip).
set -euo pipefail

REPO=$(cd "$(dirname "$0")/../.." && pwd)
PROJECT=${REHEARSAL_PROJECT:-shopee-pw049}
STATE=${REHEARSAL_STATE_DIR:-${TMPDIR:-/tmp}/shopee-pw049}
ENV_FILE="$STATE/rehearsal.env"
SRC="$STATE/src"
BASE_REF=${BASE_REF:-ea55614}
TARGET_REF=${TARGET_REF:-e47cb3c}
GATEWAY_PORT=${REHEARSAL_GATEWAY_PORT:-18080}
FRONTEND_PORT=${REHEARSAL_FRONTEND_PORT:-13000}
SERVICES=(identity vendor catalog inventory cart order payment shipment admin notification review)
# AF-10 owns exactly these; everything else stays on the base release.
ROLLOUT=(catalog cart order)
declare -A EXPECTED_BASE=([catalog]=16 [cart]=4 [order]=29)
declare -A EXPECTED_TARGET=([catalog]=17 [cart]=5 [order]=30)
MIGRATE_IMAGE=migrate/migrate:v4.19.1@sha256:cc4ad8e19d66791e3689405d9a028ce6e9614f32032db14acda1469f7201d6e4
ALPINE_IMAGE=alpine:3.24.2@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6

# Git Bash on Windows: paths Docker understands, unchanged container paths.
host_path() { cygpath -m "$1" 2> /dev/null || echo "$1"; }
log() { echo "rehearsal: $(date -u +%FT%TZ) $*"; }
die() { echo "rehearsal: $*" >&2; exit 1; }

compose_in() {
  local tree=$1
  shift
  docker compose -p "$PROJECT" --project-directory "$SRC/$tree" -f "$SRC/$tree/docker-compose.yml" \
    -f "$REPO/deploy/rehearsal/compose.rehearsal.yml" --env-file "$ENV_FILE" "$@"
}
compose() { compose_in "$(current_tree)" "$@"; }
current_tree() { if [ -f "$STATE/records/target-deployed" ]; then echo target; else echo base; fi; }
env_value() { grep -E "^$1=" "$ENV_FILE" | tail -1 | cut -d= -f2-; }
set_env() {
  local tmp="$ENV_FILE.tmp"
  grep -vE "^$1=" "$ENV_FILE" > "$tmp" || true
  echo "$1=$2" >> "$tmp"
  mv "$tmp" "$ENV_FILE"
}
psql_db() { compose exec -T postgres psql -X -q -v ON_ERROR_STOP=1 -U "$(env_value POSTGRES_USER)" -d "$1" "${@:2}"; }

# Release sources come from git objects, never from the working tree.
export_trees() {
  for pair in "base:$BASE_REF" "target:$TARGET_REF"; do
    local tree=${pair%%:*} ref=${pair#*:}
    [ -f "$SRC/$tree/.ref" ] && [ "$(cat "$SRC/$tree/.ref")" = "$(git -C "$REPO" rev-parse "$ref")" ] && continue
    rm -rf "${SRC:?}/$tree" && mkdir -p "$SRC/$tree"
    git -C "$REPO" archive "$ref" | tar -x -C "$SRC/$tree"
    git -C "$REPO" rev-parse "$ref" > "$SRC/$tree/.ref"
    log "exported $tree=$(cat "$SRC/$tree/.ref")"
  done
}

# Throwaway secrets: the target .env.example (a superset) with every
# CHANGE_ME replaced; the capability starts off.
make_env() {
  [ -f "$ENV_FILE" ] && return 0
  mkdir -p "$STATE/records" && chmod 700 "$STATE"
  local keys jwt
  keys=$(bash "$REPO/deploy/gen-service-keys.sh")
  jwt=$(bash "$REPO/deploy/gen-jwt-keys.sh" rehearsal)
  {
    while IFS= read -r line; do
      line=${line%$'\r'}
      case "$line" in
        "#"* | "") continue ;;
        *_INTERNAL_KEY=* | INTERNAL_SERVICE_KEYS=* | JWT_SIGNING_KEY*=* | JWT_PUBLIC_KEYS=*) continue ;;
        FEATURE_VARIANT_PRICING_ENABLED=*) continue ;;
        *ENCRYPTION_KEY=CHANGE_ME) echo "${line%%=*}=$(openssl rand -base64 32)" ;;
        *=CHANGE_ME) echo "${line%%=*}=rh$(openssl rand -hex 24)" ;;
        *) echo "$line" ;;
      esac
    done < "$SRC/target/.env.example"
    echo "$keys"
    echo "$jwt"
    echo "ENV=development"
    echo "PAYMENT_PROVIDER=mock"
    echo "ALLOWED_ORIGINS=http://localhost:$FRONTEND_PORT"
    echo "NEXT_PUBLIC_API_BASE_URL=http://localhost:$GATEWAY_PORT"
    echo "REHEARSAL_GATEWAY_PORT=$GATEWAY_PORT"
    echo "REHEARSAL_FRONTEND_PORT=$FRONTEND_PORT"
    echo "FEATURE_VARIANT_PRICING_ENABLED=false"
  } > "$ENV_FILE"
  chmod 600 "$ENV_FILE"
  log "secrets generated in $ENV_FILE"
}

# Copies a stopped cluster's data directory into this project's volume
# (read-only on the source), then gives every role a fresh password.
copy_db() {
  local src=${1:?usage: copy-db <source postgres volume>}
  local dst="${PROJECT}_postgres-data"
  [ -z "$(docker ps -q --filter "volume=$src")" ] || die "$src is in use by a running container; stop that stack first"
  ! docker volume inspect "$dst" > /dev/null 2>&1 || die "$dst exists; run 'down' first to start from a new copy"
  export_trees
  make_env
  docker volume create --label com.docker.compose.project="$PROJECT" --label com.docker.compose.volume=postgres-data "$dst" > /dev/null
  docker run --rm -v "$src":/from:ro -v "$dst":/to "$ALPINE_IMAGE" sh -c 'cp -a /from/. /to/'
  compose_in base up -d --wait postgres
  local su=${REHEARSAL_SOURCE_SUPERUSER:-shopee}
  {
    for name in MIGRATOR IDENTITY VENDOR CATALOG INVENTORY CART ORDER PAYMENT SHIPMENT ADMIN NOTIFICATION REVIEW MONITOR; do
      lower=$(echo "$name" | tr '[:upper:]' '[:lower:]')
      echo "\\set ${lower}_password '$(env_value "${name}_DB_PASSWORD")'"
    done
    echo "\\i /docker-entrypoint-initdb.d/db-roles.psql"
    echo "\\i /docker-entrypoint-initdb.d/monitor-role.psql"
    echo "ALTER ROLE \"$su\" WITH PASSWORD '$(env_value POSTGRES_PASSWORD)';"
  } | compose_in base exec -T postgres psql -X -q -v ON_ERROR_STOP=1 -U "$su" -d postgres
  [ "$su" = "$(env_value POSTGRES_USER)" ] || set_env POSTGRES_USER "$su"
  log "copied $src; roles reset with the generated passwords"
  versions | tee "$STATE/records/versions-copied.txt"
}

migrate_from() {
  local tree=$1
  shift
  local pw root
  pw=$(env_value MIGRATOR_DB_PASSWORD)
  for svc in "$@"; do
    root=$(host_path "$SRC/$tree/backend/services/$svc/migrations")
    MSYS_NO_PATHCONV=1 docker run --rm --network "${PROJECT}_shopee" \
      --mount "type=bind,source=$root,target=/migrations,readonly" "$MIGRATE_IMAGE" \
      -path=/migrations -database="postgres://shopee_migrator:${pw}@postgres:5432/${svc}_db?sslmode=disable" up 2>&1 |
      sed "s/^/  $svc: /"
  done
}

# version|dirty per service database, from the migration ledger.
versions() {
  for svc in "${SERVICES[@]}"; do
    echo "$svc $(psql_db "${svc}_db" -Atc 'SELECT version, dirty FROM schema_migrations' | tr '\n' ' ')"
  done
}
require_versions() {
  local -n want=$1
  for svc in "${!want[@]}"; do
    local got
    got=$(psql_db "${svc}_db" -Atc 'SELECT version || CASE WHEN dirty THEN '"'"' dirty'"'"' ELSE '"''"' END FROM schema_migrations')
    [ "$got" = "${want[$svc]}" ] || die "${svc}_db is at '$got', expected ${want[$svc]} (clean); stop"
  done
}

build_sequential() {
  local tree=$1
  shift
  # One service at a time, as deploy/build-images.sh: a laptop cannot
  # compile every service at once.
  for svc in "$@"; do compose_in "$tree" build "$svc"; done
}

digests() {
  for svc in "$@"; do
    echo "$svc $(docker image inspect --format '{{.Id}}' "${PROJECT}-$svc" 2> /dev/null || echo missing)"
  done
}

wait_healthy() {
  local svc=$1 i
  for i in $(seq 1 60); do
    [ "$(docker inspect --format '{{.State.Health.Status}}' "$(compose ps -q "$svc")" 2> /dev/null)" = healthy ] && return 0
    sleep 2
  done
  die "$svc did not become healthy"
}

backup() {
  local dir
  dir="$STATE/backup/$(date -u +%Y%m%dT%H%M%SZ)"
  mkdir -p "$dir"
  for svc in "${ROLLOUT[@]}"; do
    compose exec -T postgres pg_dump -U "$(env_value POSTGRES_USER)" -Fc "${svc}_db" > "$dir/${svc}_db.dump"
    log "dumped ${svc}_db ($(wc -c < "$dir/${svc}_db.dump") bytes)"
  done
  # A backup counts once it restores: load each dump into a scratch
  # database and compare row counts of the tables AF-10 touches.
  for svc in "${ROLLOUT[@]}"; do
    local scratch="${svc}_restore_check" tables
    case $svc in
      catalog) tables="products product_variants product_audit_logs" ;;
      cart) tables="carts cart_items" ;;
      order) tables="orders vendor_orders order_items order_refunds return_requests" ;;
    esac
    psql_db postgres -c "DROP DATABASE IF EXISTS $scratch" -c "CREATE DATABASE $scratch" > /dev/null
    compose exec -T postgres pg_restore -U "$(env_value POSTGRES_USER)" --no-owner --no-acl -d "$scratch" < "$dir/${svc}_db.dump"
    for t in $tables; do
      local a b
      a=$(psql_db "${svc}_db" -Atc "SELECT count(*) FROM $t")
      b=$(psql_db "$scratch" -Atc "SELECT count(*) FROM $t")
      [ "$a" = "$b" ] || die "restore check: $svc.$t live=$a restored=$b"
      echo "restore-check $svc.$t $a=$b"
    done
    psql_db postgres -c "DROP DATABASE $scratch" > /dev/null
  done | tee "$dir/restore-check.txt"
  versions > "$dir/versions.txt"
  digests "${SERVICES[@]}" gateway frontend > "$dir/digests.txt"
  echo "$dir" > "$STATE/records/last-backup"
  log "backup in $dir (versions.txt, digests.txt, restore-check.txt)"
}

# No NATS user may read the stream (deploy/nats/nats.conf), so the drill
# reads the JetStream file store of this throwaway volume, read-only: the
# message is stored once under its deterministic Nats-Msg-Id.
stream_message() {
  local id=${1:?usage: stream-message <event id>}
  local n
  n=$(docker run --rm -v "${PROJECT}_nats-data":/data:ro "$ALPINE_IMAGE" sh -c "grep -ra -o 'Nats-Msg-Id: *$id' /data | wc -l")
  echo "stored messages with Nats-Msg-Id $id: $n"
  [ "$n" -eq 1 ]
}

# Down migrations must refuse once SKU prices or variant order snapshots
# exist. Tried on scratch restores of the live databases, never on them.
down_guard() {
  local su pw ok=0
  su=$(env_value POSTGRES_USER)
  pw=$(env_value POSTGRES_PASSWORD)
  for svc in catalog order; do
    local scratch="${svc}_down_guard" root out
    psql_db postgres -c "DROP DATABASE IF EXISTS $scratch" -c "CREATE DATABASE $scratch" > /dev/null
    compose exec -T postgres sh -c "pg_dump -U '$su' -Fc ${svc}_db | pg_restore -U '$su' --no-owner --no-acl -d $scratch"
    root=$(host_path "$SRC/target/backend/services/$svc/migrations")
    out=$(MSYS_NO_PATHCONV=1 docker run --rm --network "${PROJECT}_shopee" \
      --mount "type=bind,source=$root,target=/migrations,readonly" "$MIGRATE_IMAGE" \
      -path=/migrations -database="postgres://$su:${pw}@postgres:5432/$scratch?sslmode=disable" down 1 2>&1 || true)
    if echo "$out" | grep -qiE "Keep variant pricing snapshots|Variant pricing has been used"; then
      echo "$svc down 1 refused: $(echo "$out" | grep -oiE "(Keep variant pricing snapshots[^\"]*|Variant pricing has been used[^\"]*)" | head -1)"
    else
      echo "$svc down 1 NOT refused: $out"
      ok=1
    fi
    psql_db postgres -c "DROP DATABASE $scratch" > /dev/null
  done
  return $ok
}

run_e2e() {
  local phase=${1:?usage: e2e <legacy|flag-off|pilot|outage|rollback>}
  REHEARSAL_BASE_URL="http://127.0.0.1:$GATEWAY_PORT" REHEARSAL_ORIGIN="http://localhost:$FRONTEND_PORT" \
    REHEARSAL_STATE_FILE="$(host_path "$STATE/e2e-state.json")" REHEARSAL_SCRIPT="$(host_path "$0")" \
    node "$(host_path "$REPO/deploy/rehearsal/variant-pricing-e2e.mjs")" "$phase" "${@:2}"
}

case "${1:-}" in
  copy-db) shift; copy_db "$@" ;;
  base-up)
    export_trees
    make_env
    docker volume inspect "${PROJECT}_postgres-data" > /dev/null 2>&1 || die "no database yet; run 'copy-db <volume>' first"
    build_sequential base "${SERVICES[@]}" gateway frontend
    compose_in base up -d --wait postgres redis redis-queue nats minio
    migrate_from base "${SERVICES[@]}"
    require_versions EXPECTED_BASE
    compose_in base up -d "${SERVICES[@]}" gateway frontend
    for svc in "${SERVICES[@]}"; do wait_healthy "$svc"; done
    versions | tee "$STATE/records/versions-base.txt"
    digests "${SERVICES[@]}" gateway frontend | tee "$STATE/records/digests-base.txt"
    log "base $(cat "$SRC/base/.ref") up: gateway http://127.0.0.1:$GATEWAY_PORT, storefront http://localhost:$FRONTEND_PORT"
    ;;
  backup) backup ;;
  migrate-target)
    [ -f "$STATE/records/last-backup" ] || die "no backup recorded; run 'backup' first"
    require_versions EXPECTED_BASE
    [ "$(env_value FEATURE_VARIANT_PRICING_ENABLED)" = false ] || die "the capability must stay off while migrating"
    # Maintenance window: no checkout while schemas move.
    compose stop gateway
    for svc in "${ROLLOUT[@]}"; do migrate_from target "$svc"; done
    require_versions EXPECTED_TARGET
    versions | tee "$STATE/records/versions-target.txt"
    # The old binaries keep serving on the additive schema until deploy.
    compose_in base up -d --no-deps gateway
    ;;
  deploy-target)
    require_versions EXPECTED_TARGET
    [ "$(env_value FEATURE_VARIANT_PRICING_ENABLED)" = false ] || die "deploy with the capability off"
    build_sequential target "${ROLLOUT[@]}" frontend
    touch "$STATE/records/target-deployed"
    for svc in "${ROLLOUT[@]}"; do
      compose_in target up -d --no-deps "$svc"
      wait_healthy "$svc"
      log "deployed $svc"
    done
    compose_in target up -d --no-deps frontend
    digests "${SERVICES[@]}" gateway frontend | tee "$STATE/records/digests-target.txt"
    ;;
  preflight)
    psql_db catalog_db < "$REPO/deploy/variant-pricing-preflight.sql"
    ;;
  flag)
    value=${2:?usage: flag on|off}
    case $value in on) set_env FEATURE_VARIANT_PRICING_ENABLED true ;; off) set_env FEATURE_VARIANT_PRICING_ENABLED false ;; *) die "flag on|off" ;; esac
    compose up -d --no-deps catalog
    wait_healthy catalog
    log "catalog FEATURE_VARIANT_PRICING_ENABLED=$(env_value FEATURE_VARIANT_PRICING_ENABLED)"
    ;;
  nats) compose "${2:?usage: nats stop|start}" nats ;;
  stream-message) stream_message "${2:-}" ;;
  down-guard) down_guard ;;
  sql) psql_db "${2:?usage: sql <db> [psql args]}" "${@:3}" ;;
  versions) versions ;;
  e2e) shift; run_e2e "$@" ;;
  ui)
    mkdir -p "$STATE/screens"
    node "$(host_path "$REPO/deploy/rehearsal/variant-pricing-ui.mjs")" "http://localhost:$FRONTEND_PORT" \
      "${2:?usage: ui <slug> <min> <max>}" "${3:?}" "${4:?}" "$(host_path "$STATE/screens")"
    ;;
  compose) shift; compose "$@" ;;
  down)
    compose down -v --remove-orphans
    # The next run starts on the base release with new synthetic accounts;
    # dumps and records stay for the release record.
    rm -f "$STATE/records/target-deployed" "$STATE/e2e-state.json"
    ;;
  *) sed -n '2,25p' "$0"; exit 2 ;;
esac
