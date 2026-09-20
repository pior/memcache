package memcache_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"net"
	"os"
	"time"

	"github.com/pior/memcache"
)

// A client with defaults: every Config field is optional. The one worth
// sizing on day one is MaxConnsPerServer, which caps how many operations can
// be in flight to a single server; callers queue once it is full, bounded only
// by their context.
func ExampleNewClient() {
	client := memcache.NewClient(
		memcache.StaticServers("localhost:11211", "localhost:11212"),
		memcache.Config{MaxConnsPerServer: 20},
	)
	defer client.Close()

	_, _ = client.Set(context.Background(), "user:123", []byte("John"))
}

// Circuit breakers are per server and off by default. Enabling them with the
// zero-value policy is the usual choice: trip when 60% of the last 10s failed
// (at least 10 operations observed), shed for 5s, then probe.
//
// Only transport failures count — dial errors, socket errors, and operations
// cut off by Config.OperationTimeout. Misses are normal, and caller-caused
// errors (a canceled context, an expired caller deadline) are excluded so an
// impatient caller cannot trip a healthy server.
//
// That exclusion has a consequence to design for: for the breaker to shed a
// hung server, Config.OperationTimeout must be the binding deadline. If every
// caller passes a context deadline at or below OperationTimeout, the timeouts
// are attributed to the caller, the breaker never opens, and every operation
// keeps paying the full timeout. Give callers a looser budget — or no deadline
// at all, which is capped at OperationTimeout.
func ExampleNewClient_breaker() {
	client := memcache.NewClient(
		memcache.StaticServers("localhost:11211", "localhost:11212"),
		memcache.Config{
			MaxConnsPerServer: 20,
			OperationTimeout:  100 * time.Millisecond,
			Breaker:           memcache.BreakerConfig{Enabled: true},
		},
	)
	defer client.Close()

	// Callers get 500ms, well above OperationTimeout, so a hung server's
	// timeouts are attributed to the server and open its breaker.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_, _ = client.Set(ctx, "user:123", []byte("John"))
}

// Tuning the breaker: every knob is a value on BreakerConfig, so the policy is
// explicit and there is no third-party type in the API.
//
// The trade-off is how fast it reacts against how easily a blip trips it. A
// shorter TripWindow and a lower TripMinRequests react faster but trip on
// noise; a higher TripFailureRatio tolerates partial failure. OpenDuration is
// what a false trip costs: that much fast-failing traffic before the next
// probe.
func ExampleNewClient_breakerTuning() {
	client := memcache.NewClient(
		memcache.StaticServers("localhost:11211", "localhost:11212"),
		memcache.Config{
			MaxConnsPerServer: 20,
			Breaker: memcache.BreakerConfig{
				Enabled:             true,
				TripMinRequests:     20,              // ignore the ratio below this volume
				TripFailureRatio:    0.5,             // trip at 50% failures
				TripWindow:          5 * time.Second, // "recent" means the last 5s
				OpenDuration:        2 * time.Second, // shed for 2s, then probe
				HalfOpenMaxRequests: 3,               // 3 successful probes to close
				OnStateChange: func(server, from, to string) {
					log.Printf("memcache breaker %s: %s -> %s", server, from, to)
				},
			},
		},
	)
	defer client.Close()

	_, _ = client.Set(context.Background(), "user:123", []byte("John"))

	// Breaker state and counts are exported per server, alongside the pool
	// metrics, for dashboards and alerting.
	for _, m := range client.PoolMetrics() {
		fmt.Printf("%s: breaker=%s requests=%d failures=%d conns=%d/%d\n",
			m.Address, m.Breaker.State,
			m.Breaker.Requests, m.Breaker.TotalFailures,
			m.Conns.ActiveConns, m.Conns.TotalConns)
	}
}

// Connecting to TLS-enabled servers (memcached running with --enable-ssl,
// e.g. AWS ElastiCache with in-transit encryption).
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

	client := memcache.NewClient(
		memcache.StaticServers(
			"cache-001.example.com:11211",
			"cache-002.example.com:11211",
		),
		memcache.Config{
			MaxConnsPerServer: 20,
			Dialer: &tls.Dialer{
				Config: &tls.Config{RootCAs: roots},
			},
			// A TLS handshake costs more than a plain dial.
			DialTimeout: 2 * time.Second,
		},
	)
	defer client.Close()

	_, _ = client.Set(context.Background(), "user:123", []byte("John"))
}

// Building a minimal client from the low-level pieces: Commands runs the
// command logic on top of any Executor, here a single unpooled Connection.
// There is no pool, no server selection and no breaker — that is what Client
// adds.
func ExampleNewCommands() {
	conn, err := net.Dial("tcp", "localhost:11211")
	if err != nil {
		panic(err)
	}
	defer conn.Close()

	commands := memcache.NewCommands(memcache.NewConnection(conn, time.Second))

	ctx := context.Background()
	_, _ = commands.Set(ctx, "mykey", []byte("value"))

	item, _ := commands.Get(ctx, "mykey")
	if item.Found {
		fmt.Printf("Value: %s\n", item.Value)
	}
}
