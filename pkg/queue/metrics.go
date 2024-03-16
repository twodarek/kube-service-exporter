package queue

import (
	"fmt"
	"time"

	"github.com/DataDog/datadog-go/v5/statsd"
	"github.com/github/kube-service-exporter/pkg/stats"
)

// Metrics is the interface that needs to be fulfilled to provide
// a metric implementation to the queue for instrumentation
type Metrics interface {
	add(item *Item, queueLength int)
	get(item *Item, queueLength int)
	forget(item *Item)
}

// NoopMetrics is an implementation that provides no functionality
// this is used with the default PriorityQueue construction
type NoopMetrics struct {
}

func NewNoopMetrics() *NoopMetrics {
	return &NoopMetrics{}
}

func (m *NoopMetrics) add(item *Item, queueLength int) {}
func (m *NoopMetrics) get(item *Item, queueLength int) {}
func (m *NoopMetrics) forget(item *Item)               {}

// StatsdQueueMetrics implements the datadog stats provider and
// fulfills the QueueMetrics interface so we can easily track our
// queue
type StatsdQueueMetrics struct {
	// statsd client
	client statsd.ClientInterface
	// these are the times that the items were added to the queue
	// so they can be diffed when sending processing metrics
	addTimes map[*Item]time.Time
	// these are the times when the item begins to be processed
	// this allows us to see difference between wait and processing times
	processingStartTimes map[*Item]time.Time

	// prefix is the prefix for the statsd metrics
	prefix string
}

func NewStatsdQueueMetrics(client statsd.ClientInterface, prefix string) *StatsdQueueMetrics {
	return &StatsdQueueMetrics{
		client:               client,
		addTimes:             make(map[*Item]time.Time),
		processingStartTimes: make(map[*Item]time.Time),
		prefix:               prefix,
	}
}

// add will increase the counters for queue.adds and queue.length as well
// as begin the timer for the time the item was added to the queue
func (m *StatsdQueueMetrics) add(item *Item, queueLength int) {
	tag := priorityTag(item)
	m.client.Incr(m.metricWithPrefix("queue.adds"), tag, 1.0)
	m.client.Gauge(m.metricWithPrefix("queue.length"), float64(queueLength), tag, 1.0)
	if _, exists := m.addTimes[item]; !exists {
		m.addTimes[item] = time.Now()
	}
}

// get will decrease the length of the queue as well as add a timestamp for the
// beginning of when it started being processed and it will send the time it waited in
// queue
func (m *StatsdQueueMetrics) get(item *Item, queueLength int) {
	tag := priorityTag(item)
	m.client.Gauge(m.metricWithPrefix("queue.length"), float64(queueLength), tag, 1.0)
	m.processingStartTimes[item] = time.Now()

	if startTime, exists := m.addTimes[item]; exists {
		stats.DistributionMs(m.metricWithPrefix("queue.wait"), tag, time.Since(startTime))
		delete(m.addTimes, item)
	}
}

// forget will calculate the time an item started being processed once it was picked up
// off the queue.
func (m *StatsdQueueMetrics) forget(item *Item) {
	if startTime, exists := m.processingStartTimes[item]; exists {
		stats.DistributionMs(m.metricWithPrefix("queue.duration"), priorityTag(item), time.Since(startTime))
		delete(m.processingStartTimes, item)
	}
}

// metricWithPrefix returns a metric name with the prefix prepended if it is set
func (m *StatsdQueueMetrics) metricWithPrefix(metric string) string {
	if m.prefix == "" {
		return metric
	}

	return fmt.Sprintf("%s.%s", m.prefix, metric)
}

func priorityTag(item *Item) []string {
	return []string{fmt.Sprintf("priority:%g", item.Priority())}
}
