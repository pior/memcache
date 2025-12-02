package memcache

import (
	"time"

	"github.com/pior/memcache/meta"
	"github.com/sony/gobreaker/v2"
)

// NewCircuitBreakerConfig returns a function that creates circuit breakers for servers.
// This is a helper for common use cases.
func NewCircuitBreakerConfig(maxRequests uint32, interval, timeout time.Duration) func(string) *gobreaker.CircuitBreaker[*meta.Response] {
	return func(serverAddr string) *gobreaker.CircuitBreaker[*meta.Response] {
		settings := gobreaker.Settings{
			Name:        serverAddr,
			MaxRequests: maxRequests,
			Interval:    interval,
			Timeout:     timeout,
			ReadyToTrip: func(counts gobreaker.Counts) bool {
				failureRatio := float64(counts.TotalFailures) / float64(counts.Requests)
				return counts.Requests >= 3 && failureRatio >= 0.6
			},
		}
		return gobreaker.NewCircuitBreaker[*meta.Response](settings)
	}
}
