package memcache

import (
	"context"
	"testing"
	"time"
)

func TestCommandChannelSignaling(t *testing.T) {
	t.Run("ResponseAvailableAfterSetResponse", func(t *testing.T) {
		cmd := NewGetCommand("test_key")

		// Start a goroutine that will set the response after a delay
		go func() {
			time.Sleep(10 * time.Millisecond)
			resp := &Response{
				Status: "VA",
				Key:    "test_key",
				Value:  []byte("test_value"),
			}
			cmd.setResponse(resp)
		}()

		// GetResponse should block until the response is available
		ctx := context.Background()
		start := time.Now()
		resp, err := cmd.GetResponse(ctx)
		elapsed := time.Since(start)

		if err != nil {
			t.Fatalf("GetResponse() error = %v", err)
		}
		if resp == nil {
			t.Fatal("GetResponse() returned nil response")
		}
		if resp.Status != "VA" {
			t.Errorf("Expected status VA, got %s", resp.Status)
		}
		if elapsed < 5*time.Millisecond {
			t.Errorf("GetResponse() returned too quickly, expected to wait at least 5ms, got %v", elapsed)
		}
	})

	t.Run("ContextCancellation", func(t *testing.T) {
		cmd := NewGetCommand("test_key")

		// Create a context that will be cancelled after 5ms
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
		defer cancel()

		// GetResponse should return context error
		start := time.Now()
		resp, err := cmd.GetResponse(ctx)
		elapsed := time.Since(start)

		if err == nil {
			t.Fatal("Expected context cancellation error, got nil")
		}
		if err != context.DeadlineExceeded {
			t.Errorf("Expected context.DeadlineExceeded, got %v", err)
		}
		if resp != nil {
			t.Error("Expected nil response on context cancellation")
		}
		if elapsed < 4*time.Millisecond || elapsed > 10*time.Millisecond {
			t.Errorf("Expected cancellation after ~5ms, got %v", elapsed)
		}
	})

	t.Run("MultipleGetResponseCalls", func(t *testing.T) {
		cmd := NewGetCommand("test_key")
		resp := &Response{
			Status: "VA",
			Key:    "test_key",
			Value:  []byte("test_value"),
		}
		cmd.setResponse(resp)

		ctx := context.Background()

		// First call should succeed
		resp1, err1 := cmd.GetResponse(ctx)
		if err1 != nil {
			t.Fatalf("First GetResponse() error = %v", err1)
		}
		if resp1 != resp {
			t.Error("First GetResponse() returned different response")
		}

		// Second call should also succeed (channel remains closed)
		resp2, err2 := cmd.GetResponse(ctx)
		if err2 != nil {
			t.Fatalf("Second GetResponse() error = %v", err2)
		}
		if resp2 != resp {
			t.Error("Second GetResponse() returned different response")
		}
	})
}
