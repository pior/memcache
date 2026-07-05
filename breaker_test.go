package memcache

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"slices"
	"testing"

	"github.com/pior/memcache/meta"
	"github.com/sony/gobreaker/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// breakerExec drives one operation outcome through a breaker: err is the
// operation's result (nil for success).
func breakerExec(cb *gobreaker.CircuitBreaker[bool], err error) error {
	_, execErr := cb.Execute(func() (bool, error) {
		return err == nil, err
	})
	return execErr
}

func TestNewBreaker_Disabled(t *testing.T) {
	assert.Nil(t, newBreaker("cache1:11211", BreakerConfig{}))
}

func TestNewBreaker_TripPolicy(t *testing.T) {
	errServer := errors.New("server failure")

	tests := []struct {
		name   string
		config BreakerConfig
		errs   []error // operation outcomes in order; nil is a success
		want   gobreaker.State
	}{
		{
			name:   "stays closed below TripMinRequests",
			config: BreakerConfig{Enabled: true, TripMinRequests: 5, TripFailureRatio: 0.5},
			errs:   slices.Repeat([]error{errServer}, 4),
			want:   gobreaker.StateClosed,
		},
		{
			name:   "trips when the failure ratio is reached",
			config: BreakerConfig{Enabled: true, TripMinRequests: 4, TripFailureRatio: 0.5},
			errs:   []error{nil, errServer, nil, errServer},
			want:   gobreaker.StateOpen,
		},
		{
			name:   "stays closed below the failure ratio",
			config: BreakerConfig{Enabled: true, TripMinRequests: 2, TripFailureRatio: 0.5},
			errs:   []error{nil, nil, nil, errServer, errServer},
			want:   gobreaker.StateClosed,
		},
		{
			name:   "excluded errors count neither as failure nor success",
			config: BreakerConfig{Enabled: true, TripMinRequests: 1, TripFailureRatio: 0.5},
			errs:   slices.Repeat([]error{context.Canceled}, 5),
			want:   gobreaker.StateClosed,
		},
		{
			name:   "defaults trip on total failure",
			config: BreakerConfig{Enabled: true},
			errs:   slices.Repeat([]error{errServer}, 10),
			want:   gobreaker.StateOpen,
		},
		{
			name:   "defaults hold below ten operations",
			config: BreakerConfig{Enabled: true},
			errs:   slices.Repeat([]error{errServer}, 9),
			want:   gobreaker.StateClosed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cb := newBreaker("cache1:11211", tt.config)
			for _, err := range tt.errs {
				_ = breakerExec(cb, err)
			}
			assert.Equal(t, tt.want.String(), cb.State().String())
		})
	}
}

func TestNewBreaker_OnStateChange(t *testing.T) {
	var events []string
	cb := newBreaker("cache1:11211", BreakerConfig{
		Enabled:          true,
		TripMinRequests:  1,
		TripFailureRatio: 1,
		OnStateChange: func(server, from, to string) {
			events = append(events, fmt.Sprintf("%s: %s -> %s", server, from, to))
		},
	})

	require.Error(t, breakerExec(cb, errors.New("server failure")))

	assert.Equal(t, []string{"cache1:11211: closed -> open"}, events)
}

func TestMapBreakerRejection(t *testing.T) {
	assert.ErrorIs(t, mapBreakerRejection(gobreaker.ErrOpenState), ErrBreakerOpen)
	assert.ErrorIs(t, mapBreakerRejection(gobreaker.ErrTooManyRequests), ErrBreakerOpen)

	execErr := errors.New("execution failure")
	assert.Equal(t, execErr, mapBreakerRejection(execErr), "execution errors must pass through unchanged")
}

func TestIsBreakerExcluded(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		excluded bool
	}{
		{"nil", nil, false},
		{"context canceled", context.Canceled, true},
		{"caller deadline exceeded", context.DeadlineExceeded, true},
		{"wrapped caller deadline", &OpError{Op: "mg", Err: context.DeadlineExceeded}, true},
		{"invalid request", &meta.InvalidRequestError{}, true},
		// A socket deadline (Config.Timeout) expiring means the server did not
		// answer in time: that is a server failure and must trip.
		{"socket deadline exceeded", os.ErrDeadlineExceeded, false},
		{"connection refused", net.ErrClosed, false},
		{"generic error", errors.New("boom"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.excluded, isBreakerExcluded(tt.err))
		})
	}
}

func TestClient_WithBreaker(t *testing.T) {
	servers := StaticServers("localhost:11211")

	client := NewClient(servers, Config{
		MaxSize: 1,
		Breaker: BreakerConfig{Enabled: true},
	})
	defer client.Close()

	assert.NotNil(t, client)
}

func TestClient_WithoutBreaker(t *testing.T) {
	servers := StaticServers("localhost:11211")

	client := NewClient(servers, Config{MaxSize: 1})
	defer client.Close()

	assert.NotNil(t, client)
}

func TestPoolMetrics_WithBreaker(t *testing.T) {
	servers := StaticServers("server1:11211", "server2:11211")

	client := NewClient(servers, Config{
		MaxSize: 2,
		Breaker: BreakerConfig{Enabled: true},

		Dialer: &mockDialer{nil, errors.New("dial error")},
	})
	defer client.Close()

	// Trigger pool creation (the operation fails, which is fine here).
	ctx := context.Background()
	_ = client.Set(ctx, Item{Key: "test", Value: []byte("value")})

	metrics := client.PoolMetrics()
	require.NotEmpty(t, metrics)
	for _, m := range metrics {
		assert.Equal(t, "closed", m.Breaker.State)
	}
}
