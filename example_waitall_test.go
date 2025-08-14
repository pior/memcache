package memcache

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/pior/memcache/protocol"
)

func ExampleWaitAll() {
	// Create a client (this example assumes memcached is running on localhost:11211)
	client, err := NewClient(&ClientConfig{
		Servers: []string{"localhost:11211"},
		PoolConfig: &PoolConfig{
			MinConnections: 2,
			MaxConnections: 10,
			ConnTimeout:    5 * time.Second,
			IdleTimeout:    time.Minute,
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	ctx := context.Background()

	// Create multiple commands
	commands := []*protocol.Command{
		NewSetCommand("key1", []byte("value1"), time.Hour),
		NewSetCommand("key2", []byte("value2"), time.Hour),
		NewSetCommand("key3", []byte("value3"), time.Hour),
	}

	// Execute all commands asynchronously
	err = client.Do(ctx, commands...)
	if err != nil {
		log.Fatal(err)
	}

	// Wait for all responses to be ready
	err = WaitAll(ctx, commands...)
	if err != nil {
		log.Fatal(err)
	}

	// Now all responses are guaranteed to be ready
	for i, cmd := range commands {
		resp, err := cmd.GetResponse(ctx)
		if err != nil {
			log.Printf("Command %d failed: %v", i, err)
			continue
		}
		fmt.Printf("Command %d completed: key=%s, status=%s\n", i, resp.Key, resp.Status)
	}

	// Output (example):
	// Command 0 completed: key=key1, status=HD
	// Command 1 completed: key=key2, status=HD
	// Command 2 completed: key=key3, status=HD
}

func ExampleWaitAll_withTimeout() {
	client, err := NewClient(nil)
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	// Create a context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Create commands
	commands := []*protocol.Command{
		NewGetCommand("key1"),
		NewGetCommand("key2"),
		NewGetCommand("key3"),
	}

	// Execute commands
	err = client.Do(ctx, commands...)
	if err != nil {
		log.Fatal(err)
	}

	// Wait for all responses with timeout
	err = WaitAll(ctx, commands...)
	if err != nil {
		if err == context.DeadlineExceeded {
			fmt.Println("Timeout waiting for responses")
		} else {
			log.Fatal(err)
		}
		return
	}

	fmt.Println("All responses ready within timeout")

	// Output:
	// All responses ready within timeout
}
