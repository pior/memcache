package memcache

import (
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
	pool *Pool
}

// NewClient creates a new pooled Memcached client using the provided configuration.
func NewClient(address string, config Config) (*client, error) {
	poolConfig := PoolConfig{
		DialTimeout:  config.DialTimeout,
		DialFunc:     config.DialFunc,
		MaxIdleConns: config.MaxIdleConns,
		IdleTimeout:  config.IdleTimeout,
	}

	p, err := NewPool(address, poolConfig)
	if err != nil {
		return nil, err
	}

	cl := &client{
		pool: p,
	}
	return cl, nil
}

func (cl *client) execute(fn func(mc *Conn) error) (err error) {
	pConn, err := cl.pool.Get()
	if err != nil {
		return err
	}
	defer func() {
		pConn.Release(err)
	}()

	mc := NewConn(pConn.nc)
	err = fn(mc)
	return err
}

// MetaGet executes a MetaGet command.
func (cl *client) MetaGet(key string, flags ...MetaFlag) (resp GetResponse, err error) {
	err = cl.execute(func(mc *Conn) error {
		resp, err = mc.MetaGet(key, flags...)
		return err
	})
	return
}

// MetaSet executes a MetaSet command.
func (cl *client) MetaSet(key string, value []byte, flags ...MetaFlag) (resp MutateResponse, err error) {
	err = cl.execute(func(mc *Conn) error {
		resp, err = mc.MetaSet(key, value, flags...)
		return err
	})
	return
}

// MetaDelete executes a MetaDelete command.
func (cl *client) MetaDelete(key string, flags ...MetaFlag) (resp MutateResponse, err error) {
	err = cl.execute(func(mc *Conn) error {
		resp, err = mc.MetaDelete(key, flags...)
		return err
	})
	return
}

// MetaArithmetic executes a MetaArithmetic command.
func (cl *client) MetaArithmetic(key string, flags ...MetaFlag) (resp ArithmeticResponse, err error) {
	err = cl.execute(func(mc *Conn) error {
		resp, err = mc.MetaArithmetic(key, flags...)
		return err
	})
	return
}

// MetaNoop executes a MetaNoop command.
func (cl *client) MetaNoop() (resp MutateResponse, err error) {
	err = cl.execute(func(mc *Conn) error {
		resp, err = mc.MetaNoop()
		return err
	})
	return
}

// Close closes the connection pool.
func (cl *client) Close() error {
	if cl.pool != nil {
		return cl.pool.Close()
	}
	return nil
}

// Ensure client implements Client interface
var _ Client = (*client)(nil)
