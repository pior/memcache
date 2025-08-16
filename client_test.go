package memcache

import (
	"context"
	"testing"
	"time"

	"github.com/pior/memcache/protocol"
	"github.com/stretchr/testify/require"
)

func TestClient(t *testing.T) {
	t.Run("no servers available", func(t *testing.T) {
		config := &ClientConfig{}

		_, err := NewClient(nil, config)
		require.ErrorIs(t, err, ErrNoServersSpecified)
	})
}

func TestClientDo(t *testing.T) {
	client := createTestingClient(t, nil)

	ctx := context.Background()

	t.Run("no commands", func(t *testing.T) {
		err := client.Do(ctx)
		require.NoError(t, err)
	})

	t.Run("multi commands", func(t *testing.T) {
		commands := []*protocol.Command{
			NewGetCommand("key1"),
			NewSetCommand("key2", []byte("value2"), time.Minute),
			NewDeleteCommand("key3"),
		}
		setOpaqueFromKey(commands...)

		err := client.DoWait(ctx, commands...)
		require.NoError(t, err)

		// Check responses match commands
		for i, cmd := range commands {
			if cmd.Response.Opaque != cmd.Opaque {
				t.Errorf("Response %d: expected opaque %s, got %s", i, cmd.Opaque, cmd.Response.Opaque)
			}
		}
	})
}

func TestClientValidateCommand(t *testing.T) {
	client := createTestingClient(t, nil)

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
	client, err := NewClient(GetMemcacheServers(), nil)
	require.NoError(t, err)

	err = client.Close()
	require.NoError(t, err)

	ctx := context.Background()
	cmd := NewGetCommand("test")

	err = client.Do(ctx, cmd)
	require.ErrorIs(t, err, ErrClientClosed)

	err = client.Ping(ctx)
	require.ErrorIs(t, err, ErrClientClosed)

	_ = client.Stats()
}

func TestWaitAll(t *testing.T) {
	ctx := context.Background()

	t.Run("empty commands", func(t *testing.T) {
		err := WaitAll(ctx)
		require.NoError(t, err)
	})

	t.Run("nil command", func(t *testing.T) {
		err := WaitAll(ctx, nil)
		require.NoError(t, err)
	})

	t.Run("context cancellation", func(t *testing.T) {
		cmd := NewGetCommand("test_key")

		// Create a context that will be cancelled
		cancelCtx, cancel := context.WithCancel(ctx)
		cancel() // Cancel immediately

		err := WaitAll(cancelCtx, cmd)
		require.ErrorIs(t, err, context.Canceled)
	})
}
