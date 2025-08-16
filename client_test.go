package memcache

import (
	"context"
	"testing"
	"time"

	"github.com/pior/memcache/protocol"
)

func TestClient(t *testing.T) {
	config := &ClientConfig{
		Servers: GetMemcacheServers(),
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

	t.Run("no servers available", func(t *testing.T) {
		config := &ClientConfig{
			Servers: []string{},
		}

		_, err := NewClient(config)
		assertErrorIs(t, err, ErrNoServersSpecified)
	})
}

func TestClientDo(t *testing.T) {
	client := createTestingClient(t, &ClientConfig{Servers: GetMemcacheServers()})

	ctx := context.Background()

	t.Run("no commands", func(t *testing.T) {
		err := client.Do(ctx)
		assertNoError(t, err)
	})

	t.Run("multi commands", func(t *testing.T) {
		commands := []*protocol.Command{
			NewGetCommand("key1"),
			NewSetCommand("key2", []byte("value2"), time.Minute),
			NewDeleteCommand("key3"),
		}
		setOpaqueFromKey(commands...)

		err := client.DoWait(ctx, commands...)
		assertNoError(t, err)

		// Check responses match commands
		for i, cmd := range commands {
			if cmd.Response.Opaque != cmd.Opaque {
				t.Errorf("Response %d: expected opaque %s, got %s", i, cmd.Opaque, cmd.Response.Opaque)
			}
		}
	})
}

func TestClientValidateCommand(t *testing.T) {
	client := createTestingClient(t, &ClientConfig{Servers: GetMemcacheServers()})

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
	client, err := NewClient(&ClientConfig{Servers: GetMemcacheServers()})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	client.Close()

	ctx := context.Background()
	cmd := NewGetCommand("test")

	err = client.Do(ctx, cmd)
	assertErrorIs(t, err, ErrClientClosed)

	err = client.Ping(ctx)
	assertErrorIs(t, err, ErrClientClosed)

	_ = client.Stats()
}

func TestWaitAll(t *testing.T) {
	ctx := context.Background()

	t.Run("empty commands", func(t *testing.T) {
		err := WaitAll(ctx)
		assertNoError(t, err)
	})

	t.Run("nil command", func(t *testing.T) {
		err := WaitAll(ctx, nil)
		assertNoError(t, err)
	})

	t.Run("context cancellation", func(t *testing.T) {
		cmd := NewGetCommand("test_key")

		// Create a context that will be cancelled
		cancelCtx, cancel := context.WithCancel(ctx)
		cancel() // Cancel immediately

		err := WaitAll(cancelCtx, cmd)
		assertErrorIs(t, err, context.Canceled)
	})
}
