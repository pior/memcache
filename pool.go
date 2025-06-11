package memcache

import (
	"context"
	"net"
	"sync"
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

// Pool manages a pool of network connections for a single server address.
type Pool struct {
	address     string // The server address (e.g., "host:port")
	config      Config
	mu          sync.Mutex
	connections []*pooledConn // List of free connections for the single address
}

// NewPool creates a new connection pool for a given server address and configuration.
func NewPool(address string, config Config) *Pool {
	return &Pool{
		address:     address,
		config:      config,
		connections: make([]*pooledConn, 0, config.MaxIdleConns),
	}
}

// With provides a connection from the pool to the provided function, ensuring proper release/close logic.
// The provided function must not retain the net.Conn after it returns.
func (p *Pool) With(fn func(net.Conn) error) error {
	p.mu.Lock()
	// Check for an existing free connection
	if len(p.connections) > 0 {
		if p.config.IdleTimeout > 0 {
			p.garbageCollectStale()
		}

		if len(p.connections) > 0 {
			// Get the most recently added (end of list)
			cn := p.connections[len(p.connections)-1]
			p.connections = p.connections[:len(p.connections)-1]
			p.mu.Unlock()
			err := fn(cn.nc)
			p.releaseConn(cn, err)
			return err
		}
	}
	p.mu.Unlock() // Unlock before dialing if no free connections were found or all were stale

	// Dial a new connection
	netConn, err := p.dial()
	if err != nil {
		return err
	}

	cn := &pooledConn{
		nc:   netConn,
		pool: p,
	}
	err = fn(cn.nc)
	p.releaseConn(cn, err)
	return err
}

func (p *Pool) garbageCollectStale() {
	var validConns []*pooledConn
	now := time.Now()

	cleaned := false

	for i := 0; i < len(p.connections); i++ {
		cn := p.connections[i]
		if now.Sub(cn.lastUsed) > p.config.IdleTimeout {
			cn.close() // Close stale connection
			cleaned = true
		} else {
			validConns = append(validConns, cn)
		}
	}

	if cleaned {
		p.connections = validConns
	}
}

// releaseConn handles the conditional release of the connection back to the pool or closes it.
func (p *Pool) releaseConn(cn *pooledConn, err error) {
	if !p.resumableError(err) {
		cn.close() // Close the connection
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.connections) >= p.config.MaxIdleConns {
		cn.close() // Close surplus connection
		return
	}

	cn.lastUsed = time.Now()
	p.connections = append(p.connections, cn)
}

func (p *Pool) resumableError(err error) bool {
	if err == nil {
		return true
	}

	// TODO: handle network errors
	// TODO: handle memcached errors

	return false
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
	for _, cn := range p.connections {
		if err := cn.close(); err != nil && firstErr == nil {
			firstErr = err // Capture the first error
		}
	}
	p.connections = make([]*pooledConn, 0) // Clear the list
	return firstErr
}

func (p *Pool) connectionsCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.connections)
}
