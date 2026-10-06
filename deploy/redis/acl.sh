#!/bin/sh
# Starts Redis with one ACL user per client service, built from passwords
# in the environment (stored as SHA-256 in a file only Redis reads, never
# on the command line). The unauthenticated default user is off, and each
# service reaches only its own keys and commands, so a compromised or
# buggy service cannot read, rewrite or flush another's data.
#
#   REDIS_ROLE=cache   rate-limit counters of identity, payment, review
#   REDIS_ROLE=queue   notification's Asynq jobs
#
# Both: user "monitor" (redis_exporter, healthcheck) reads statistics only.
# Arguments are passed to redis-server. Passwords: deploy/gen-redis-passwords.sh.
set -eu

acl=/tmp/users.acl

require() {
  eval "value=\${$1:-}"
  case "$value" in
    "" | CHANGE_ME) echo "redis-acl: $1 is not set" >&2; exit 1 ;;
  esac
}
hash() { printf '%s' "$1" | sha256sum | cut -d' ' -f1; }

case "${REDIS_ROLE:-}" in
  cache) users="IDENTITY PAYMENT REVIEW" ;;
  queue) users="NOTIFICATION" ;;
  *) echo "redis-acl: REDIS_ROLE must be cache or queue" >&2; exit 1 ;;
esac
require REDIS_MONITOR_PASSWORD
for u in $users; do require "${u}_REDIS_PASSWORD"; done

# Connection set-up every client sends (go-redis: HELLO, CLIENT SETINFO).
connect="+ping +hello +auth +client|setinfo +select"

umask 077
{
  echo "user default off resetkeys resetchannels -@all"
  echo "user monitor on #$(hash "$REDIS_MONITOR_PASSWORD") resetkeys resetchannels -@all $connect +info +config|get +client|list +client|setname +slowlog|get +slowlog|len +latency|latest +latency|histogram +memory|stats"
  for u in $users; do
    name=$(echo "$u" | tr '[:upper:]' '[:lower:]')
    eval "password=\${${u}_REDIS_PASSWORD}"
    case "$name" in
      # Fixed-window counters: INCR + EXPIRE, directly or in a Lua script.
      identity | payment | review)
        echo "user $name on #$(hash "$password") resetkeys ~$name:* resetchannels -@all $connect +get +set +del +incr +expire +ttl +eval +evalsha" ;;
      # Asynq keeps every key and channel under "asynq:"; no admin or
      # dangerous commands (FLUSHALL, KEYS, CONFIG, DEBUG, ...).
      notification)
        echo "user notification on #$(hash "$password") resetkeys ~asynq:* resetchannels &asynq:* +@all -@admin -@dangerous" ;;
    esac
  done
} > "$acl"
chown redis:redis "$acl" 2> /dev/null || true

# The image's entrypoint prepares /data and drops to the redis user.
exec docker-entrypoint.sh redis-server "$@" --aclfile "$acl"
