package memcache

import (
	"net"
	"time"

	"github.com/fatih/pool"
)

// Client defines the interface for a Memcached client.
type Client interface {
	MetaGet(key string, flags ...MetaFlag) (code string, args []string, data []byte, err error)
	MetaSet(key string, value []byte, flags ...MetaFlag) (code string, args []string, err error)
	MetaDelete(key string, flags ...MetaFlag) (code string, args []string, err error)
	MetaArithmetic(key string, flags ...MetaFlag) (code string, args []string, data []byte, err error)
	MetaNoop() (code string, args []string, err error)
	Close() error
}

// pooledClient is an implementation of Client that uses a connection pool.
type pooledClient struct {
	pool pool.Pool
}

// NewClient creates a new pooled Memcached client.
// addr is the Memcached server address (e.g., "127.0.0.1:11211").
// initialConns is the initial number of connections in the pool.
// maxConns is the maximum number of connections in the pool.
// idleTimeout is the duration after which an idle connection is closed.
func NewClient(addr string, initialConns, maxConns int, idleTimeout time.Duration) (Client, error) {
	factory := func() (net.Conn, error) {
		return net.DialTimeout("tcp", addr, time.Second*5) // 5-second dial timeout
	}

	p, err := pool.NewChannelPool(initialConns, maxConns, factory)
	if err != nil {
		return nil, err
	}

	// It's good practice to also allow configuring the idle timeout for connections in the pool,
	// but fatih/pool v3 doesn't directly expose this in NewChannelPool.
	// For more sophisticated pool management (like idle timeouts per connection),
	// one might need a different pool library or a custom pool implementation.
	// However, fatih/pool handles closing connections when the pool itself is closed.

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
func (pc *pooledClient) MetaGet(key string, flags ...MetaFlag) (code string, args []string, data []byte, err error) {
	err = pc.execute(func(mc *Conn) error {
		code, args, data, err = mc.MetaGet(key, flags...)
		return err
	})
	return
}

// MetaSet executes a MetaSet command.
func (pc *pooledClient) MetaSet(key string, value []byte, flags ...MetaFlag) (code string, args []string, err error) {
	err = pc.execute(func(mc *Conn) error {
		code, args, err = mc.MetaSet(key, value, flags...)
		return err
	})
	return
}

// MetaDelete executes a MetaDelete command.
func (pc *pooledClient) MetaDelete(key string, flags ...MetaFlag) (code string, args []string, err error) {
	err = pc.execute(func(mc *Conn) error {
		code, args, err = mc.MetaDelete(key, flags...)
		return err
	})
	return
}

// MetaArithmetic executes a MetaArithmetic command.
func (pc *pooledClient) MetaArithmetic(key string, flags ...MetaFlag) (code string, args []string, data []byte, err error) {
	err = pc.execute(func(mc *Conn) error {
		code, args, data, err = mc.MetaArithmetic(key, flags...)
		return err
	})
	return
}

// MetaNoop executes a MetaNoop command.
func (pc *pooledClient) MetaNoop() (code string, args []string, err error) {
	err = pc.execute(func(mc *Conn) error {
		code, args, err = mc.MetaNoop()
		return err
	})
	return
}

// Close closes the connection pool.
func (pc *pooledClient) Close() error {
	pc.pool.Close()
	return nil // fatih/pool's Close() doesn't return an error.
}
