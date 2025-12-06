package memcache

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/pior/memcache/meta"
	"github.com/sony/gobreaker/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCircuitBreaker(t *testing.T) {
	settings := gobreaker.Settings{
		Name:        "test",
		MaxRequests: 1,
		Interval:    time.Second,
		Timeout:     time.Second,
	}

	cb := gobreaker.NewCircuitBreaker[*meta.Response](settings)
	require.NotNil(t, cb)

	// Should start in closed state
	assert.Equal(t, gobreaker.StateClosed, cb.State())
}

func TestCircuitBreaker_Execute_Success(t *testing.T) {
	settings := gobreaker.Settings{
		Name:    "test",
		Timeout: time.Second,
	}

	cb := gobreaker.NewCircuitBreaker[*meta.Response](settings)

	result, err := cb.Execute(func() (*meta.Response, error) {
		return &meta.Response{Status: meta.StatusHD}, nil
	})

	require.NoError(t, err)
	assert.Equal(t, meta.StatusHD, result.Status)
	assert.Equal(t, gobreaker.StateClosed, cb.State())
}

func TestCircuitBreaker_Execute_Failure(t *testing.T) {
	settings := gobreaker.Settings{
		Name:    "test",
		Timeout: time.Second,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures > 2
		},
	}

	cb := gobreaker.NewCircuitBreaker[*meta.Response](settings)

	// First few failures should keep circuit closed
	for range 2 {
		_, err := cb.Execute(func() (*meta.Response, error) {
			return nil, fmt.Errorf("failure")
		})
		require.Error(t, err)
		assert.Equal(t, gobreaker.StateClosed, cb.State())
	}

	// Third failure should open the circuit
	_, err := cb.Execute(func() (*meta.Response, error) {
		return nil, fmt.Errorf("failure")
	})
	require.Error(t, err)
	assert.Equal(t, gobreaker.StateOpen, cb.State())
}

func TestClient_WithCircuitBreaker(t *testing.T) {
	// Test that client can be created with circuit breaker config
	servers := NewStaticServers("localhost:11211")

	client, err := NewClient(servers, Config{
		MaxSize: 1,
		CircuitBreakerSettings: &gobreaker.Settings{
			MaxRequests: 3,
			Interval:    time.Minute,
			Timeout:     10 * time.Second,
		},
	})
	require.NoError(t, err)
	defer client.Close()

	// Verify client was created successfully
	assert.NotNil(t, client)
}

func TestClient_WithoutCircuitBreaker(t *testing.T) {
	// Test that client works without circuit breaker (nil config)
	servers := NewStaticServers("localhost:11211")

	client, err := NewClient(servers, Config{
		MaxSize:                1,
		CircuitBreakerSettings: nil, // No circuit breaker
	})
	require.NoError(t, err)
	defer client.Close()

	// Verify client was created successfully
	assert.NotNil(t, client)
}

func TestCircuitBreakerState_String(t *testing.T) {
	tests := []struct {
		state    gobreaker.State
		expected string
	}{
		{gobreaker.StateClosed, "closed"},
		{gobreaker.StateHalfOpen, "half-open"},
		{gobreaker.StateOpen, "open"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.state.String())
		})
	}
}

func TestAllPoolStats_WithCircuitBreaker(t *testing.T) {
	servers := NewStaticServers("server1:11211", "server2:11211")

	client, err := NewClient(servers, Config{
		MaxSize: 2,
		CircuitBreakerSettings: &gobreaker.Settings{
			Timeout: time.Second,
		},

		Dialer: &mockDialer{nil, errors.New("dial error")},
	})
	require.NoError(t, err)
	defer client.Close()

	// Try to trigger pool creation (will fail but that's ok for this test)
	ctx := context.Background()
	_ = client.Set(ctx, Item{Key: "test", Value: []byte("value")})

	// Check stats - circuit breaker state should be included
	stats := client.AllPoolStats()
	for _, s := range stats {
		// Circuit breaker state should be set (default is closed)
		assert.Equal(t, gobreaker.StateClosed, s.CircuitBreakerState)
	}
}
