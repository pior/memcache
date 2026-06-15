// Package memcache is a modern memcache client for Go implementing the
// memcached meta protocol (version 1.6+).
//
// The high-level [Client] is the recommended entry point. It provides
// multi-server support with consistent key distribution, per-server circuit
// breakers, and connection pooling with health checks:
//
//	servers := memcache.StaticServers("localhost:11211", "localhost:11212")
//	client := memcache.NewClient(servers, memcache.Config{
//		MaxSize: 10,
//		Timeout: 500 * time.Millisecond,
//	})
//	defer client.Close()
//
//	_ = client.Set(ctx, memcache.Item{Key: "mykey", Value: []byte("hello")})
//	item, _ := client.Get(ctx, "mykey")
//
// # Building Blocks
//
// The client is assembled from smaller pieces that can be used on their own to
// build a custom client:
//
//   - The meta package serializes requests and parses responses for the
//     memcached meta protocol.
//   - [Connection] wraps a single net.Conn and implements [Executor].
//   - [Commands] and [BatchCommands] hold the command logic (Get, Set, Delete,
//     Increment, …) on top of any [Executor].
//   - [Pool] is a pluggable connection pool interface, with puddle-based
//     ([NewPuddlePool]) and channel-based ([NewChannelPool]) implementations.
package memcache
