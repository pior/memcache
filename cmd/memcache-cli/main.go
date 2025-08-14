package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pior/memcache"
	"github.com/pior/memcache/protocol"
)

func main() {
	fmt.Println("Memcache CLI Tool")
	fmt.Println("================")
	fmt.Println("Commands: get <key>, set <key> <value> [ttl], delete <key>, multi-get <key1> <key2> ..., stats, ping, quit")
	fmt.Println()

	// Create client with default config
	client, err := memcache.NewClient(nil)
	if err != nil {
		fmt.Printf("Failed to create client: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			break
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) == 0 {
			continue
		}

		command := strings.ToLower(parts[0])
		ctx := context.Background()

		switch command {
		case "get":
			if len(parts) != 2 {
				fmt.Println("Usage: get <key>")
				continue
			}
			handleGet(ctx, client, parts[1])

		case "set":
			if len(parts) < 3 || len(parts) > 4 {
				fmt.Println("Usage: set <key> <value> [ttl_seconds]")
				continue
			}
			ttl := time.Duration(0)
			if len(parts) == 4 {
				ttlSecs, err := strconv.Atoi(parts[3])
				if err != nil {
					fmt.Printf("Invalid TTL: %v\n", err)
					continue
				}
				ttl = time.Duration(ttlSecs) * time.Second
			}
			handleSet(ctx, client, parts[1], parts[2], ttl)

		case "delete", "del":
			if len(parts) != 2 {
				fmt.Println("Usage: delete <key>")
				continue
			}
			handleDelete(ctx, client, parts[1])

		case "multi-get", "mget":
			if len(parts) < 2 {
				fmt.Println("Usage: multi-get <key1> <key2> ...")
				continue
			}
			handleMultiGet(ctx, client, parts[1:])

		case "stats":
			handleStats(client)

		case "ping":
			handlePing(ctx, client)

		case "help":
			fmt.Println("Commands:")
			fmt.Println("  get <key>                 - Get a value by key")
			fmt.Println("  set <key> <value> [ttl]   - Set a key-value pair with optional TTL")
			fmt.Println("  delete <key>              - Delete a key")
			fmt.Println("  multi-get <key1> <key2>   - Get multiple keys at once")
			fmt.Println("  stats                     - Show server statistics")
			fmt.Println("  ping                      - Ping all servers")
			fmt.Println("  quit                      - Exit the CLI")

		case "quit", "exit":
			fmt.Println("Goodbye!")
			return

		default:
			fmt.Printf("Unknown command: %s. Type 'help' for available commands.\n", command)
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Printf("Error reading input: %v\n", err)
	}
}

func handleGet(ctx context.Context, client *memcache.Client, key string) {
	start := time.Now()
	cmd := memcache.NewGetCommand(key)
	err := client.Do(ctx, cmd)
	duration := time.Since(start)

	if err != nil {
		fmt.Printf("Error: %v (took %v)\n", err, duration)
		return
	}

	response, err := cmd.GetResponse(ctx)
	if err != nil {
		fmt.Printf("Error getting response: %v (took %v)\n", err, duration)
		return
	}

	if response.Error != nil {
		if response.Error == protocol.ErrCacheMiss {
			fmt.Printf("Key not found (took %v)\n", duration)
		} else {
			fmt.Printf("Error: %v (took %v)\n", response.Error, duration)
		}
		return
	}

	fmt.Printf("Value: %s (took %v)\n", string(response.Value), duration)
	if len(response.Flags) > 0 {
		fmt.Printf("Flags: %v\n", response.Flags)
	}
}

func handleSet(ctx context.Context, client *memcache.Client, key, value string, ttl time.Duration) {
	start := time.Now()
	cmd := memcache.NewSetCommand(key, []byte(value), ttl)
	err := client.Do(ctx, cmd)
	duration := time.Since(start)

	if err != nil {
		fmt.Printf("Error: %v (took %v)\n", err, duration)
		return
	}

	response, err := cmd.GetResponse(ctx)
	if err != nil {
		fmt.Printf("Error getting response: %v (took %v)\n", err, duration)
		return
	}

	if response.Error != nil {
		fmt.Printf("Error: %v (took %v)\n", response.Error, duration)
		return
	}

	fmt.Printf("Stored successfully (took %v)\n", duration)
}

func handleDelete(ctx context.Context, client *memcache.Client, key string) {
	start := time.Now()
	cmd := memcache.NewDeleteCommand(key)
	err := client.Do(ctx, cmd)
	duration := time.Since(start)

	if err != nil {
		fmt.Printf("Error: %v (took %v)\n", err, duration)
		return
	}

	response, err := cmd.GetResponse(ctx)
	if err != nil {
		fmt.Printf("Error getting response: %v (took %v)\n", err, duration)
		return
	}

	if response.Error != nil {
		if response.Error == protocol.ErrCacheMiss {
			fmt.Printf("Key not found (took %v)\n", duration)
		} else {
			fmt.Printf("Error: %v (took %v)\n", response.Error, duration)
		}
		return
	}

	fmt.Printf("Delete successful (took %v)\n", duration)
}

func handleMultiGet(ctx context.Context, client *memcache.Client, keys []string) {
	start := time.Now()

	commands := make([]*protocol.Command, len(keys))
	for i, key := range keys {
		commands[i] = memcache.NewGetCommand(key)
	}

	err := client.Do(ctx, commands...)
	duration := time.Since(start)

	if err != nil {
		fmt.Printf("Error: %v (took %v)\n", err, duration)
		return
	}

	found := 0
	for i, cmd := range commands {
		resp, err := cmd.GetResponse(ctx)
		if err != nil {
			fmt.Printf("  %s: <error getting response: %v>\n", keys[i], err)
			continue
		}

		if resp.Error == nil {
			found++
			fmt.Printf("  %s: %s\n", keys[i], string(resp.Value))
		} else if resp.Error == protocol.ErrCacheMiss {
			fmt.Printf("  %s: <not found>\n", keys[i])
		} else {
			fmt.Printf("  %s: <error: %v>\n", keys[i], resp.Error)
		}
	}

	fmt.Printf("Retrieved %d out of %d keys (took %v)\n", found, len(keys), duration)
}

func handleStats(client *memcache.Client) {
	stats := client.Stats()
	if len(stats) == 0 {
		fmt.Println("No statistics available")
		return
	}

	fmt.Println("Server Statistics:")
	for i, stat := range stats {
		fmt.Printf("Server %d (%s):\n", i+1, stat.Address)
		fmt.Printf("  Total Connections: %d\n", stat.TotalConnections)
		fmt.Printf("  Active Connections: %d\n", stat.ActiveConnections)
		fmt.Printf("  Min Connections: %d\n", stat.MinConnections)
		fmt.Printf("  Max Connections: %d\n", stat.MaxConnections)
		fmt.Printf("  Total In-Flight: %d\n", stat.TotalInFlight)
		fmt.Println()
	}
}

func handlePing(ctx context.Context, client *memcache.Client) {
	start := time.Now()
	err := client.Ping(ctx)
	duration := time.Since(start)

	if err != nil {
		fmt.Printf("Ping failed: %v (took %v)\n", err, duration)
		return
	}

	fmt.Printf("Ping successful (took %v)\n", duration)
}
