package stats

import (
	"testing"
	"time"

	"github.com/DataDog/datadog-go/v5/statsd"
	"github.com/stretchr/testify/assert"
)

func TestConfigure(t *testing.T) {
	var err error

	assert.Equal(t, &statsd.NoOpClient{}, Client(), "Client() should be NullStatter before Configure()")

	Client().Gauge("foo", 1, []string{}, 1.0)
	dur := WithTiming("test", nil, func() {
		time.Sleep(10 * time.Millisecond)
	})
	assert.True(t, dur >= 10*time.Millisecond, "WithTiming should work with an unconfigured client")

	err = Configure("kse.", "127.0.0.1", 8125, map[string]string{})
	assert.Nil(t, err, "Configure should not return an error")
	assert.NotNil(t, Client(), "Client should not be nil after configure")
}

func TestWithTiming(t *testing.T) {
	err := Configure("kse.", "127.0.0.1", 8125, map[string]string{})
	assert.Nil(t, err, "Configure should not return an error")

	dur := WithTiming("test", nil, func() {
		time.Sleep(10 * time.Millisecond)
	})
	assert.True(t, dur >= 10*time.Millisecond)
}
