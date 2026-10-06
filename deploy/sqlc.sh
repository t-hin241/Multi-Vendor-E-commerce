#!/usr/bin/env bash
# Runs sqlc for every service that has a sqlc.yaml (or only the ones named),
# with the image pinned in deploy/tools/compose.tools.yml.
#
#   bash deploy/sqlc.sh                       # regenerate after editing queries or migrations
#   bash deploy/sqlc.sh diff                  # fail if the committed code is stale (CI)
#   bash deploy/sqlc.sh diff services/review  # one service
set -euo pipefail
cd "$(dirname "$0")/.."

command=${1:-generate}
case "$command" in
  generate | diff) ;;
  *)
    echo "usage: bash deploy/sqlc.sh [generate|diff] [services/<name> ...]" >&2
    exit 2
    ;;
esac
shift || true

image=$(awk '$1 == "image:" && $2 ~ /^sqlc\/sqlc:/ { print $2; exit }' deploy/tools/compose.tools.yml | tr -d '\r')
root=$(pwd -W 2> /dev/null || pwd)

if [ "$#" -gt 0 ]; then
  configs=()
  for dir in "$@"; do configs+=("backend/$dir/sqlc.yaml"); done
else
  configs=(backend/services/*/sqlc.yaml)
fi

for config in "${configs[@]}"; do
  [ -f "$config" ] || { echo "sqlc: $config not found" >&2; exit 1; }
  dir=$(dirname "$config")
  echo "sqlc $command: $dir"
  # The caller's uid owns generated files on Linux; Docker Desktop ignores it.
  MSYS_NO_PATHCONV=1 docker run --rm --user "$(id -u):$(id -g)" \
    --mount "type=bind,source=$root/$dir,target=/src" -w /src "$image" "$command"
done
