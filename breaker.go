package memcache

import (
	"context"
	"errors"
	"time"

	"github.com/pior/memcache/meta"
	"github.com/sony/gobreaker/v2"
)

// BreakerConfig configures the circuit breaker guarding each server.
//
// Every server gets its own breaker. While the breaker is closed, operations
// flow normally and their outcomes are counted. When failures dominate (see
// TripMinRequests and TripFailureRatio), the breaker opens: operations to
// that server fail immediately with ErrBreakerOpen instead of tying up
// connections on a server that is misbehaving. After OpenDuration the breaker
// becomes half-open and lets HalfOpenMaxRequests probe operations through: if
// a probe fails the breaker reopens, once they all succeed it closes.
//
// Only transport-level errors count as failures: dial errors, socket I/O
// errors, and operations cut off by the operation timeout (Config.Timeout).
// A cache miss is a normal outcome, not a failure. Errors caused by the
// caller — a canceled context, an expired caller deadline, a request rejected
// by client-side validation — say nothing about server health and are not
// counted at all, in either direction.
//
// A hung server — reachable and accepting connections but never responding —
// is visible only as timeouts, and a timeout counts against the server only
// when Config.Timeout was the binding deadline. When every caller passes a
// context deadline at or below Config.Timeout, each timeout is attributed to
// the caller and excluded, so live traffic alone can never trip the breaker
// on a hung server. The background maintenance loop closes that gap: when a
// pass finds the server unresponsive it confirms by probing it
// TripMinRequests times on fresh connections under the operator's own
// deadlines and records the outcomes, tripping the breaker regardless of the
// deadlines callers use. Detection takes up to Config.MaintenanceInterval
// plus a few Config.Timeout.
type BreakerConfig struct {
	// Enabled turns the circuit breaker on. When false (the zero value) no
	// breaker is created, every operation is always attempted, and the other
	// fields are ignored.
	Enabled bool

	// TripMinRequests is the minimum number of operations that must have been
	// observed within TripWindow before the breaker can trip. Below this
	// volume the failure ratio is statistically meaningless (one failure out
	// of two operations is not an outage), so the breaker stays closed
	// regardless of the ratio.
	// The default is DefaultBreakerTripMinRequests (10).
	TripMinRequests uint32

	// TripFailureRatio is the fraction of failed operations at which the
	// breaker trips. Each time an operation fails, the breaker looks at the
	// operations observed within TripWindow and opens when at least
	// TripMinRequests were observed and failures/operations reaches this
	// ratio. Must be in (0, 1]: 0.6 means "open when 60% of recent
	// operations failed", 1 means "open only when every recent operation
	// failed".
	// The default is DefaultBreakerTripFailureRatio (0.6).
	TripFailureRatio float64

	// TripWindow is how far back the trip condition looks: the operation and
	// failure counts cover approximately the last TripWindow (a rolling
	// window), so old failures age out and cannot combine with fresh ones to
	// trip the breaker long after a blip.
	// The default is DefaultBreakerTripWindow (10s).
	TripWindow time.Duration

	// OpenDuration is how long the breaker stays open after tripping. While
	// open, every operation to the server fails immediately with
	// ErrBreakerOpen, without dialing or using a connection. When it
	// elapses, the breaker becomes half-open and probes the server.
	// The default is DefaultBreakerOpenDuration (5s).
	OpenDuration time.Duration

	// HalfOpenMaxRequests is the number of operations let through while the
	// breaker is half-open; any further operations fail with ErrBreakerOpen
	// until the probes complete. A single failed probe reopens the breaker
	// for another OpenDuration; once this many probes have succeeded the
	// breaker closes.
	// The default is DefaultBreakerHalfOpenMaxRequests (1).
	HalfOpenMaxRequests uint32

	// OnStateChange, if set, is called whenever a server's breaker changes
	// state. server is the server address; from and to are "closed",
	// "half-open" or "open" (the values reported in BreakerStats.State).
	// It is called synchronously from the operation's goroutine while the
	// breaker's internal lock is held: it must return quickly and must not
	// perform memcache operations. Use it for logging and metrics.
	OnStateChange func(server, from, to string)
}

// Defaults for BreakerConfig. A field left at zero (or negative, for the
// ratio and the durations) gets its default.
const (
	// DefaultBreakerTripMinRequests requires a meaningful sample before the
	// failure ratio is trusted; it also means very-low-traffic pools (under
	// one operation per second) never trip, which is fine: a breaker
	// protects against load piling onto a bad server, and there is no
	// pile-up without load.
	DefaultBreakerTripMinRequests = 10

	// DefaultBreakerTripFailureRatio tolerates transient error bursts (a
	// rolling restart, a dropped connection) but trips well before total
	// failure.
	DefaultBreakerTripFailureRatio = 0.6

	// DefaultBreakerTripWindow keeps the counts fresh: with the default
	// operation timeout of 1s, a short window bounds how much healthy
	// history a sudden outage has to overcome before the ratio trips.
	DefaultBreakerTripWindow = 10 * time.Second

	// DefaultBreakerOpenDuration sheds load long enough for a struggling
	// server to recover while retesting quickly: a false trip costs at most
	// a few seconds of fast-failing operations.
	DefaultBreakerOpenDuration = 5 * time.Second

	// DefaultBreakerHalfOpenMaxRequests closes the breaker after a single
	// successful probe: memcache operations are cheap and frequent, so one
	// probe per OpenDuration is signal enough, and a failed probe reopens
	// immediately anyway.
	DefaultBreakerHalfOpenMaxRequests = 1
)

// withDefaults returns the config with every unset field resolved, so the
// trip policy and the hung-server confirmation burst (which is sized to
// TripMinRequests, see ServerPool.confirmHungServer) agree on the effective
// values.
func (c BreakerConfig) withDefaults() BreakerConfig {
	if c.TripMinRequests == 0 {
		c.TripMinRequests = DefaultBreakerTripMinRequests
	}
	if c.TripFailureRatio <= 0 {
		c.TripFailureRatio = DefaultBreakerTripFailureRatio
	}
	if c.TripWindow <= 0 {
		c.TripWindow = DefaultBreakerTripWindow
	}
	if c.OpenDuration <= 0 {
		c.OpenDuration = DefaultBreakerOpenDuration
	}
	if c.HalfOpenMaxRequests == 0 {
		c.HalfOpenMaxRequests = DefaultBreakerHalfOpenMaxRequests
	}
	return c
}

// newBreaker builds the breaker for one server, or returns nil when the
// breaker is disabled. The underlying gobreaker package is an implementation
// detail: its types and errors never cross the public API (see BreakerConfig,
// BreakerStats and ErrBreakerOpen).
func newBreaker(addr string, config BreakerConfig) *gobreaker.CircuitBreaker[bool] {
	if !config.Enabled {
		return nil
	}

	config = config.withDefaults()

	settings := gobreaker.Settings{
		Name:        addr,
		MaxRequests: config.HalfOpenMaxRequests,
		Interval:    config.TripWindow,
		// Sub-window buckets make the counts a rolling window over
		// TripWindow instead of a fixed window that periodically resets
		// to zero.
		BucketPeriod: config.TripWindow / 10,
		Timeout:      config.OpenDuration,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			// Requests includes excluded operations (caller cancellations,
			// invalid requests). They say nothing about server health, so
			// the trip condition must not let them dilute the ratio.
			if counts.Requests < counts.TotalExclusions {
				return false
			}
			counted := counts.Requests - counts.TotalExclusions
			return counted >= config.TripMinRequests &&
				float64(counts.TotalFailures) >= config.TripFailureRatio*float64(counted)
		},
		IsExcluded: isBreakerExcluded,
	}
	if config.OnStateChange != nil {
		onStateChange := config.OnStateChange
		settings.OnStateChange = func(name string, from, to gobreaker.State) {
			onStateChange(name, from.String(), to.String())
		}
	}

	return gobreaker.NewCircuitBreaker[bool](settings)
}

// mapBreakerRejection converts gobreaker's rejection errors (open state,
// half-open probe quota exhausted) into the exported ErrBreakerOpen sentinel,
// so callers can detect rejections without importing gobreaker. Any other
// error passes through unchanged.
func mapBreakerRejection(err error) error {
	if errors.Is(err, gobreaker.ErrOpenState) || errors.Is(err, gobreaker.ErrTooManyRequests) {
		return ErrBreakerOpen
	}
	return err
}

// isBreakerExcluded reports errors that say nothing about server health: a
// caller cancellation or deadline, and requests rejected by client-side
// validation. A socket timeout counts as a server failure only when the
// operator-configured Timeout was the binding deadline; when a caller-imposed
// deadline caused it, Connection also wraps the caller's context error and the
// timeout is excluded here.
func isBreakerExcluded(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var invalidRequest *meta.InvalidRequestError
	return errors.As(err, &invalidRequest)
}

// BreakerStats is a snapshot of a server's circuit breaker. When no breaker
// is configured, State is empty and the counts are zero. The counts cover
// the operations observed within the current TripWindow.
type BreakerStats struct {
	State                string // "", "closed", "open" or "half-open"
	Requests             uint32
	TotalSuccesses       uint32
	TotalFailures        uint32
	ConsecutiveSuccesses uint32
	ConsecutiveFailures  uint32
}
