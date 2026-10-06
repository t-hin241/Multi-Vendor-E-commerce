#!/usr/bin/env bash
# Generates the Redis ACL passwords (deploy/redis/acl.sh) and prints .env
# lines: paste them into the deployment's .env (never commit them). Hex
# only, since they go into redis:// URLs.
set -euo pipefail

for name in REDIS_MONITOR IDENTITY_REDIS PAYMENT_REDIS REVIEW_REDIS NOTIFICATION_REDIS; do
  echo "${name}_PASSWORD=$(openssl rand -hex 24)"
done
