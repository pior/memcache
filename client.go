package memcache

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/pior/memcache/protocol"
)

var (
	ErrServerError        = errors.New("memcache: server error")
	ErrClientClosed       = errors.New("memcache: client closed")
	ErrNoServersSpecified = errors.New("memcache: no servers specified")
	ErrMalformedKey       = errors.New("memcache: malformed key")
	ErrMalformedCommand   = errors.New("memcache: malformed command")
)

// Client is a high-level memcache client that manages multiple servers
type Client struct {
	servers  []string
	selector Selector
	pools    map[string]ConnectionPool
	closed   atomic.Bool
}

// ClientConfig contains configuration for the memcache client
type ClientConfig struct {
	PoolConfig PoolConfig
}

// DefaultClientConfig returns a default client configuration
func DefaultClientConfig() *ClientConfig {
	return &ClientConfig{
		PoolConfig: DefaultPoolConfig(),
	}
}

// NewClient creates a new memcache client
func NewClient(serverAddresses []string, config *ClientConfig) (*Client, error) {
	if config == nil {
		config = DefaultClientConfig()
	}

	if len(serverAddresses) == 0 {
		return nil, ErrNoServersSpecified
	}

	defaultPoolConfig := DefaultPoolConfig()
	if config.PoolConfig == (PoolConfig{}) {
		config.PoolConfig = defaultPoolConfig
	}
	if config.PoolConfig.ConnTimeout == 0 {
		config.PoolConfig.ConnTimeout = defaultPoolConfig.ConnTimeout
	}
	if config.PoolConfig.IdleTimeout == 0 {
		config.PoolConfig.IdleTimeout = defaultPoolConfig.IdleTimeout
	}

	c := &Client{
		servers:  serverAddresses,
		selector: DefaultSelector,
		pools:    make(map[string]ConnectionPool, len(serverAddresses)),
	}

	for _, server := range serverAddresses {
		c.pools[server] = NewPool(server, config.PoolConfig)
	}

	return c, nil
}

// Do executes one or more memcache commands
func (c *Client) Do(ctx context.Context, commands ...*protocol.Command) error {
	if c.closed.Load() {
		return ErrClientClosed
	}

	if len(commands) == 0 {
		return nil
	}

	// Validate commands first
	for _, cmd := range commands {
		if err := c.validateCommand(cmd); err != nil {
			return err
		}
	}

	// Group commands by server
	commandsByServer := make(map[string][]*protocol.Command)
	for _, cmd := range commands {
		serverAddr := c.selector(c.servers, cmd.Key)
		commandsByServer[serverAddr] = append(commandsByServer[serverAddr], cmd)
	}

	// Execute commands per server
	var errs error
	for serverAddr, poolCommands := range commandsByServer {
		err := c.pools[serverAddr].With(func(conn *Connection) error {
			return conn.ExecuteBatch(ctx, poolCommands)
		})

		if err != nil {
			errs = errors.Join(errs, err)
		}
	}

	return errs
}

func (c *Client) DoWait(ctx context.Context, commands ...*protocol.Command) error {
	if err := c.Do(ctx, commands...); err != nil {
		return err
	}
	return WaitAll(ctx, commands...)
}

// Get retrieves a single value from the cache
func (c *Client) Get(ctx context.Context, key string) (*protocol.Response, error) {
	cmd := NewGetCommand(key)
	if err := c.DoWait(ctx, cmd); err != nil {
		return nil, err
	}
	return cmd.Response, nil
}

// Set stores a value in the cache
func (c *Client) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	cmd := NewSetCommand(key, value, ttl)
	return c.DoWait(ctx, cmd)
}

// Delete removes a value from the cache
func (c *Client) Delete(ctx context.Context, key string) error {
	cmd := NewDeleteCommand(key)
	return c.DoWait(ctx, cmd)
}

// TODO: return the updated value as int64

// Increment increments a numeric value in the cache
func (c *Client) Increment(ctx context.Context, key string, delta int64) (*protocol.Response, error) {
	cmd := NewIncrementCommand(key, delta)
	if err := c.DoWait(ctx, cmd); err != nil {
		return nil, err
	}
	return cmd.Response, nil
}

// Decrement decrements a numeric value in the cache
func (c *Client) Decrement(ctx context.Context, key string, delta int64) (*protocol.Response, error) {
	cmd := NewDecrementCommand(key, delta)
	if err := c.DoWait(ctx, cmd); err != nil {
		return nil, err
	}
	return cmd.Response, nil
}

func (c *Client) validateCommand(cmd *protocol.Command) error {
	if cmd == nil {
		return ErrMalformedCommand
	}

	switch cmd.Type {
	case protocol.CmdGet, protocol.CmdDelete, protocol.CmdDebug:
		if !protocol.IsValidKey(cmd.Key) {
			return ErrMalformedKey
		}
	case protocol.CmdSet:
		if !protocol.IsValidKey(cmd.Key) {
			return ErrMalformedKey
		}

		// Set commands need a value
		if cmd.Value == nil {
			return ErrMalformedCommand
		}
	case protocol.CmdArithmetic:
		if !protocol.IsValidKey(cmd.Key) {
			return ErrMalformedKey
		}

		// Arithmetic commands need a key and delta flag
		if _, exists := cmd.Flags.Get(protocol.FlagDelta); !exists {
			return ErrMalformedCommand
		}
	case protocol.CmdNoOp:
	default:
		return ErrMalformedCommand
	}

	return nil
}

// Ping checks connectivity to all servers
func (c *Client) Ping(ctx context.Context) error {
	if c.closed.Load() {
		return ErrClientClosed
	}

	var errs error
	for serverAddress, pool := range c.pools {
		err := pool.With(func(conn *Connection) error {
			return conn.Ping(ctx)
		})
		if err != nil {
			errs = fmt.Errorf("memcache: failed to ping server %q: %v", serverAddress, err)
			errs = errors.Join(errs, err)
		}
	}

	return errs
}

// Stats returns statistics from all servers
func (c *Client) Stats() []PoolStats {
	if c.closed.Load() {
		return nil
	}

	var stats []PoolStats
	for _, pool := range c.pools {
		stats = append(stats, pool.Stats())
	}

	return stats
}

// Close closes all connections to all servers
func (c *Client) Close() error {
	if c.closed.CompareAndSwap(false, true) {
		return nil
	}

	for _, pool := range c.pools {
		if err := pool.Close(); err != nil {
			return err
		}
	}

	return nil
}

func GetMemcacheServers() []string {
	return strings.Split(os.Getenv("MEMCACHE_SERVERS"), ",")
}
