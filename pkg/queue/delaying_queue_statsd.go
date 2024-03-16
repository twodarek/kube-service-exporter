package queue

import (
	"fmt"
	"time"

	"github.com/github/kube-service-exporter/pkg/stats"
	workqueue "k8s.io/client-go/util/workqueue"
)

// DelayingQueueStatsd is a wrapper around workqueue.DelayingInterface
// that adds statsd metrics. This is necessary because the workqueue
// MetricsProvier interface only supports Prometheus metrics, so we need
// to wrap the DelayedQueue to add statsd metrics.
type DelayingQueueStatsd struct {
	queue workqueue.RateLimitingInterface
	// prefix is a prefix for the metric names
	prefix string
}

// make sure the interface is implemented
var _ workqueue.RateLimitingInterface = (*DelayingQueueStatsd)(nil)

func NewDelayingQueueStatsd(prefix string) *DelayingQueueStatsd {
	dqs := DelayingQueueStatsd{
		queue:  workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter()),
		prefix: prefix,
	}

	return &dqs
}

func (dqs *DelayingQueueStatsd) AddAfter(item interface{}, duration time.Duration) {
	dqs.queue.AddAfter(item, duration)
	stats.Client().Incr(dqs.metricWithPrefix("queue.add_after"), []string{}, 1.0)
	stats.Client().Gauge(dqs.metricWithPrefix("queue.length"), float64(dqs.queue.Len()), []string{}, 1.0)
}

func (dqs *DelayingQueueStatsd) AddRateLimited(item interface{}) {
	dqs.queue.AddRateLimited(item)
	stats.Client().Incr(dqs.metricWithPrefix("queue.add_rate_limited"), []string{}, 1.0)
}

func (dqs *DelayingQueueStatsd) Forget(item interface{}) {
	dqs.queue.Forget(item)
	stats.Client().Incr(dqs.metricWithPrefix("queue.forget"), []string{}, 1.0)
	stats.Client().Gauge(dqs.metricWithPrefix("queue.length"), float64(dqs.queue.Len()), []string{}, 1.0)
}

func (dqs *DelayingQueueStatsd) NumRequeues(item interface{}) int {
	return dqs.queue.NumRequeues(item)
}

func (dqs *DelayingQueueStatsd) Add(item interface{}) {
	dqs.queue.Add(item)
	stats.Client().Incr(dqs.metricWithPrefix("queue.add"), []string{}, 1)
	stats.Client().Gauge(dqs.metricWithPrefix("queue.length"), float64(dqs.queue.Len()), []string{}, 1.0)
}

func (dqs *DelayingQueueStatsd) Len() int {
	length := dqs.queue.Len()
	stats.Client().Gauge(dqs.metricWithPrefix("queue.length"), float64(length), []string{}, 1.0)
	return length
}

func (dqs *DelayingQueueStatsd) Get() (item interface{}, shutdown bool) {
	stats.Client().Incr(dqs.metricWithPrefix("queue.add"), []string{}, 1.0)
	stats.Client().Gauge(dqs.metricWithPrefix("queue.length"), float64(dqs.queue.Len()), []string{}, 1.0)
	return dqs.queue.Get()
}

func (dqs *DelayingQueueStatsd) Done(item interface{}) {
	dqs.queue.Done(item)
	stats.Client().Incr(dqs.metricWithPrefix("queue.done"), []string{}, 1.0)
	stats.Client().Gauge(dqs.metricWithPrefix("queue.length"), float64(dqs.queue.Len()), []string{}, 1.0)
}

func (dqs *DelayingQueueStatsd) ShutDown() {
	stats.Client().Incr(dqs.metricWithPrefix("queue.shutdown"), []string{"drain:false"}, 1.0)
	dqs.queue.ShutDown()
}

func (dqs *DelayingQueueStatsd) ShutDownWithDrain() {
	stats.Client().Incr(dqs.metricWithPrefix("queue.shutdown"), []string{"drain:true"}, 1.0)
	dqs.queue.ShutDownWithDrain()
}

func (dqs *DelayingQueueStatsd) ShuttingDown() bool {
	return dqs.queue.ShuttingDown()
}

// metricWithPrefix returns a metric name with the prefix prepended if it is set
func (dsq *DelayingQueueStatsd) metricWithPrefix(metric string) string {
	if dsq.prefix == "" {
		return metric
	}

	return fmt.Sprintf("%s.%s", dsq.prefix, metric)
}
