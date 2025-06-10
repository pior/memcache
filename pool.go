package memcache

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"syscall"
	"time"
)

// DialContextFunc is a function that can dial a network connection.
// It's compatible with net.Dialer.DialContext.
type DialContextFunc func(ctx context.Context, network, address string) (net.Conn, error)

// PoolConfig holds configuration options for the connection pool.
type PoolConfig struct {
	DialTimeout  time.Duration
	DialFunc     DialContextFunc
	MaxIdleConns int
	IdleTimeout  time.Duration
}

// pooledConn is an internal representation of a connection managed by the Pool.
type pooledConn struct {
	nc       net.Conn  // The underlying network connection
	pool     *Pool     // The pool this connection belongs to
	lastUsed time.Time // When the connection was last returned to the pool
}

// close closes the underlying network connection.
func (pc *pooledConn) close() error {
	if pc.nc != nil {
		return pc.nc.Close()
	}
	return nil
}

// Release handles the conditional release of the connection back to the pool or closes it.
// The error argument is the error that occurred during the operation using this connection.
func (pc *pooledConn) Release(err error) {
	if pc.pool == nil { // Should not happen if properly managed
		if pc.nc != nil {
			pc.nc.Close()
		}
		return
	}

	if err == nil {
		pc.pool.putFreeConn(pc)
		return
	}

	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		pc.pool.putFreeConn(pc)
		return
	}

	switch {
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.EPIPE):
		pc.close() // Close the connection
		return
	}
	pc.pool.putFreeConn(pc) // Release for other errors
}

// Pool manages a pool of network connections for a single server address.
type Pool struct {
	address  string // The server address (e.g., "host:port")
	config   PoolConfig
	mu       sync.Mutex
	freeconn []*pooledConn // List of free connections for the single address
}

// NewPool creates a new connection pool for a given server address and configuration.
func NewPool(address string, config PoolConfig) (*Pool, error) {
	if config.DialTimeout == 0 {
		config.DialTimeout = 5 * time.Second
	}
	if config.MaxIdleConns <= 0 {
		config.MaxIdleConns = 2 // Default
	}
	if config.DialFunc == nil {
		var d net.Dialer
		config.DialFunc = d.DialContext
	}

	p := &Pool{
		address:  address,
		config:   config,
		freeconn: make([]*pooledConn, 0, config.MaxIdleConns),
	}
	return p, nil
}

// Get retrieves or creates a new connection from the pool.
// The caller is responsible for setting the deadline on the obtained net.Conn.
func (p *Pool) Get() (*pooledConn, error) {
	p.mu.Lock()
	// Check for an existing free connection
	if len(p.freeconn) > 0 {
		// Try to find a non-stale connection if IdleTimeout is set
		if p.config.IdleTimeout > 0 {
			var validConns []*pooledConn
			now := time.Now()
			cleaned := false
			for i := 0; i < len(p.freeconn); i++ {
				cn := p.freeconn[i]
				if now.Sub(cn.lastUsed) > p.config.IdleTimeout {
					cn.close() // Close stale connection
					cleaned = true
				} else {
					validConns = append(validConns, cn)
				}
			}
			if cleaned {
				p.freeconn = validConns
			}
		}

		if len(p.freeconn) > 0 {
			// Get the most recently added (end of list)
			cn := p.freeconn[len(p.freeconn)-1]
			p.freeconn = p.freeconn[:len(p.freeconn)-1]
			p.mu.Unlock()
			return cn, nil
		}
	}
	p.mu.Unlock() // Unlock before dialing if no free connections were found or all were stale

	// Dial a new connection
	netConn, err := p.dial()
	if err != nil {
		return nil, err
	}

	cn := &pooledConn{
		nc:   netConn,
		pool: p,
	}
	return cn, nil
}

// putFreeConn adds a connection to the free list.
func (p *Pool) putFreeConn(cn *pooledConn) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.freeconn) >= p.config.MaxIdleConns {
		cn.close() // Close surplus connection
		return
	}
	cn.lastUsed = time.Now()
	p.freeconn = append(p.freeconn, cn)
}

// dial establishes a new network connection to the pool's address.
func (p *Pool) dial() (net.Conn, error) {
	dialCtx, cancel := context.WithTimeout(context.Background(), p.config.DialTimeout)
	defer cancel()
	return p.config.DialFunc(dialCtx, "tcp", p.address)
}

// Close closes all connections in the pool.
func (p *Pool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	var firstErr error
	for _, cn := range p.freeconn {
		if err := cn.close(); err != nil && firstErr == nil {
			firstErr = err // Capture the first error
		}
	}
	p.freeconn = make([]*pooledConn, 0) // Clear the list
	return firstErr
}
