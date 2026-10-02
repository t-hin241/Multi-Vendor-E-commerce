#!/usr/bin/env bash
# Lists every port the running stack publishes on the host and fails if any
# listens on a public interface, except the edge proxy's 80/443.
#
# Docker writes its own iptables rules, so a published port is reachable
# even when ufw "denies" it: this check, not the firewall, is what proves
# the database, Redis, NATS, MinIO and the services are closed.
#
#   bash deploy/check-exposure.sh            # project "shopee"
#   COMPOSE_PROJECT_NAME=other bash deploy/check-exposure.sh
set -euo pipefail

project="${COMPOSE_PROJECT_NAME:-shopee}"
allowed_service="caddy"
allowed_ports=" 80 443 "

rows=$(docker ps --filter "label=com.docker.compose.project=${project}" \
  --format '{{.Label "com.docker.compose.service"}}|{{.Ports}}')
if [ -z "$rows" ]; then
  echo "no running containers for project ${project}" >&2
  exit 2
fi

bad=0
while IFS='|' read -r service ports; do
  [ -z "$ports" ] && continue
  # "0.0.0.0:80->80/tcp, [::]:80->80/tcp, 127.0.0.1:5432->5432/tcp"
  IFS=',' read -ra bindings <<<"$ports"
  for b in "${bindings[@]}"; do
    b="${b# }"
    case "$b" in
      *"->"*) ;;
      *) continue ;; # exposed inside the network only, not published
    esac
    host="${b%%->*}"           # 0.0.0.0:80 / [::]:80 / 127.0.0.1:5432
    addr="${host%:*}"
    port="${host##*:}"
    case "$addr" in
      127.0.0.1 | "[::1]")
        printf '  local   %-14s %s\n' "$service" "$b"
        ;;
      *)
        if [ "$service" = "$allowed_service" ] && [[ "$allowed_ports" == *" $port "* ]]; then
          printf '  public  %-14s %s\n' "$service" "$b"
        else
          printf '  EXPOSED %-14s %s\n' "$service" "$b"
          bad=1
        fi
        ;;
    esac
  done
done <<<"$rows"

if [ "$bad" -ne 0 ]; then
  echo "FAIL: a port other than the edge proxy's 80/443 is published on a public interface." >&2
  echo "Set INTERNAL_BIND_ADDRESS/PUBLIC_BIND_ADDRESS to 127.0.0.1 and start with -f deploy/compose.edge.yml." >&2
  exit 1
fi
echo "OK: only the edge proxy is public."
