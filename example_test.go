package memcache_test

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/pior/memcache"
)

// Example demonstrates the new API where responses are stored in Command objects
func Example_newAPI() {
	// Create a client
	client, err := memcache.NewClient(&memcache.ClientConfig{
		Servers: []string{"localhost:11211"},
	})
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	ctx := context.Background()

	// Method 1: Using the convenience methods
	err = client.Set(ctx, "key1", []byte("value1"), time.Hour)
	if err != nil {
		log.Printf("Set failed: %v", err)
		return
	}

	resp, err := client.Get(ctx, "key1")
	if err != nil {
		log.Printf("Get failed: %v", err)
		return
	}
	fmt.Printf("Got value: %s\n", string(resp.Value))

	// Method 2: Using commands directly for batch operations
	cmd1 := memcache.NewGetCommand("key1")
	cmd2 := memcache.NewGetCommand("key2")
	cmd3 := memcache.NewSetCommand("key3", []byte("value3"), time.Hour)

	// Execute all commands in a single call
	err = client.Do(ctx, cmd1, cmd2, cmd3)
	if err != nil {
		log.Printf("Do failed: %v", err)
		return
	}

	// Get responses from each command
	resp1, err := cmd1.GetResponse(ctx)
	if err != nil {
		log.Printf("Get response for cmd1 failed: %v", err)
		return
	}

	resp2, err := cmd2.GetResponse(ctx)
	if err != nil {
		log.Printf("Get response for cmd2 failed: %v", err)
		return
	}

	resp3, err := cmd3.GetResponse(ctx)
	if err != nil {
		log.Printf("Get response for cmd3 failed: %v", err)
		return
	}

	fmt.Printf("Command 1 status: %s\n", resp1.Status)
	fmt.Printf("Command 2 status: %s\n", resp2.Status)
	fmt.Printf("Command 3 status: %s\n", resp3.Status)

	// Output:
	// Got value: value1
	// Command 1 status: VA
	// Command 2 status: VA
	// Command 3 status: HD
}
