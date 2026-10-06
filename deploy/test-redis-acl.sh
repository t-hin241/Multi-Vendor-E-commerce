#!/usr/bin/env bash
# Proves the Redis ACL (deploy/redis/acl.sh) on throwaway containers: no
# anonymous access, each service only its own key prefix and commands, the
# monitor user statistics only. Uses the Redis image pinned in
# docker-compose.yml and obviously fake passwords.
#
#   bash deploy/test-redis-acl.sh
set -uo pipefail
cd "$(dirname "$0")/.."

image="${REDIS_IMAGE:-$(awk '$1 == "image:" && $2 ~ /^redis:/ { print $2; exit }' docker-compose.yml | tr -d '\r')}"
root="$(pwd -W 2> /dev/null || pwd)"
fail=0
ok() { echo "  PASS $1"; }
bad() { echo "  FAIL $1"; fail=1; }

start() { # name role env...
  local name=$1 role=$2
  shift 2
  MSYS_NO_PATHCONV=1 docker run -d --rm --name "$name" -e REDIS_ROLE="$role" -e REDIS_MONITOR_PASSWORD=test-monitor-pw "$@" \
    --mount "type=bind,source=$root/deploy/redis/acl.sh,target=/etc/redis/acl.sh,readonly" "$image" \
    sh /etc/redis/acl.sh --appendonly no --save "" > /dev/null
}
cli() { # name [redis-cli args...]
  local name=$1
  shift
  MSYS_NO_PATHCONV=1 docker exec "$name" redis-cli --no-auth-warning "$@" 2>&1 | head -1
}
expect() { # description pattern output
  if [[ "$3" =~ $2 ]]; then ok "$1"; else bad "$1: $3"; fi
}

cache=shopee-acl-test-cache
queue=shopee-acl-test-queue
trap 'docker rm -f "$cache" "$queue" > /dev/null 2>&1' EXIT
start "$cache" cache -e IDENTITY_REDIS_PASSWORD=test-identity-pw -e PAYMENT_REDIS_PASSWORD=test-payment-pw -e REVIEW_REDIS_PASSWORD=test-review-pw
start "$queue" queue -e NOTIFICATION_REDIS_PASSWORD=test-notification-pw
for c in "$cache" "$queue"; do
  for _ in $(seq 1 30); do [ "$(cli "$c" --user monitor --pass test-monitor-pw ping)" = PONG ] && break; sleep 1; done
done

id=(--user identity --pass test-identity-pw)
pay=(--user payment --pass test-payment-pw)
note=(--user notification --pass test-notification-pw)
mon=(--user monitor --pass test-monitor-pw)
script="local n=redis.call('INCR',KEYS[1]); if n==1 then redis.call('EXPIRE',KEYS[1],60) end; return n"

echo "### cache instance"
expect "no anonymous access" "NOAUTH" "$(cli "$cache" ping)"
expect "a wrong password is refused" "WRONGPASS" "$(cli "$cache" --user identity --pass nope ping)"
expect "identity counts its own keys (Lua script)" "^1$" "$(cli "$cache" "${id[@]}" eval "$script" 1 identity:rate:x)"
expect "payment counts its own keys (INCR)" "^1$" "$(cli "$cache" "${pay[@]}" incr payment:webhook:x)"
expect "identity cannot touch payment's keys" "NOPERM" "$(cli "$cache" "${id[@]}" get payment:webhook:x)"
expect "nor through a script" "ACL failure|NOPERM" "$(cli "$cache" "${id[@]}" eval "return redis.call('DEL','payment:webhook:x')" 0)"
expect "payment cannot reset identity's login limits" "NOPERM" "$(cli "$cache" "${pay[@]}" del identity:rate:x)"
expect "no service can flush" "NOPERM" "$(cli "$cache" "${id[@]}" flushall)"
expect "no service can list keys" "NOPERM" "$(cli "$cache" "${id[@]}" keys '*')"
expect "no service can reconfigure" "NOPERM" "$(cli "$cache" "${pay[@]}" config set maxmemory 1)"
expect "notification has no user here" "WRONGPASS" "$(cli "$cache" "${note[@]}" ping)"
expect "monitor reads statistics" "# Memory" "$(cli "$cache" "${mon[@]}" info memory)"
expect "monitor reads no data" "NOPERM" "$(cli "$cache" "${mon[@]}" get identity:rate:x)"

echo "### queue instance"
expect "no anonymous access" "NOAUTH" "$(cli "$queue" ping)"
expect "notification writes its queue keys" "^1$" "$(cli "$queue" "${note[@]}" sadd asynq:queues test)"
expect "notification is confined to asynq:" "NOPERM" "$(cli "$queue" "${note[@]}" set identity:rate:x 1)"
expect "notification cannot flush" "NOPERM" "$(cli "$queue" "${note[@]}" flushall)"
expect "identity has no user here" "WRONGPASS" "$(cli "$queue" "${id[@]}" ping)"

echo "### result: $([ $fail = 0 ] && echo ALL PASS || echo FAILURES)"
exit $fail
