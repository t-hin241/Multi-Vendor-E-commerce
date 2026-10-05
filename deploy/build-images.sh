#!/usr/bin/env bash
# Builds every image of the stack one at a time.
#
# "docker compose build" (and "up --build") builds all images in parallel.
# On a slow network or a small Docker host that ends in
# "failed to solve: context deadline exceeded". This script pulls the base
# images once, then builds each service in turn and retries a failed step.
#
# Usage (from the repository root, Linux shell or Git Bash):
#   bash deploy/build-images.sh              # every service
#   bash deploy/build-images.sh order cart   # only these services
# BUILD_RETRIES (default 3) sets the attempts per pull or build.
set -euo pipefail

cd "$(dirname "$0")/.."

# Base images come from the Dockerfiles' FROM lines (pinned by digest there),
# so this list cannot drift from what the builds use.
mapfile -t BASE_IMAGES < <(awk 'toupper($1) == "FROM" { sub(/\r$/, "", $2); print $2 }' \
  backend/gateway/Dockerfile backend/services/*/Dockerfile frontend/Dockerfile | sort -u)
SERVICES=(gateway identity vendor catalog inventory cart order payment shipment admin notification review frontend)
RETRIES="${BUILD_RETRIES:-3}"

if [ "$#" -gt 0 ]; then
  SERVICES=("$@")
fi

retry() {
  local what="$1"
  shift
  local n=1
  until "$@"; do
    if [ "$n" -ge "$RETRIES" ]; then
      echo "FAILED: $what after $n attempts" >&2
      return 1
    fi
    n=$((n + 1))
    echo "retrying $what (attempt $n of $RETRIES)" >&2
    sleep 5
  done
}

for image in "${BASE_IMAGES[@]}"; do
  echo "--- pull $image ---"
  retry "pull $image" docker pull -q "$image"
done

# Compose interpolates the whole file even to build, including runtime-only
# secrets (database role passwords, keys) that no image contains. Read
# .env.example first and .env over it: real values win (the frontend's
# NEXT_PUBLIC_API_BASE_URL build argument comes from .env), and a runtime
# secret .env does not set yet only gets its placeholder for the build.
ENV_FILES=(--env-file .env.example)
if [ -f .env ]; then
  ENV_FILES+=(--env-file .env)
fi

for service in "${SERVICES[@]}"; do
  echo "--- build $service ---"
  retry "build $service" docker compose "${ENV_FILES[@]}" build "$service"
done

echo "All images built: ${SERVICES[*]}"
