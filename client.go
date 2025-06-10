package memcache

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"syscall"
	"time"
)

// Client defines the interface for a Memcached client.
type Client interface {
	MetaGet(key string, flags ...MetaFlag) (GetResponse, error)
	MetaSet(key string, value []byte, flags ...MetaFlag) (MutateResponse, error)
	MetaDelete(key string, flags ...MetaFlag) (MutateResponse, error)
	MetaArithmetic(key string, flags ...MetaFlag) (ArithmeticResponse, error)
	MetaNoop() (MutateResponse, error)
	Close() error
}

// DialContextFunc is a function that can dial a network connection.
// It's compatible with net.Dialer.DialContext.
type DialContextFunc func(ctx context.Context, network, address string) (net.Conn, error)

// Config holds configuration options for the Memcached client.
type Config struct {
	// Address is the network address of the Memcached server (e.g., "127.0.0.1:11211").
	Address string

	// DialTimeout is the timeout for establishing new connections.
	// Default is 5 seconds if not set.
	DialTimeout time.Duration

	// DialFunc is an optional custom function for dialing new connections.
	// If nil, a default dialer using DialTimeout will be used.
	// The context passed to DialFunc will have a deadline set according to DialTimeout.
	DialFunc DialContextFunc

	// MaxIdleConns specifies the maximum number of idle connections that will
	// be maintained. If less than one, a default (e.g., 2) will be used.
	MaxIdleConns int

	// IdleTimeout is the duration after which an idle connection is closed.
	// If zero or negative, idle connections are not timed out.
	IdleTimeout time.Duration
}

// conn is a connection to a server.
type conn struct {
	nc       net.Conn
	rw       *bufio.ReadWriter
	addr     net.Addr
	pc       *pooledClient
	lastUsed time.Time // Added to track when the connection was last returned to the pool
}

// release returns this connection back to the client's free pool
func (cn *conn) release() {
	cn.pc.putFreeConn(cn.addr, cn)
}

// extendDeadline sets the read/write deadline on the connection.
func (cn *conn) extendDeadline() {
	cn.nc.SetDeadline(time.Now().Add(cn.pc.netTimeout()))
}

// condRelease releases this connection if the error pointed to by err
// is nil (not an error) or is only a protocol level error.
// Otherwise, it closes the net.Conn.
func (cn *conn) condRelease(err *error) {
	if *err == nil {
		cn.release()
		return
	}

	// Check for specific network errors that might mean the connection is permanently broken.
	if ne, ok := (*err).(net.Error); ok && ne.Timeout() {
		// If it's a timeout, we can likely reuse the connection.
		cn.release()
		return
	}

	// Check for errors that indicate a definitely broken connection.
	// This list can be expanded based on common persistent network issues.
	switch {
	case errors.Is(*err, io.EOF), errors.Is(*err, io.ErrUnexpectedEOF), errors.Is(*err, syscall.ECONNRESET), errors.Is(*err, syscall.EPIPE):
		// These errors typically mean the connection is gone or unusable.
		cn.nc.Close()
		return
	}

	// For other errors (including protocol errors from our package, or other net.Error types
	// not explicitly handled as fatal), we'll attempt to reuse the connection.
	// This assumes that application-level errors (like "NF" - Not Found) are handled by the caller
	// and don't necessarily mean the connection itself is bad.
	cn.release()
}

// pooledClient is an implementation of Client that uses a connection pool.
type pooledClient struct {
	config   Config
	mu       sync.Mutex
	freeconn map[string][]*conn
}

// NewClient creates a new pooled Memcached client using the provided configuration.
func NewClient(config Config) (Client, error) {
	if config.DialTimeout == 0 {
		config.DialTimeout = 5 * time.Second
	}
	if config.MaxIdleConns <= 0 {
		config.MaxIdleConns = 2 // DefaultMaxIdleConns
	}

	if config.DialFunc == nil {
		var d net.Dialer
		config.DialFunc = d.DialContext
	}

	pc := &pooledClient{
		config:   config,
		freeconn: make(map[string][]*conn),
	}
	return pc, nil
}

func (pc *pooledClient) netTimeout() time.Duration {
	// Assuming DialTimeout can be used as a general network timeout for now
	// This might need to be a separate Config field if more granularity is needed.
	return pc.config.DialTimeout
}

func (pc *pooledClient) maxIdleConns() int {
	return pc.config.MaxIdleConns
}

// putFreeConn adds a connection to the free list.
func (pc *pooledClient) putFreeConn(addr net.Addr, cn *conn) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if pc.freeconn == nil {
		// This should not happen if initialized in NewClient, but as a safeguard:
		pc.freeconn = make(map[string][]*conn)
	}
	addrStr := addr.String()
	freelist := pc.freeconn[addrStr]
	if len(freelist) >= pc.maxIdleConns() {
		cn.nc.Close() // Close surplus connection
		return
	}
	cn.lastUsed = time.Now() // Update lastUsed time when putting back to pool
	pc.freeconn[addrStr] = append(freelist, cn)
}

// getFreeConn retrieves an idle connection for the address if available.
// It also handles closing stale connections based on IdleTimeout.
func (pc *pooledClient) getFreeConn(addr net.Addr) (*conn, bool) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if pc.freeconn == nil {
		return nil, false
	}
	addrStr := addr.String()
	freelist, ok := pc.freeconn[addrStr]
	if !ok || len(freelist) == 0 {
		return nil, false
	}

	if pc.config.IdleTimeout > 0 {
		// Iterate from the oldest (front) to find a non-stale connection or clean up.
		// This is a bit inefficient as it might involve multiple slice reslicings.
		// A doubly linked list or a heap might be better for performance with many idle conns,
		// but for typical small pools, this should be acceptable.
		var validConns []*conn
		now := time.Now()
		cleaned := false
		for i := 0; i < len(freelist); i++ {
			cn := freelist[i]
			if now.Sub(cn.lastUsed) > pc.config.IdleTimeout {
				cn.nc.Close() // Close stale connection
				cleaned = true
			} else {
				validConns = append(validConns, cn) // Keep non-stale connection
			}
		}
		if cleaned {
			pc.freeconn[addrStr] = validConns
			freelist = validConns // Update freelist for subsequent logic
		}
		if len(freelist) == 0 {
			return nil, false
		}
	}

	// Get the most recently used (end of list)
	cn := freelist[len(freelist)-1]
	pc.freeconn[addrStr] = freelist[:len(freelist)-1]
	return cn, true
}

// dial establishes a new network connection.
func (pc *pooledClient) dial(address string) (net.Conn, error) {
	dialCtx, cancel := context.WithTimeout(context.Background(), pc.config.DialTimeout)
	defer cancel()
	// Assuming "tcp" for now, could be configurable if needed
	return pc.config.DialFunc(dialCtx, "tcp", address)
}

// getConn gets a connection, either from the pool or by dialing a new one.
func (pc *pooledClient) getConn(address string) (*conn, error) {
	// For now, the pool is keyed by the string address.
	// If multiple addresses/sharding were supported, this would resolve to a net.Addr first.
	// For simplicity with single server, we parse the address string to net.Addr here.
	addr, err := net.ResolveTCPAddr("tcp", address)
	if err != nil {
		return nil, err // Should not happen if config.Address is valid
	}

	cn, ok := pc.getFreeConn(addr)
	if ok {
		cn.extendDeadline()
		return cn, nil
	}

	nc, err := pc.dial(address)
	if err != nil {
		return nil, err
	}

	newCn := &conn{
		nc:   nc,
		rw:   bufio.NewReadWriter(bufio.NewReader(nc), bufio.NewWriter(nc)),
		addr: addr, // Use the resolved net.Addr
		pc:   pc,
	}
	newCn.extendDeadline()
	return newCn, nil
}

func (pc *pooledClient) execute(fn func(mc *Conn) error) error {
	// conn, err := pc.pool.Get() // Removed
	cn, err := pc.getConn(pc.config.Address) // Get connection from our pool
	if err != nil {
		return err
	}
	// defer conn.Close() // Removed (fatih/pool specific)
	defer cn.condRelease(&err) // Use our conditional release

	// mc := NewConn(conn) // conn was net.Conn from fatih/pool
	mc := NewConn(cn.nc) // Pass the net.Conn from our conn struct
	// ... rest of the function
	// The underlying net.Conn will be closed by the pool when it\'s discarded.
	// We don\'t call mc.Close() here as that would close the actual net.Conn,
	// and the pool expects to manage the lifecycle of the net.Conn it created.
	// This comment needs adjustment: our condRelease handles closing.
	// mc.Close() should still not be called here as it's for the Conn wrapper,
	// not the underlying net.Conn lifecycle which condRelease manages.
	err = fn(mc) // Assign error from fn to err so condRelease can see it
	return err
}

// MetaGet executes a MetaGet command.
func (pc *pooledClient) MetaGet(key string, flags ...MetaFlag) (resp GetResponse, err error) {
	err = pc.execute(func(mc *Conn) error {
		resp, err = mc.MetaGet(key, flags...)
		return err
	})
	return
}

// MetaSet executes a MetaSet command.
func (pc *pooledClient) MetaSet(key string, value []byte, flags ...MetaFlag) (resp MutateResponse, err error) {
	err = pc.execute(func(mc *Conn) error {
		resp, err = mc.MetaSet(key, value, flags...)
		return err
	})
	return
}

// MetaDelete executes a MetaDelete command.
func (pc *pooledClient) MetaDelete(key string, flags ...MetaFlag) (resp MutateResponse, err error) {
	err = pc.execute(func(mc *Conn) error {
		resp, err = mc.MetaDelete(key, flags...)
		return err
	})
	return
}

// MetaArithmetic executes a MetaArithmetic command.
func (pc *pooledClient) MetaArithmetic(key string, flags ...MetaFlag) (resp ArithmeticResponse, err error) {
	err = pc.execute(func(mc *Conn) error {
		resp, err = mc.MetaArithmetic(key, flags...)
		return err
	})
	return
}

// MetaNoop executes a MetaNoop command.
func (pc *pooledClient) MetaNoop() (resp MutateResponse, err error) {
	err = pc.execute(func(mc *Conn) error {
		resp, err = mc.MetaNoop()
		return err
	})
	return
}

// Close closes the connection pool.
func (pc *pooledClient) Close() error {
	// pc.pool.Close() // Removed
	pc.mu.Lock()
	defer pc.mu.Unlock()
	for _, conns := range pc.freeconn {
		for _, cn := range conns {
			cn.nc.Close()
		}
	}
	pc.freeconn = make(map[string][]*conn) // Clear the map
	return nil
}
