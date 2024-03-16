package queue

import (
	"testing"
	"time"

	"github.com/github/kube-service-exporter/pkg/stats"
	"github.com/stretchr/testify/assert"
)

func TestStatsdQueueMetrics(t *testing.T) {
	if err := stats.Configure("dd-test", "localhost", 8125, map[string]string{}); err != nil {
		t.FailNow()
	}

	t.Run("metrics has timer started", func(t *testing.T) {
		metrics := NewStatsdQueueMetrics(stats.Client(), "")
		item := &Item{value: "test"}
		metrics.add(item, 6)

		assert.NotEmpty(t, metrics.addTimes[item])
		assert.Equal(t, time.Now().Day(), metrics.addTimes[item].Day())
	})

	t.Run("metrics has processing start time", func(t *testing.T) {
		metrics := NewStatsdQueueMetrics(stats.Client(), "")
		item := &Item{value: "test"}
		metrics.add(item, 6)
		metrics.get(item, 5)

		assert.NotEmpty(t, metrics.processingStartTimes[item])
		// should delete added times after processing
		assert.Empty(t, metrics.addTimes[item])
	})

	t.Run("metrics has no processing time when forgotten", func(t *testing.T) {
		metrics := NewStatsdQueueMetrics(stats.Client(), "")
		item := &Item{value: "test"}

		metrics.add(item, 6)
		assert.Empty(t, metrics.processingStartTimes[item])
		assert.NotEmpty(t, metrics.addTimes[item])

		metrics.get(item, 5)
		assert.Empty(t, metrics.addTimes[item])
		assert.NotEmpty(t, metrics.processingStartTimes[item])

		metrics.forget(item)
		assert.Empty(t, metrics.processingStartTimes[item])
	})

}

func TestMetricWithPrefix(t *testing.T) {
	t.Run("when metric has no prefix", func(t *testing.T) {
		metrics := NewStatsdQueueMetrics(stats.Client(), "")
		assert.Equal(t, "metric", metrics.metricWithPrefix("metric"))
	})

	t.Run("when metric has prefix", func(t *testing.T) {
		metrics := NewStatsdQueueMetrics(stats.Client(), "prefix")
		assert.Equal(t, "prefix.metric", metrics.metricWithPrefix("metric"))
	})
}
