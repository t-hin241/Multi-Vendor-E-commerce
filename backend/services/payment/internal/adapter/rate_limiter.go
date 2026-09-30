package adapter

import (
	"context"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisRateLimiter is a fixed-window counter per key.
type RedisRateLimiter struct {
	Client redis.UniversalClient
	Prefix string
	Limit  int64
	Window time.Duration
}

// Allow counts one request for key and reports whether it is within the limit.
func (l RedisRateLimiter) Allow(ctx context.Context, key string) (bool, error) {
	window := int64(l.Window / time.Second)
	if window <= 0 {
		window = 60
	}
	k := l.Prefix + key + ":" + strconv.FormatInt(time.Now().Unix()/window, 10)
	n, err := l.Client.Incr(ctx, k).Result()
	if err != nil {
		return true, err
	}
	if n == 1 {
		l.Client.Expire(ctx, k, time.Duration(window)*time.Second)
	}
	return n <= l.Limit, nil
}
