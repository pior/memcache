package memcache

import (
	"context"
	"net"
	"time"

	"github.com/fatih/pool"
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

	// InitialConns is the initial number of connections in the pool.
	InitialConns int

	// MaxConns is the maximum number of connections in the pool.
	MaxConns int

	// IdleTimeout is the duration after which an idle connection is closed by the pool.
	// Note: fatih/pool v3 (which is an older version, often pulled in by go get without specific version)
	// used by NewChannelPool doesn't directly support per-connection idle timeout in the same way
	// some other pool implementations do. The pool itself can be configured with idle checks
	// if using a more advanced pool constructor from fatih/pool or another library.
	// This field is included for API completeness and future-proofing.
	IdleTimeout time.Duration // Currently informational for fatih/pool NewChannelPool
}

// pooledClient is an implementation of Client that uses a connection pool.
type pooledClient struct {
	pool pool.Pool
}

// NewClient creates a new pooled Memcached client using the provided configuration.
func NewClient(config Config) (Client, error) {
	if config.DialTimeout == 0 {
		config.DialTimeout = 5 * time.Second
	}

	factory := func() (net.Conn, error) {
		dialCtx, cancel := context.WithTimeout(context.Background(), config.DialTimeout)
		defer cancel()

		if config.DialFunc != nil {
			return config.DialFunc(dialCtx, "tcp", config.Address)
		}
		var d net.Dialer
		return d.DialContext(dialCtx, "tcp", config.Address)
	}

	p, err := pool.NewChannelPool(config.InitialConns, config.MaxConns, factory)
	if err != nil {
		return nil, err
	}

	return &pooledClient{pool: p}, nil
}

func (pc *pooledClient) execute(fn func(mc *Conn) error) error {
	conn, err := pc.pool.Get()
	if err != nil {
		return err
	}
	defer conn.Close() // This returns the connection to the pool.

	mc := NewConn(conn)
	// The underlying net.Conn will be closed by the pool when it's discarded.
	// We don't call mc.Close() here as that would close the actual net.Conn,
	// and the pool expects to manage the lifecycle of the net.Conn it created.
	return fn(mc)
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
	pc.pool.Close()
	return nil // fatih/pool's Close() doesn't return an error.
}
