package memcache

import (
	"context"
	"testing"
	"time"
)

func TestIntegration_DebugSimple(t *testing.T) {
	client := createTestingClient(t, &ClientConfig{
		Servers: []string{"localhost:11211"},
		PoolConfig: &PoolConfig{
			MinConnections: 1,
			MaxConnections: 2,
			ConnTimeout:    5 * time.Second,
			IdleTimeout:    time.Minute,
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Test a simple set operation (no value reading)
	key := "debug_simple_key"
	value := []byte("small_value")

	setCmd := NewSetCommand(key, value, time.Hour)
	t.Logf("Starting set operation...")

	err := client.Do(ctx, setCmd)
	if err != nil {
		t.Fatalf("Set operation failed: %v", err)
	}
	t.Logf("Set operation sent, getting response...")

	setResp, err := setCmd.GetResponse(ctx)
	if err != nil {
		t.Fatalf("Failed to get set response: %v", err)
	}
	if setResp.Error != nil {
		t.Fatalf("Set operation returned error: %v", setResp.Error)
	}
	t.Logf("Set operation completed successfully")

	// Now test a get operation (this will read a value)
	t.Logf("Starting get operation...")
	getCmd := NewGetCommand(key)
	err = client.Do(ctx, getCmd)
	if err != nil {
		t.Fatalf("Get operation failed: %v", err)
	}
	t.Logf("Get operation sent, getting response...")

	getResp, err := getCmd.GetResponse(ctx)
	if err != nil {
		t.Fatalf("Failed to get response: %v", err)
	}
	if getResp.Error != nil {
		t.Fatalf("Get operation returned error: %v", getResp.Error)
	}
	t.Logf("Get operation completed successfully, value: %s", string(getResp.Value))
}
