package memcache

import (
	"bufio"
	"context"
	"net"
	"time"

	"github.com/pior/memcache/meta"
)

func NewConnection(conn net.Conn) *Connection {
	return &Connection{
		Conn:   conn,
		Reader: bufio.NewReader(conn),
		Writer: bufio.NewWriter(conn),
	}
}

// Connection wraps a network connection with buffered reader and writer for efficient I/O.
type Connection struct {
	net.Conn
	Reader *bufio.Reader
	Writer *bufio.Writer
}

func (c *Connection) Send(req *meta.Request) (*meta.Response, error) {
	if err := meta.WriteRequest(c.Writer, req); err != nil {
		return nil, err
	}

	resp, err := meta.ReadResponse(c.Reader)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// Resource represents a connection resource from the pool.
type Resource interface {
	// Value returns the underlying connection.
	Value() *Connection

	// Release returns the connection to the pool for reuse.
	Release()

	// ReleaseUnused returns the connection to the pool without marking it as used.
	// Used for health checks that don't actually use the connection.
	ReleaseUnused()

	// Destroy closes the connection and removes it from the pool.
	Destroy()

	// CreationTime returns when the connection was created.
	CreationTime() time.Time

	// IdleDuration returns how long the connection has been idle.
	IdleDuration() time.Duration
}

// Pool manages a pool of connections.
type Pool interface {
	// Acquire gets a connection from the pool, creating one if necessary.
	// Blocks until a connection is available or context is canceled.
	Acquire(ctx context.Context) (Resource, error)

	// AcquireAllIdle acquires all idle connections from the pool.
	// Used for health checks and maintenance.
	AcquireAllIdle() []Resource

	// Close closes the pool and all connections.
	Close()

	// Stats returns a snapshot of pool statistics.
	Stats() PoolStats
}
