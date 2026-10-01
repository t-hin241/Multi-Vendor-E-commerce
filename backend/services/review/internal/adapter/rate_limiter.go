package adapter

import (
	"context"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisRateLimiter counts actions per key in fixed windows. Counters are
// disposable: losing them only resets the windows.
type RedisRateLimiter struct {
	Client redis.UniversalClient
	Prefix string
}

const rateScript = `local n=redis.call('INCR',KEYS[1]); if n==1 then redis.call('EXPIRE',KEYS[1],ARGV[1]) end; return n`

// Allow counts one action for key and reports whether it stays within
// limit per window.
func (l RedisRateLimiter) Allow(ctx context.Context, key string, limit int64, window time.Duration) (bool, error) {
	seconds := int64(window / time.Second)
	if seconds <= 0 {
		seconds = 60
	}
	k := l.Prefix + key + ":" + strconv.FormatInt(time.Now().Unix()/seconds, 10)
	n, err := l.Client.Eval(ctx, rateScript, []string{k}, seconds).Int64()
	if err != nil {
		return true, err
	}
	return n <= limit, nil
}
