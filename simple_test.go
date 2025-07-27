package memcache

import (
	"context"
	"testing"
	"time"
)

func TestSimpleMetaFlags(t *testing.T) {
	skipIfNoMemcached(t)

	client, err := NewClient(&ClientConfig{
		Servers: []string{"localhost:11211"},
		PoolConfig: &PoolConfig{
			MinConnections: 1,
			MaxConnections: 2,
			ConnTimeout:    2 * time.Second,
			IdleTimeout:    time.Minute,
		},
	})
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	key := "simple_test"
	value := []byte("hello")

	// Set
	setCmd := NewSetCommand(key, value, time.Hour)
	_, err = client.Do(ctx, setCmd)
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Test combined value+size flags step by step
	getCmd := &Command{
		Type: CmdMetaGet,
		Key:  key,
		Flags: map[string]string{
			FlagValue: "", // "v"
			FlagSize:  "", // "s"
		},
	}

	t.Logf("Attempting get with flags: %+v", getCmd.Flags)

	responses, err := client.Do(ctx, getCmd)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	t.Logf("Get response: %+v", responses[0])

	// Clean up
	client.Do(ctx, NewDeleteCommand(key))
}
