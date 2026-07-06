package main

import (
	"testing"
	"time"
)

func TestBreakerConfig(t *testing.T) {
	config := breakerConfig(7, 3*time.Second)

	if !config.Enabled {
		t.Fatal("breaker is disabled")
	}
	if config.TripMinRequests != 7 {
		t.Fatalf("TripMinRequests = %d, want 7", config.TripMinRequests)
	}
	if config.TripFailureRatio != 1 {
		t.Fatalf("TripFailureRatio = %v, want 1", config.TripFailureRatio)
	}
	if config.OpenDuration != 3*time.Second {
		t.Fatalf("OpenDuration = %v, want 3s", config.OpenDuration)
	}
}
