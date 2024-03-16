// stats is a very lightweight wrapper around the dogstatsd client, which gives
// us a package-level function
package stats

import (
	"fmt"
	"time"

	"github.com/DataDog/datadog-go/v5/statsd"
	"github.com/pkg/errors"
)

func init() {
	// If we don't configure the client, we'll get a NoopClient to avoid panics in tests
	client = &statsd.NoOpClient{}
}

var client statsd.ClientInterface

func Configure(namespace string, host string, port int, tags map[string]string) error {
	// Synthesize the tags into an array of strings so that we don't break the existing configuration
	tagsarray := make([]string, len(tags))
	for k, v := range tags {
		tagsarray = append(tagsarray, fmt.Sprintf("%s:%s", k, v))
	}

	var err error
	client, err = statsd.New(fmt.Sprintf("%s:%d", host, port), statsd.WithNamespace(namespace), statsd.WithTags(tagsarray))
	if err != nil {
		return errors.Wrap(err, "Error configuring statsd client")
	}
	return nil

}

func Client() statsd.ClientInterface {
	return client
}

func WithTiming(name string, tags []string, f func()) time.Duration {
	start := time.Now()
	f()
	took := time.Since(start)
	client.Timing(name, took, tags, 1.0)
	return took
}

func DistributionMs(name string, tags []string, value time.Duration) {
	client.Distribution(name, float64(value/time.Millisecond), tags, 1.0)
}

func IncrSuccessOrFail(err error, prefix string, tags []string) {
	var key string

	if err == nil {
		key = prefix + ".success"
	} else {
		key = prefix + ".fail"
	}

	client.Incr(key, tags, 1.0)
}
