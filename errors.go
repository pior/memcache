package memcache

import "errors"

// Sentinel errors returned by the client. Check them with errors.Is; they may
// be wrapped with additional context.
var (
	// ErrNotStored is returned when a conditional store is not applied:
	// Add on an existing key, or replace/append/prepend on a missing key.
	ErrNotStored = errors.New("memcache: item not stored")

	// ErrClientClosed is returned by operations issued after Client.Close.
	ErrClientClosed = errors.New("memcache: client is closed")

	// ErrNoServers is returned when the client has no server to talk to.
	ErrNoServers = errors.New("memcache: no servers available")

	// ErrPoolClosed is returned by Pool.Acquire after the pool has been closed.
	ErrPoolClosed = errors.New("memcache: pool is closed")
)
