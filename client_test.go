package memcache

import (
	"context"
	"testing"
	"time"

	"github.com/pior/memcache/protocol"
)

func TestNewClient(t *testing.T) {
	config := &ClientConfig{
		Servers: []string{"localhost:11211"},
		PoolConfig: &PoolConfig{
			MinConnections: 1,
			MaxConnections: 5,
			ConnTimeout:    time.Second,
			IdleTimeout:    time.Minute,
		},
		HashRing: &HashRingConfig{
			VirtualNodes: 100,
		},
	}

	client, err := NewClient(config)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	if client.closed {
		t.Error("client should not be closed initially")
	}
}

func TestNewClientWithDefaults(t *testing.T) {
	client, err := NewClient(nil)
	if err != nil {
		t.Fatalf("NewClient with defaults failed: %v", err)
	}
	defer client.Close()

	if client.closed {
		t.Error("client should not be closed initially")
	}
}

func TestNewClientNoServers(t *testing.T) {
	config := &ClientConfig{
		Servers: []string{},
	}

	_, err := NewClient(config)
	if err == nil {
		t.Error("NewClient should fail with no servers")
	}
}

func TestClientDo(t *testing.T) {
	client, err := NewClient(nil)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	ctx := context.Background()

	// Test empty commands
	err = client.Do(ctx)
	if err != nil {
		t.Errorf("Do with no commands failed: %v", err)
	}

	// Test single get command
	getCmd := NewGetCommand("test_key")
	err = client.Do(ctx, getCmd)
	if err != nil {
		t.Errorf("Do with get command failed: %v", err)
	}

	response, err := getCmd.GetResponse(ctx)
	if err != nil {
		t.Errorf("GetResponse failed: %v", err)
	}
	if response.Key != "test_key" {
		t.Errorf("Expected key test_key, got %s", response.Key)
	}
}

func TestClientDoMultipleCommands(t *testing.T) {
	client, err := NewClient(nil)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	ctx := context.Background()

	// Test multiple commands
	commands := []*protocol.Command{
		NewGetCommand("key1"),
		NewSetCommand("key2", []byte("value2"), time.Minute),
		NewDeleteCommand("key3"),
	}

	err = client.Do(ctx, commands...)
	if err != nil {
		t.Errorf("Do with multiple commands failed: %v", err)
	}

	// Check response keys match command keys
	expectedKeys := []string{"key1", "key2", "key3"}
	for i, cmd := range commands {
		resp, err := cmd.GetResponse(ctx)
		if err != nil {
			t.Errorf("GetResponse for command %d failed: %v", i, err)
		}
		if resp.Key != expectedKeys[i] {
			t.Errorf("Response %d: expected key %s, got %s", i, expectedKeys[i], resp.Key)
		}
	}
}

func TestClientValidateCommand(t *testing.T) {
	client, err := NewClient(nil)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	ctx := context.Background()

	tests := []struct {
		name        string
		cmd         *protocol.Command
		expectError bool
	}{
		// invalid
		{"nil command", nil, true},
		{"empty key", NewGetCommand(""), true},
		{"invalid key", NewGetCommand("key with space"), true},
		{"set without value", NewSetCommand("key", nil, 0), true},
		{"unsupported type", &protocol.Command{Type: "unknown", Key: "key"}, true},
		// valid
		{"valid get", NewGetCommand("valid_key"), false},
		{"valid set", NewSetCommand("valid_key", []byte("value"), 0), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := client.Do(ctx, tt.cmd)
			if tt.expectError && err == nil {
				t.Error("expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestClientClosed(t *testing.T) {
	client, err := NewClient(nil)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	client.Close()

	ctx := context.Background()
	cmd := NewGetCommand("test")

	// Test that Do returns ErrClientClosed after closing
	err = client.Do(ctx, cmd)
	if err != ErrClientClosed {
		t.Errorf("Do should return ErrClientClosed, got: %v", err)
	}

	err = client.Ping(ctx)
	if err != ErrClientClosed {
		t.Errorf("Ping should return ErrClientClosed, got: %v", err)
	}

	stats := client.Stats()
	if stats != nil {
		t.Error("Stats should return nil for closed client")
	}
}

func TestDefaultClientConfig(t *testing.T) {
	config := DefaultClientConfig()

	if len(config.Servers) == 0 {
		t.Error("default config should have at least one server")
	}

	if config.PoolConfig == nil {
		t.Error("default config should have pool config")
	}

	if config.HashRing == nil {
		t.Error("default config should have hash ring config")
	}

	if config.HashRing.VirtualNodes <= 0 {
		t.Error("default hash ring should have positive virtual nodes")
	}
}

func TestDefaultHashRingConfig(t *testing.T) {
	config := DefaultClientConfig()

	if config.HashRing.VirtualNodes != 160 {
		t.Errorf("expected 160 virtual nodes, got %d", config.HashRing.VirtualNodes)
	}
}

func TestWaitAll(t *testing.T) {
	client, err := NewClient(nil)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	ctx := context.Background()

	t.Run("empty commands", func(t *testing.T) {
		err := WaitAll(ctx)
		if err != nil {
			t.Errorf("WaitAll with no commands should not error: %v", err)
		}
	})

	t.Run("nil command", func(t *testing.T) {
		err := WaitAll(ctx, nil)
		if err == nil {
			t.Error("WaitAll with nil command should return error")
		}
	})

	t.Run("single command", func(t *testing.T) {
		cmd := NewGetCommand("test_key")

		// Execute the command first
		err := client.Do(ctx, cmd)
		if err != nil {
			t.Fatalf("Do failed: %v", err)
		}

		// Wait for response to be ready
		err = WaitAll(ctx, cmd)
		if err != nil {
			t.Errorf("WaitAll failed: %v", err)
		}

		// Should be able to get response immediately
		resp, err := cmd.GetResponse(ctx)
		if err != nil {
			t.Errorf("GetResponse failed after WaitAll: %v", err)
		}
		if resp.Key != "test_key" {
			t.Errorf("Expected key test_key, got %s", resp.Key)
		}
	})

	t.Run("multiple commands", func(t *testing.T) {
		commands := []*protocol.Command{
			NewGetCommand("key1"),
			NewSetCommand("key2", []byte("value2"), time.Minute),
			NewDeleteCommand("key3"),
		}

		// Execute all commands
		err := client.Do(ctx, commands...)
		if err != nil {
			t.Fatalf("Do with multiple commands failed: %v", err)
		}

		// Wait for all responses to be ready
		err = WaitAll(ctx, commands...)
		if err != nil {
			t.Errorf("WaitAll with multiple commands failed: %v", err)
		}

		// All responses should be available immediately
		for i, cmd := range commands {
			resp, err := cmd.GetResponse(ctx)
			if err != nil {
				t.Errorf("GetResponse for command %d failed after WaitAll: %v", i, err)
			}
			expectedKeys := []string{"key1", "key2", "key3"}
			if resp.Key != expectedKeys[i] {
				t.Errorf("Command %d: expected key %s, got %s", i, expectedKeys[i], resp.Key)
			}
		}
	})

	t.Run("context cancellation", func(t *testing.T) {
		cmd := NewGetCommand("test_key")

		// Create a context that will be cancelled
		cancelCtx, cancel := context.WithCancel(ctx)
		cancel() // Cancel immediately

		// WaitAll should return context error
		err := WaitAll(cancelCtx, cmd)
		if err == nil {
			t.Error("WaitAll should return error when context is cancelled")
		}
		if err != context.Canceled {
			t.Errorf("Expected context.Canceled, got: %v", err)
		}
	})

	t.Run("timeout context", func(t *testing.T) {
		cmd := NewGetCommand("test_key")

		// Create a context with very short timeout
		timeoutCtx, cancel := context.WithTimeout(ctx, 1*time.Nanosecond)
		defer cancel()

		// Give time for context to expire
		time.Sleep(10 * time.Millisecond)

		// WaitAll should return timeout error
		err := WaitAll(timeoutCtx, cmd)
		if err == nil {
			t.Error("WaitAll should return error when context times out")
		}
		if err != context.DeadlineExceeded {
			t.Errorf("Expected context.DeadlineExceeded, got: %v", err)
		}
	})
}
