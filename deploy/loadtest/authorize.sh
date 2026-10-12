#!/usr/bin/env bash
# PW-020: only the existing, disposable shopee-loadtest project.
set -euo pipefail
cd "$(dirname "$0")/../.."
: "${LOADTEST_STATE_DIR:?Point to the existing loadtest state directory}"
ENV_FILE="$LOADTEST_STATE_DIR/loadtest.env"
[ -f "$ENV_FILE" ] || { echo 'Missing loadtest environment' >&2; exit 1; }
FILES=(-f docker-compose.yml -f deploy/observability/compose.observability.yml -f deploy/loadtest/compose.loadtest.yml -f deploy/loadtest/compose.authorize.yml)
compose() { docker compose -p shopee-loadtest --env-file "$ENV_FILE" "${FILES[@]}" "$@"; }
# Never touch the development volume, even if someone changed an override.
check_copy() {
  local mounts
  mounts=$(docker inspect shopee-loadtest-postgres-1 --format '{{range .Mounts}}{{println .Name}}{{end}}')
  echo "$mounts" | grep -qx 'shopee-loadtest_postgres-data' || { echo 'Not the isolated loadtest database' >&2; exit 1; }
  if echo "$mounts" | grep -qx 'shopee_postgres-data'; then echo 'Development database refused' >&2; exit 1; fi
}
case "${1:-}" in
  prepare)
    check_copy
    # Ephemeral invitation key; no invitation can be created in this drill.
    export STAFF_INVITATION_FINGERPRINT_KEY
    STAFF_INVITATION_FINGERPRINT_KEY=$(openssl rand -hex 32)
    compose build authorize-probe
    compose up -d --no-deps --wait --wait-timeout 120 authorize-probe vendor
    user=$(sed -n 's/^POSTGRES_USER=//p' "$ENV_FILE" | tr -d '\r' | tail -1)
    compose exec -T postgres psql -X -q -v ON_ERROR_STOP=1 -U "${user:-shopee}" -d postgres < deploy/loadtest/authorize-fixture.sql
    ;;
  k6)
    shift
    mkdir -p deploy/loadtest/results
    compose run --rm --no-deps k6 run authorize.js "$@"
    ;;
  stop|start)
    case "${2:-}" in identity|vendor) ;; *) echo 'Only identity/vendor faults allowed' >&2; exit 1 ;; esac
    compose "$1" "$2"
    ;;
  recover)
    case "${2:-}" in identity|vendor) ;; *) echo 'Only identity/vendor recovery allowed' >&2; exit 1 ;; esac
    svc=$2
    started=$(date +%s%3N)
    compose start "$svc"
    # Catalog is not stopped by these drills. Probe uses the real client,
    # so health here means an actual grant through both dependencies.
    body='{"actor_user_id":"02000000-0000-4000-8000-000000000001","vendor_id":"02000001-0000-4000-8000-000000000001","permission":"orders.read"}'
    recovered=false
    for attempt in {1..15}; do
      response=$(compose exec -T catalog wget -qO- -T 4 --header='Content-Type: application/json' --post-data="$body" http://authorize-probe:8080/check 2>/dev/null || true)
      if [[ "$response" == *'"allowed":true'* ]]; then recovered=true; break; fi
      sleep 1
    done
    elapsed=$(( $(date +%s%3N) - started ))
    mkdir -p deploy/loadtest/results
    printf '{"service":"%s","recovered":%s,"elapsed_ms":%s,"utc":"%s"}\n' "$svc" "$recovered" "$elapsed" "$(date -u +%FT%TZ)" | tee "deploy/loadtest/results/authorize-$svc-recovery.json"
    [ "$recovered" = true ] && [ "$elapsed" -lt 5000 ]
    ;;
  status) compose ps ;;
  metrics)
    tag=${2:?metrics requires a run label}
    [[ "$tag" =~ ^[a-zA-Z0-9_-]+$ ]] || exit 2
    mkdir -p deploy/loadtest/results
    for svc in vendor identity; do
      compose exec -T "$svc" wget -qO- http://localhost:9464/metrics > "deploy/loadtest/results/authorize-$tag-$svc.prom"
    done
    docker stats --no-stream --format '{{json .}}' > "deploy/loadtest/results/authorize-$tag-containers.jsonl"
    ;;
  restore)
    # Restore normal routing and default flags; preserve all test volumes.
    docker compose -p shopee-loadtest --env-file "$ENV_FILE" -f docker-compose.yml -f deploy/observability/compose.observability.yml -f deploy/loadtest/compose.loadtest.yml up -d --no-deps vendor identity
    compose stop authorize-probe
    ;;
  *) echo 'Usage: authorize.sh prepare|k6 [k6 args]|stop identity|start identity|stop vendor|start vendor|recover identity|recover vendor|metrics label|status|restore' >&2; exit 2 ;;
esac
