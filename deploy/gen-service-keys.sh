#!/usr/bin/env bash
# Generates one internal service key per backend service (PLT-01) and the
# registry of their SHA-256 hashes. Prints .env lines to stdout: paste
# them into the deployment's .env (never commit them). Re-run to rotate:
# keep the old hash next to the new one ("name=newhash|oldhash") until every
# service has restarted with its new key, then drop the old hash.
set -euo pipefail

services=(identity vendor catalog inventory cart order payment shipment admin review notification)
registry=()
for svc in "${services[@]}"; do
  key="$(openssl rand -hex 32)"
  hash="$(printf '%s' "$key" | openssl dgst -sha256 -r | cut -d' ' -f1)"
  echo "$(echo "$svc" | tr '[:lower:]' '[:upper:]')_INTERNAL_KEY=${key}"
  registry+=("${svc}=${hash}")
done
(IFS=,; echo "INTERNAL_SERVICE_KEYS=${registry[*]}")
