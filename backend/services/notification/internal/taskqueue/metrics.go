package taskqueue

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	queueTasks = prometheus.NewDesc("notification_queue_tasks", "Delivery jobs in the Asynq queue by state.", []string{"state"}, nil)
	// Latency is how long the oldest pending job has waited for a worker.
	queueLatency = prometheus.NewDesc("notification_queue_latency_seconds", "Wait of the oldest pending delivery job.", nil, nil)
	queueUp      = prometheus.NewDesc("notification_queue_probe_success", "1 when the queue could be read at scrape time.", nil, nil)
)

// Collector exports the queue's depth and latency, read from Redis at
// scrape time.
func (q *Queue) Collector() prometheus.Collector { return queueCollector{q: q} }

type queueCollector struct{ q *Queue }

func (c queueCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- queueTasks
	ch <- queueLatency
	ch <- queueUp
}

func (c queueCollector) Collect(ch chan<- prometheus.Metric) {
	info, err := c.q.inspector.GetQueueInfo(c.q.name)
	if err != nil {
		// No job was ever queued (the queue does not exist yet) or Redis
		// is unreachable: report the probe, not zeros that look healthy.
		ch <- prometheus.MustNewConstMetric(queueUp, prometheus.GaugeValue, 0)
		return
	}
	ch <- prometheus.MustNewConstMetric(queueUp, prometheus.GaugeValue, 1)
	for state, n := range map[string]int{
		"pending": info.Pending, "active": info.Active, "scheduled": info.Scheduled,
		"retry": info.Retry, "archived": info.Archived,
	} {
		ch <- prometheus.MustNewConstMetric(queueTasks, prometheus.GaugeValue, float64(n), state)
	}
	ch <- prometheus.MustNewConstMetric(queueLatency, prometheus.GaugeValue, info.Latency.Seconds())
}
