#!/usr/bin/env bash
# Generates an Ed25519 key pair for access tokens and prints .env lines:
# paste them into the deployment's .env (never commit them).
#
#   JWT_SIGNING_KEY_ID, JWT_SIGNING_KEY  Identity only: the private key, the
#                                        one thing that can issue a token.
#   JWT_PUBLIC_KEYS                      every service that checks auth.
#
# Rotation (no one is signed out):
#   1. bash deploy/gen-jwt-keys.sh <new-id>; append the new public entry to
#      JWT_PUBLIC_KEYS ("old-id:...,new-id:..."), docker compose up -d, so
#      every service accepts both keys;
#   2. set Identity's JWT_SIGNING_KEY_ID/JWT_SIGNING_KEY to the new pair and
#      restart Identity;
#   3. 15 minutes later (the access token lifetime) drop the old entry.
# See deploy/identity-runbook.md.
set -euo pipefail

id="${1:-$(date -u +%Y%m%d)}"
if [[ ! "$id" =~ ^[A-Za-z0-9._-]{1,32}$ ]]; then
  echo "gen-jwt-keys: key id may contain only letters, digits, . _ - (at most 32)" >&2
  exit 2
fi

tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT
openssl genpkey -algorithm ED25519 -outform DER -out "$tmp" 2> /dev/null
# PKCS#8 DER ends with the 32-byte seed; SPKI DER ends with the 32-byte public key.
seed="$(tail -c 32 "$tmp" | base64 | tr -d '\n')"
public="$(openssl pkey -inform DER -in "$tmp" -pubout -outform DER | tail -c 32 | base64 | tr -d '\n')"

echo "JWT_SIGNING_KEY_ID=${id}"
echo "JWT_SIGNING_KEY=${seed}"
echo "JWT_PUBLIC_KEYS=${id}:${public}"
