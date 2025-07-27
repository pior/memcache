package memcache

import (
	"context"
	"os"
	"testing"
	"time"
)

// Simple test to verify the heavy test infrastructure works
func TestHeavy_Simple(t *testing.T) {
	if os.Getenv("SKIP_INTEGRATION") != "" {
		t.Skip("SKIP_INTEGRATION is set")
	}

	client, err := NewClient(&ClientConfig{
		Servers: []string{"localhost:11211"},
	})
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	ctx := context.Background()
	key := "simple-test-key"
	value := []byte("simple-test-value")

	// Set
	setCmd := NewSetCommand(key, value, time.Minute)
	responses, err := client.Do(ctx, setCmd)
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}
	if responses[0].Error != nil {
		t.Fatalf("Set error: %v", responses[0].Error)
	}

	// Get
	getCmd := NewGetCommand(key)
	responses, err = client.Do(ctx, getCmd)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if responses[0].Error != nil {
		t.Fatalf("Get error: %v", responses[0].Error)
	}

	if string(responses[0].Value) != string(value) {
		t.Errorf("Value mismatch: expected %q, got %q", string(value), string(responses[0].Value))
	}

	// Increment
	counterKey := "simple-counter"
	setCmd = NewSetCommand(counterKey, []byte("0"), time.Minute)
	client.Do(ctx, setCmd)

	incrCmd := NewIncrementCommand(counterKey, 1)
	responses, err = client.Do(ctx, incrCmd)
	if err != nil {
		t.Fatalf("Increment failed: %v", err)
	}
	if responses[0].Error != nil {
		t.Fatalf("Increment error: %v", responses[0].Error)
	}

	// Delete
	deleteCmd := NewDeleteCommand(key)
	responses, err = client.Do(ctx, deleteCmd)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if responses[0].Error != nil {
		t.Fatalf("Delete error: %v", responses[0].Error)
	}

	t.Logf("Simple heavy test passed!")
}
