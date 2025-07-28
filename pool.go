package memcache

import (
	"errors"
	"sync"
	"time"
)

var (
	ErrPoolClosed             = errors.New("memcache: pool closed")
	ErrNoConnectionsAvailable = errors.New("memcache: no connections available")
)

// ConnectionPool is an interface for managing a pool of connections to memcache servers
type ConnectionPool interface {
	// With provides a connection for use within the given function
	With(fn func(conn *Connection) error) error

	// Stats returns statistics about the pool
	Stats() PoolStats

	// Close closes all connections in the pool
	Close() error
}

// Pool manages a pool of connections to memcache servers
type Pool struct {
	addr        string
	minConns    int
	maxConns    int
	connTimeout time.Duration
	idleTimeout time.Duration
	mu          sync.RWMutex
	connections []*Connection
	closed      bool
}

// PoolConfig contains configuration for a connection pool
type PoolConfig struct {
	MinConnections int
	MaxConnections int
	ConnTimeout    time.Duration
	IdleTimeout    time.Duration
}

// DefaultPoolConfig returns a default pool configuration
func DefaultPoolConfig() *PoolConfig {
	return &PoolConfig{
		MinConnections: 1,
		MaxConnections: 10,
		ConnTimeout:    5 * time.Second,
		IdleTimeout:    5 * time.Minute,
	}
}

// NewPool creates a new connection pool for the given address
func NewPool(addr string, config *PoolConfig) (*Pool, error) {
	if config == nil {
		config = DefaultPoolConfig()
	}

	pool := &Pool{
		addr:        addr,
		minConns:    config.MinConnections,
		maxConns:    config.MaxConnections,
		connTimeout: config.ConnTimeout,
		idleTimeout: config.IdleTimeout,
		connections: make([]*Connection, 0, config.MaxConnections),
	}

	// Create minimum connections
	for i := 0; i < config.MinConnections; i++ {
		conn, err := NewConnection(addr, config.ConnTimeout)
		if err != nil {
			// Close any connections we've already created
			pool.Close()
			return nil, err
		}
		pool.connections = append(pool.connections, conn)
	}

	// Start cleanup goroutine
	go pool.cleanup()

	return pool, nil
}

// With provides a connection for use within the given function
func (p *Pool) With(fn func(conn *Connection) error) error {
	conn, err := p.get()
	if err != nil {
		return err
	}

	return fn(conn)
}

// get returns the best available connection from the pool (renamed from Get for internal use)
func (p *Pool) get() (*Connection, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return nil, ErrPoolClosed
	}

	// Remove any closed connections
	p.removeClosedConnections()

	// Find connection with least requests in flight
	var bestConn *Connection
	minInFlight := int(^uint(0) >> 1) // max int

	for _, conn := range p.connections {
		if !conn.IsClosed() {
			inFlight := conn.InFlight()
			if inFlight < minInFlight {
				minInFlight = inFlight
				bestConn = conn
			}
		}
	}

	// If we found a good connection, return it
	if bestConn != nil && minInFlight == 0 {
		return bestConn, nil
	}

	// If we can create more connections, do so
	if len(p.connections) < p.maxConns {
		conn, err := NewConnection(p.addr, p.connTimeout)
		if err != nil {
			// If we can't create a new connection but have existing ones, return best available
			if bestConn != nil {
				return bestConn, nil
			}
			return nil, err
		}
		p.connections = append(p.connections, conn)
		return conn, nil
	}

	// Return best available connection (might have requests in flight)
	if bestConn != nil {
		return bestConn, nil
	}

	return nil, ErrNoConnectionsAvailable
}

// Stats returns statistics about the pool
func (p *Pool) Stats() PoolStats {
	p.mu.RLock()
	defer p.mu.RUnlock()

	stats := PoolStats{
		Address:          p.addr,
		TotalConnections: len(p.connections),
		MinConnections:   p.minConns,
		MaxConnections:   p.maxConns,
	}

	for _, conn := range p.connections {
		if !conn.IsClosed() {
			stats.ActiveConnections++
			stats.TotalInFlight += conn.InFlight()
		}
	}

	return stats
}

// Close closes all connections in the pool
func (p *Pool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return nil
	}

	p.closed = true

	var lastErr error
	for _, conn := range p.connections {
		if err := conn.Close(); err != nil {
			lastErr = err
		}
	}

	p.connections = nil
	return lastErr
}

// cleanup removes idle and closed connections
func (p *Pool) cleanup() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			return
		}

		now := time.Now()
		newConnections := make([]*Connection, 0, len(p.connections))

		for _, conn := range p.connections {
			// Remove closed connections
			if conn.IsClosed() {
				continue
			}

			// Remove idle connections (but keep minimum)
			if len(newConnections) >= p.minConns &&
				conn.InFlight() == 0 &&
				now.Sub(conn.LastUsed()) > p.idleTimeout {
				conn.Close()
				continue
			}

			newConnections = append(newConnections, conn)
		}

		p.connections = newConnections
		p.mu.Unlock()
	}
}

// removeClosedConnections removes closed connections from the pool (must be called with lock held)
func (p *Pool) removeClosedConnections() {
	newConnections := make([]*Connection, 0, len(p.connections))
	for _, conn := range p.connections {
		if !conn.IsClosed() {
			newConnections = append(newConnections, conn)
		}
	}
	p.connections = newConnections
}

// PoolStats contains statistics about a connection pool
type PoolStats struct {
	Address           string
	TotalConnections  int
	ActiveConnections int
	MinConnections    int
	MaxConnections    int
	TotalInFlight     int
}
