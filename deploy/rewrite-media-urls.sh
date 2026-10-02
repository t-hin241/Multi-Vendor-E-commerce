#!/usr/bin/env bash
# Rewrites stored image URLs (products, shop logo/banner, review photos) from
# an old public base to a new one in catalog_db, vendor_db and review_db.
# Dry run unless --apply. See deploy/rewrite-media-urls.sql and
# deploy/edge-runbook.md.
#
#   bash deploy/rewrite-media-urls.sh --old http://localhost:9000 --new https://shop.example.com/media
#   bash deploy/rewrite-media-urls.sh --old http://localhost:9000 --new https://shop.example.com/media --apply
#
# By default psql runs inside the Compose postgres container. Override:
#   PSQL_CMD="psql -h 127.0.0.1 -p 5432 -U shopee"   (another connection)
#   MEDIA_DBS="catalog_db vendor_db review_db"       (database names)
set -euo pipefail

old="" new="" apply=false
while [ $# -gt 0 ]; do
  case "$1" in
    --old) old="${2:-}"; shift 2 ;;
    --new) new="${2:-}"; shift 2 ;;
    --apply) apply=true; shift ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done
if [ -z "$old" ] || [ -z "$new" ]; then
  echo "usage: $0 --old <old public base> --new <new public base> [--apply]" >&2
  exit 2
fi

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root" # docker compose finds the project here
sql="$root/deploy/rewrite-media-urls.sql"
read -ra psql_cmd <<<"${PSQL_CMD:-docker compose exec -T postgres psql -U ${POSTGRES_USER:-shopee}}"
read -ra dbs <<<"${MEDIA_DBS:-catalog_db vendor_db review_db}"

if [ "$apply" = true ]; then
  echo "APPLY: rewriting $old -> $new (take a database backup first)"
else
  echo "DRY RUN: $old -> $new (nothing is written; add --apply)"
fi

# One transaction per database: a failure in one leaves the others as they
# are, and a rerun skips what is already rewritten.
for db in "${dbs[@]}"; do
  "${psql_cmd[@]}" -X -q -v ON_ERROR_STOP=1 -v old_base="$old" -v new_base="$new" -v apply="$apply" -d "$db" -f - <"$sql"
done
