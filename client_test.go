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

func TestClientValidateKey(t *testing.T) {
	tests := []struct {
		name        string
		key         string
		expectError bool
	}{
		{"valid key", "test_key", false},
		{"empty key", "", true},
		{"long key", string(make([]byte, 251)), true},
		{"key with space", "test key", true},
		{"key with newline", "test\nkey", true},
		{"key with tab", "test\tkey", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateKey(tt.key)
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

	// Test that methods return ErrClientClosed after closing
	_, err = client.Get(ctx, "test")
	if err != ErrClientClosed {
		t.Errorf("Get should return ErrClientClosed, got: %v", err)
	}

	item := &Item{Key: "test", Value: []byte("value")}
	err = client.Set(ctx, item)
	if err != ErrClientClosed {
		t.Errorf("Set should return ErrClientClosed, got: %v", err)
	}

	err = client.Delete(ctx, "test")
	if err != ErrClientClosed {
		t.Errorf("Delete should return ErrClientClosed, got: %v", err)
	}

	_, err = client.GetMulti(ctx, []string{"test1", "test2"})
	if err != ErrClientClosed {
		t.Errorf("GetMulti should return ErrClientClosed, got: %v", err)
	}

	err = client.SetMulti(ctx, []*Item{item})
	if err != ErrClientClosed {
		t.Errorf("SetMulti should return ErrClientClosed, got: %v", err)
	}

	err = client.DeleteMulti(ctx, []string{"test1", "test2"})
	if err != ErrClientClosed {
		t.Errorf("DeleteMulti should return ErrClientClosed, got: %v", err)
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

func TestClientMultiEmptySlices(t *testing.T) {
	client, err := NewClient(nil)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	ctx := context.Background()

	// Test empty slices
	results, err := client.GetMulti(ctx, []string{})
	if err != nil {
		t.Errorf("GetMulti with empty slice failed: %v", err)
	}
	if len(results) != 0 {
		t.Error("GetMulti with empty slice should return empty map")
	}

	err = client.SetMulti(ctx, []*Item{})
	if err != nil {
		t.Errorf("SetMulti with empty slice failed: %v", err)
	}

	err = client.DeleteMulti(ctx, []string{})
	if err != nil {
		t.Errorf("DeleteMulti with empty slice failed: %v", err)
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
