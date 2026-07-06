package memcache_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/pior/memcache"
)

// Example demonstrating how to use circuit breakers with the memcache client
func ExampleNewClient() {
	servers := memcache.StaticServers("localhost:11211", "localhost:11212")

	// Create client with a circuit breaker for each server
	client := memcache.NewClient(servers, memcache.Config{
		MaxSize: 10,
		Breaker: memcache.BreakerConfig{
			Enabled:          true,
			TripMinRequests:  10,               // don't trip below this volume
			TripFailureRatio: 0.6,              // trip when 60% of recent operations failed
			TripWindow:       10 * time.Second, // "recent" means the last 10s
			OpenDuration:     5 * time.Second,  // fail fast for 5s, then probe the server
			OnStateChange: func(server, from, to string) {
				fmt.Printf("Circuit breaker %s: %s -> %s\n", server, from, to)
			},
		},
	})
	defer client.Close()

	ctx := context.Background()

	// Perform operations - circuit breaker protects against failing servers
	_ = client.Set(ctx, memcache.Item{Key: "user:123", Value: []byte("John")})

	// Check circuit breaker states
	metrics := client.PoolMetrics()
	for _, m := range metrics {
		fmt.Printf("Server: %s\n", m.Addr)
		fmt.Printf("  Circuit Breaker: %s\n", m.Breaker.State)
		fmt.Printf("  Total Connections: %d\n", m.Conns.TotalConns)
		fmt.Printf("  Active Connections: %d\n", m.Conns.ActiveConns)
	}
}

// Example demonstrating how to build a minimal client from the low-level
// building blocks: Commands runs the command logic on top of any Executor,
// here a single unpooled Connection.
func ExampleNewCommands() {
	conn, err := net.Dial("tcp", "localhost:11211")
	if err != nil {
		panic(err)
	}
	defer conn.Close()

	commands := memcache.NewCommands(memcache.NewConnection(conn, time.Second))

	ctx := context.Background()
	_ = commands.Set(ctx, memcache.Item{Key: "mykey", Value: []byte("value")})

	item, _ := commands.Get(ctx, "mykey")
	if item.Found {
		fmt.Printf("Value: %s\n", item.Value)
	}
}

// Example connecting to TLS-enabled servers (memcached running with
// --enable-ssl, e.g. AWS ElastiCache with in-transit encryption).
//
// A *tls.Dialer satisfies memcache.Dialer, so it plugs straight into Config.
// One dialer covers every server in the set: crypto/tls fills ServerName from
// each dial address, so each server is verified against its own hostname.
func ExampleNewClient_tls() {
	// Trust the CA that signed the servers' certificates. Omit RootCAs to use
	// the system trust store.
	caPEM, err := os.ReadFile("/etc/ssl/memcache-ca.pem")
	if err != nil {
		panic(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(caPEM)

	servers := memcache.StaticServers(
		"cache-001.example.com:11211",
		"cache-002.example.com:11211",
	)

	client := memcache.NewClient(servers, memcache.Config{
		MaxSize: 10,
		Dialer: &tls.Dialer{
			Config: &tls.Config{RootCAs: roots},
		},
	})
	defer client.Close()

	_ = client.Set(context.Background(), memcache.Item{Key: "user:123", Value: []byte("John")})
}
