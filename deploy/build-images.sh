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

BASE_IMAGES=(golang:1.25-alpine alpine:3.20 node:24-alpine)
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

for service in "${SERVICES[@]}"; do
  echo "--- build $service ---"
  retry "build $service" docker compose build "$service"
done

echo "All images built: ${SERVICES[*]}"
