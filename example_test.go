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
		Servers: memcache.GetMemcacheServers(),
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

	_ = memcache.WaitAll(ctx, cmd1, cmd2, cmd3)

	if cmd1.Response.Error != nil {
		log.Printf("Get response for cmd1 failed: %v", cmd1.Response.Error)
		return
	}

	if cmd2.Response.Error != nil {
		log.Printf("Get response for cmd2 failed: %v", cmd2.Response.Error)
		return
	}

	if cmd3.Response.Error != nil {
		log.Printf("Get response for cmd3 failed: %v", cmd3.Response.Error)
		return
	}

	fmt.Printf("Command 1 status: %s\n", cmd1.Response.Status)
	fmt.Printf("Command 2 status: %s\n", cmd2.Response.Status)
	fmt.Printf("Command 3 status: %s\n", cmd3.Response.Status)

	// Output:
	// Got value: value1
	// Command 1 status: VA
	// Command 2 status: VA
	// Command 3 status: HD
}
