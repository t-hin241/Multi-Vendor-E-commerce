#!/usr/bin/env bash
# Generates the database role passwords (migrator and one runtime role per
# service). Prints .env lines to stdout: paste them into the deployment's
# .env (never commit them), then run deploy/db-roles.sh. Hexadecimal, so
# they are safe inside a connection URL.
set -euo pipefail

for name in MIGRATOR IDENTITY VENDOR CATALOG INVENTORY CART ORDER PAYMENT SHIPMENT ADMIN NOTIFICATION REVIEW; do
  echo "${name}_DB_PASSWORD=$(openssl rand -hex 24)"
done
