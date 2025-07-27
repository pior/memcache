package memcache

import (
	"context"
	"testing"
	"time"
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
	responses, err := client.Do(ctx)
	if err != nil {
		t.Errorf("Do with no commands failed: %v", err)
	}
	if len(responses) != 0 {
		t.Error("Do with no commands should return empty slice")
	}

	// Test single get command
	getCmd := NewGetCommand("test_key")
	responses, err = client.Do(ctx, getCmd)
	if err != nil {
		t.Errorf("Do with get command failed: %v", err)
	}
	if len(responses) != 1 {
		t.Errorf("Expected 1 response, got %d", len(responses))
	}
	if responses[0].Key != "test_key" {
		t.Errorf("Expected key test_key, got %s", responses[0].Key)
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
	commands := []*Command{
		NewGetCommand("key1"),
		NewSetCommand("key2", []byte("value2"), time.Minute),
		NewDeleteCommand("key3"),
	}

	responses, err := client.Do(ctx, commands...)
	if err != nil {
		t.Errorf("Do with multiple commands failed: %v", err)
	}
	if len(responses) != 3 {
		t.Errorf("Expected 3 responses, got %d", len(responses))
	}

	// Check response keys match command keys
	expectedKeys := []string{"key1", "key2", "key3"}
	for i, resp := range responses {
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
		cmd         *Command
		expectError bool
	}{
		{"nil command", nil, true},
		{"empty key", &Command{Type: "mg", Key: ""}, true},
		{"invalid key", &Command{Type: "mg", Key: "key with space"}, true},
		{"valid get", NewGetCommand("valid_key"), false},
		{"valid set", NewSetCommand("valid_key", []byte("value"), 0), false},
		{"set without value", &Command{Type: "ms", Key: "key", Value: nil}, true},
		{"unsupported type", &Command{Type: "unknown", Key: "key"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.Do(ctx, tt.cmd)
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
	_, err = client.Do(ctx, cmd)
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
