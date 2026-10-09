package telemetry

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

// PW-008: the operator queues of the add-features (holds needing review,
// cancellations stuck, refunds whose transfer is unknown, …) are exported
// as metrics so an alert reaches someone, instead of only a log line.

// WorkQueue is one operator queue. SQL returns how many items need a
// person now (already past the queue's own threshold) and the age in
// seconds of the oldest of them (0 when none), e.g.
//
//	SELECT count(*), COALESCE(EXTRACT(EPOCH FROM now() - min(updated_at)), 0)::float8
//	FROM x WHERE status = 'needs_review'
//
// Severity is "critical" (money or stock at risk) or "warning". Labels
// carry the queue name only: no ids, no personal data.
type WorkQueue struct {
	Name     string
	Severity string
	SQL      string
}

var (
	workQueueItems  = prometheus.NewDesc("work_queue_attention_items", "Items that need a person now.", []string{"queue", "severity"}, nil)
	workQueueOldest = prometheus.NewDesc("work_queue_oldest_attention_seconds", "Age of the oldest item that needs a person (0 when none).", []string{"queue", "severity"}, nil)
	workQueueOK     = prometheus.NewDesc("work_queue_probe_success", "1 when the queue could be read at scrape time.", []string{"queue", "severity"}, nil)
)

// RegisterWorkQueues reads each queue at scrape time (bounded to 2s).
func RegisterWorkQueues(pool *pgxpool.Pool, queues ...WorkQueue) error {
	return prometheus.Register(workQueueCollector{pool: pool, queues: queues})
}

type workQueueCollector struct {
	pool   *pgxpool.Pool
	queues []WorkQueue
}

func (c workQueueCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- workQueueItems
	ch <- workQueueOldest
	ch <- workQueueOK
}

func (c workQueueCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for _, q := range c.queues {
		var items int64
		var oldest float64
		if err := c.pool.QueryRow(ctx, q.SQL).Scan(&items, &oldest); err != nil {
			ch <- prometheus.MustNewConstMetric(workQueueOK, prometheus.GaugeValue, 0, q.Name, q.Severity)
			continue
		}
		ch <- prometheus.MustNewConstMetric(workQueueOK, prometheus.GaugeValue, 1, q.Name, q.Severity)
		ch <- prometheus.MustNewConstMetric(workQueueItems, prometheus.GaugeValue, float64(items), q.Name, q.Severity)
		ch <- prometheus.MustNewConstMetric(workQueueOldest, prometheus.GaugeValue, oldest, q.Name, q.Severity)
	}
}
