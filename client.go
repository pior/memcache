package memcache

import (
	"fmt"
	"net"
	"sync"
	"time"
)

// Client defines the interface for a Memcached client.
type Client interface {
	MetaGet(key string, flags ...MetaFlag) (GetResponse, error)
	MetaSet(key string, value []byte, flags ...MetaFlag) (MutateResponse, error)
	MetaDelete(key string, flags ...MetaFlag) (MutateResponse, error)
	MetaArithmetic(key string, flags ...MetaFlag) (ArithmeticResponse, error)
	MetaNoop(key string) (MutateResponse, error)
	Close() error
}

// Config holds configuration options for the Memcached client.
type Config struct {
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

type client struct {
	servers Servers
	config  Config

	pools map[string]*Pool
	mu    sync.Mutex
}

// NewClient creates a new pooled Memcached client using the provided configuration.
func NewClient(servers Servers, config Config) (*client, error) {
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

	cl := &client{
		servers: servers,
		config:  config,
		pools:   make(map[string]*Pool),
	}
	return cl, nil
}

func (cl *client) getPool(key string) *Pool {
	address := cl.servers.Select(key)

	cl.mu.Lock()
	p, exists := cl.pools[address]
	if !exists {
		p = NewPool(address, cl.config)
		cl.pools[address] = p
	}
	cl.mu.Unlock()

	return p
}

func (cl *client) execute(key string, fn func(mc *Conn) error) (err error) {
	pool := cl.getPool(key)

	return pool.With(func(c net.Conn) error {
		mc := NewConn(c)
		return fn(mc)
	})
}

// MetaGet executes a MetaGet command.
func (cl *client) MetaGet(key string, flags ...MetaFlag) (resp GetResponse, err error) {
	err = cl.execute(key, func(mc *Conn) error {
		resp, err = mc.MetaGet(key, flags...)
		return err
	})
	return
}

// MetaSet executes a MetaSet command.
func (cl *client) MetaSet(key string, value []byte, flags ...MetaFlag) (resp MutateResponse, err error) {
	err = cl.execute(key, func(mc *Conn) error {
		resp, err = mc.MetaSet(key, value, flags...)
		return err
	})
	return
}

// MetaDelete executes a MetaDelete command.
func (cl *client) MetaDelete(key string, flags ...MetaFlag) (resp MutateResponse, err error) {
	err = cl.execute(key, func(mc *Conn) error {
		resp, err = mc.MetaDelete(key, flags...)
		return err
	})
	return
}

// MetaArithmetic executes a MetaArithmetic command.
func (cl *client) MetaArithmetic(key string, flags ...MetaFlag) (resp ArithmeticResponse, err error) {
	err = cl.execute(key, func(mc *Conn) error {
		resp, err = mc.MetaArithmetic(key, flags...)
		return err
	})
	return
}

// MetaNoop executes a MetaNoop command. The key parameter is only used for server selection.
func (cl *client) MetaNoop(key string) (resp MutateResponse, err error) {
	err = cl.execute(key, func(mc *Conn) error {
		resp, err = mc.MetaNoop()
		return err
	})
	return
}

// Close closes the connection pool.
func (cl *client) Close() (err error) {
	cl.mu.Lock()
	defer cl.mu.Unlock()

	for address, pool := range cl.pools {
		if errClose := pool.Close(); errClose != nil {
			err = fmt.Errorf("failed to close pool for %s: %w", address, errClose)
		}
		delete(cl.pools, address)
	}

	return
}

// Ensure client implements Client interface
var _ Client = (*client)(nil)
