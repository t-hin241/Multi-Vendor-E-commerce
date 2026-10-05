package telemetry

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
)

// RegisterDBPool exports the connection pool's state. Saturation shows as
// acquired == max and as growth of the empty-acquire count and wait time:
// requests waiting for a free connection.
func RegisterDBPool(pool *pgxpool.Pool) error {
	return prometheus.Register(dbPoolCollector{pool: pool})
}

var (
	dbAcquired     = prometheus.NewDesc("db_pool_acquired_connections", "Connections in use.", nil, nil)
	dbIdle         = prometheus.NewDesc("db_pool_idle_connections", "Idle connections.", nil, nil)
	dbTotal        = prometheus.NewDesc("db_pool_total_connections", "Open connections.", nil, nil)
	dbMax          = prometheus.NewDesc("db_pool_max_connections", "Pool size limit (DB_MAX_CONNS).", nil, nil)
	dbAcquires     = prometheus.NewDesc("db_pool_acquires_total", "Connections acquired.", nil, nil)
	dbEmptyAcq     = prometheus.NewDesc("db_pool_empty_acquires_total", "Acquires that had to wait for a connection.", nil, nil)
	dbEmptyWait    = prometheus.NewDesc("db_pool_empty_acquire_wait_seconds_total", "Time spent waiting for a free connection.", nil, nil)
	dbCanceledAcq  = prometheus.NewDesc("db_pool_canceled_acquires_total", "Acquires abandoned because the context ended.", nil, nil)
	dbAcquireTime  = prometheus.NewDesc("db_pool_acquire_seconds_total", "Total time spent acquiring connections.", nil, nil)
	dbNewConns     = prometheus.NewDesc("db_pool_new_connections_total", "Connections opened.", nil, nil)
	redisHits      = prometheus.NewDesc("redis_pool_hits_total", "Connection found free in the pool.", nil, nil)
	redisMisses    = prometheus.NewDesc("redis_pool_misses_total", "Connection not found in the pool.", nil, nil)
	redisTimeouts  = prometheus.NewDesc("redis_pool_timeouts_total", "Waits for a connection that timed out.", nil, nil)
	redisTotal     = prometheus.NewDesc("redis_pool_total_connections", "Open connections.", nil, nil)
	redisIdle      = prometheus.NewDesc("redis_pool_idle_connections", "Idle connections.", nil, nil)
	outboxPending  = prometheus.NewDesc("outbox_pending_events", "Events written but not yet delivered.", []string{"outbox"}, nil)
	outboxOldest   = prometheus.NewDesc("outbox_oldest_pending_seconds", "Age of the oldest undelivered event (0 when none).", []string{"outbox"}, nil)
	outboxScrapeOK = prometheus.NewDesc("outbox_probe_success", "1 when the outbox could be read at scrape time.", []string{"outbox"}, nil)
)

type dbPoolCollector struct{ pool *pgxpool.Pool }

func (c dbPoolCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{dbAcquired, dbIdle, dbTotal, dbMax, dbAcquires, dbEmptyAcq, dbEmptyWait, dbCanceledAcq, dbAcquireTime, dbNewConns} {
		ch <- d
	}
}

func (c dbPoolCollector) Collect(ch chan<- prometheus.Metric) {
	s := c.pool.Stat()
	ch <- prometheus.MustNewConstMetric(dbAcquired, prometheus.GaugeValue, float64(s.AcquiredConns()))
	ch <- prometheus.MustNewConstMetric(dbIdle, prometheus.GaugeValue, float64(s.IdleConns()))
	ch <- prometheus.MustNewConstMetric(dbTotal, prometheus.GaugeValue, float64(s.TotalConns()))
	ch <- prometheus.MustNewConstMetric(dbMax, prometheus.GaugeValue, float64(s.MaxConns()))
	ch <- prometheus.MustNewConstMetric(dbAcquires, prometheus.CounterValue, float64(s.AcquireCount()))
	ch <- prometheus.MustNewConstMetric(dbEmptyAcq, prometheus.CounterValue, float64(s.EmptyAcquireCount()))
	ch <- prometheus.MustNewConstMetric(dbEmptyWait, prometheus.CounterValue, s.EmptyAcquireWaitTime().Seconds())
	ch <- prometheus.MustNewConstMetric(dbCanceledAcq, prometheus.CounterValue, float64(s.CanceledAcquireCount()))
	ch <- prometheus.MustNewConstMetric(dbAcquireTime, prometheus.CounterValue, s.AcquireDuration().Seconds())
	ch <- prometheus.MustNewConstMetric(dbNewConns, prometheus.CounterValue, float64(s.NewConnsCount()))
}

// RegisterRedisPool exports the Redis client's connection pool state.
func RegisterRedisPool(client *redis.Client) error {
	return prometheus.Register(redisPoolCollector{client: client})
}

type redisPoolCollector struct{ client *redis.Client }

func (c redisPoolCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{redisHits, redisMisses, redisTimeouts, redisTotal, redisIdle} {
		ch <- d
	}
}

func (c redisPoolCollector) Collect(ch chan<- prometheus.Metric) {
	s := c.client.PoolStats()
	ch <- prometheus.MustNewConstMetric(redisHits, prometheus.CounterValue, float64(s.Hits))
	ch <- prometheus.MustNewConstMetric(redisMisses, prometheus.CounterValue, float64(s.Misses))
	ch <- prometheus.MustNewConstMetric(redisTimeouts, prometheus.CounterValue, float64(s.Timeouts))
	ch <- prometheus.MustNewConstMetric(redisTotal, prometheus.GaugeValue, float64(s.TotalConns))
	ch <- prometheus.MustNewConstMetric(redisIdle, prometheus.GaugeValue, float64(s.IdleConns))
}

// Outbox describes how to read one outbox's backlog: SQL returning the
// number of undelivered rows and the age in seconds of the oldest one
// (0 when empty), e.g.
//
//	SELECT count(*), COALESCE(EXTRACT(EPOCH FROM now() - min(created_at)), 0)
//	FROM x_outbox WHERE published_at IS NULL
type Outbox struct {
	Name string
	SQL  string
}

// RegisterOutboxes reads each outbox's backlog at scrape time (bounded to
// 2s per scrape). A growing oldest age means events are not leaving the
// service: broker down, consumer contract broken, or relay too slow.
func RegisterOutboxes(pool *pgxpool.Pool, outboxes ...Outbox) error {
	return prometheus.Register(outboxCollector{pool: pool, outboxes: outboxes})
}

type outboxCollector struct {
	pool     *pgxpool.Pool
	outboxes []Outbox
}

func (c outboxCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- outboxPending
	ch <- outboxOldest
	ch <- outboxScrapeOK
}

func (c outboxCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for _, o := range c.outboxes {
		var pending int64
		var oldest float64
		if err := c.pool.QueryRow(ctx, o.SQL).Scan(&pending, &oldest); err != nil {
			ch <- prometheus.MustNewConstMetric(outboxScrapeOK, prometheus.GaugeValue, 0, o.Name)
			continue
		}
		ch <- prometheus.MustNewConstMetric(outboxScrapeOK, prometheus.GaugeValue, 1, o.Name)
		ch <- prometheus.MustNewConstMetric(outboxPending, prometheus.GaugeValue, float64(pending), o.Name)
		ch <- prometheus.MustNewConstMetric(outboxOldest, prometheus.GaugeValue, oldest, o.Name)
	}
}
