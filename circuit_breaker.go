package memcache

import (
	"time"

	"github.com/sony/gobreaker/v2"
)

// NewCircuitBreakerConfig returns a function that creates circuit breakers for servers.
// This is a helper for common use cases.
// Uses CircuitBreaker[bool] to support both single and batch operations.
func NewCircuitBreakerConfig(maxRequests uint32, interval, timeout time.Duration) func(string) *gobreaker.CircuitBreaker[bool] {
	return func(serverAddr string) *gobreaker.CircuitBreaker[bool] {
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
		return gobreaker.NewCircuitBreaker[bool](settings)
	}
}
