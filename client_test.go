package memcache

import (
	"context"
	"testing"

	"github.com/pior/memcache/protocol"
	"github.com/stretchr/testify/require"
)

func TestClient(t *testing.T) {
	t.Run("no servers available", func(t *testing.T) {
		_, err := NewClient(nil, nil)
		require.ErrorIs(t, err, ErrNoServersSpecified)
	})
}

func TestClientInvalidCommand(t *testing.T) {
	client, err := NewClient([]string{"localhost:11211"}, nil)
	require.NoError(t, err)

	ctx := context.Background()

	tests := []struct {
		name    string
		cmd     *protocol.Command
		wantErr error
	}{
		{"nil command", nil, ErrMalformedCommand},
		{"empty key", NewGetCommand(""), ErrMalformedKey},
		{"invalid key", NewGetCommand("key with space"), ErrMalformedKey},
		{"set without value", NewSetCommand("key", nil, 0), ErrMalformedCommand},
		{"unsupported type", &protocol.Command{Type: "unknown", Key: "key"}, ErrMalformedCommand},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := client.Execute(ctx, tt.cmd)
			require.ErrorIs(t, err, tt.wantErr)
		})
	}
}

func TestClientClosed(t *testing.T) {
	client, err := NewClient([]string{"localhost:11211"}, nil)
	require.NoError(t, err)

	err = client.Close()
	require.NoError(t, err)

	ctx := context.Background()
	cmd := NewGetCommand("test")

	err = client.Execute(ctx, cmd)
	require.ErrorIs(t, err, ErrClientClosed)

	err = client.Ping(ctx)
	require.ErrorIs(t, err, ErrClientClosed)

	_ = client.Stats()
}
