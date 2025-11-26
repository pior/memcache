package memcache

import (
	"time"

	"github.com/pior/memcache/meta"
	"github.com/sony/gobreaker/v2"
)

// CircuitBreaker wraps circuit breaker functionality.
// This allows users to provide their own implementation.
type CircuitBreaker interface {
	// Execute runs the given function if the circuit breaker is closed.
	// Returns error if circuit is open or if the function fails.
	Execute(func() (*meta.Response, error)) (*meta.Response, error)

	// State returns the current state of the circuit breaker.
	State() CircuitBreakerState
}

// CircuitBreakerState represents the state of a circuit breaker
type CircuitBreakerState int

const (
	CircuitStateClosed CircuitBreakerState = iota
	CircuitStateHalfOpen
	CircuitStateOpen
)

// String returns the string representation of the circuit breaker state
func (s CircuitBreakerState) String() string {
	switch s {
	case CircuitStateClosed:
		return "closed"
	case CircuitStateHalfOpen:
		return "half-open"
	case CircuitStateOpen:
		return "open"
	default:
		return "unknown"
	}
}

// GoBreakerWrapper adapts gobreaker.CircuitBreaker to our interface
type GoBreakerWrapper struct {
	cb *gobreaker.CircuitBreaker[*meta.Response]
}

func (w *GoBreakerWrapper) Execute(fn func() (*meta.Response, error)) (*meta.Response, error) {
	return w.cb.Execute(fn)
}

func (w *GoBreakerWrapper) State() CircuitBreakerState {
	switch w.cb.State() {
	case gobreaker.StateClosed:
		return CircuitStateClosed
	case gobreaker.StateHalfOpen:
		return CircuitStateHalfOpen
	case gobreaker.StateOpen:
		return CircuitStateOpen
	default:
		return CircuitStateClosed
	}
}

// NewGoBreaker creates a new circuit breaker using gobreaker
func NewGoBreaker(settings gobreaker.Settings) CircuitBreaker {
	return &GoBreakerWrapper{
		cb: gobreaker.NewCircuitBreaker[*meta.Response](settings),
	}
}

// NewGobreakerConfig returns a function that creates circuit breakers for servers.
// This is a helper for common use cases.
func NewGobreakerConfig(maxRequests uint32, interval, timeout time.Duration) func(string) CircuitBreaker {
	return func(serverAddr string) CircuitBreaker {
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
		return NewGoBreaker(settings)
	}
}
