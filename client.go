package memcache

import (
	"context"
	"errors"
)

var (
	ErrCacheMiss    = errors.New("memcache: cache miss")
	ErrKeyTooLong   = errors.New("memcache: key too long")
	ErrEmptyKey     = errors.New("memcache: empty key")
	ErrServerError  = errors.New("memcache: server error")
	ErrClientClosed = errors.New("memcache: client closed")
)

// Client is a high-level memcache client that manages multiple servers
type Client struct {
	selector ServerSelector
	closed   bool
}

// ClientConfig contains configuration for the memcache client
type ClientConfig struct {
	Servers    []string
	PoolConfig *PoolConfig
	HashRing   *HashRingConfig
}

// HashRingConfig contains configuration for consistent hashing
type HashRingConfig struct {
	VirtualNodes int
}

// DefaultClientConfig returns a default client configuration
func DefaultClientConfig() *ClientConfig {
	return &ClientConfig{
		Servers:    []string{"localhost:11211"},
		PoolConfig: DefaultPoolConfig(),
		HashRing: &HashRingConfig{
			VirtualNodes: 160,
		},
	}
}

// NewClient creates a new memcache client
func NewClient(config *ClientConfig) (*Client, error) {
	if config == nil {
		config = DefaultClientConfig()
	}

	if len(config.Servers) == 0 {
		return nil, errors.New("memcache: no servers specified")
	}

	// Ensure we have pool config
	if config.PoolConfig == nil {
		config.PoolConfig = DefaultPoolConfig()
	} else {
		if config.PoolConfig.ConnTimeout == 0 {
			config.PoolConfig.ConnTimeout = DefaultPoolConfig().ConnTimeout
		}
		if config.PoolConfig.IdleTimeout == 0 {
			config.PoolConfig.IdleTimeout = DefaultPoolConfig().IdleTimeout
		}
	}

	// Ensure we have hash ring config
	if config.HashRing == nil {
		config.HashRing = &HashRingConfig{
			VirtualNodes: 160,
		}
	}

	// Create server selector
	selector, err := NewConsistentHashSelectorWithPools(config.Servers, config.PoolConfig, config.HashRing.VirtualNodes)
	if err != nil {
		return nil, err
	}

	return &Client{
		selector: selector,
	}, nil
}

// Do executes one or more memcache commands and returns responses
func (c *Client) Do(ctx context.Context, commands ...*Command) ([]*Response, error) {
	if c.closed {
		return nil, ErrClientClosed
	}

	if len(commands) == 0 {
		return []*Response{}, nil
	}

	// Validate all commands first
	for _, cmd := range commands {
		if err := c.validateCommand(cmd); err != nil {
			return nil, err
		}
	}

	// Group commands by server
	serverCommands := make(map[ConnectionPool][]*Command)
	for _, cmd := range commands {
		pool, err := c.selector.SelectServer(cmd.Key)
		if err != nil {
			return nil, err
		}
		serverCommands[pool] = append(serverCommands[pool], cmd)
	}

	// Execute commands per server
	responses := make([]*Response, 0, len(commands))
	for pool, poolCommands := range serverCommands {
		// Execute using the pool's With method
		var metaResponses []*metaResponse
		var err error

		err = pool.With(func(conn *Connection) error {
			if len(poolCommands) == 1 {
				metaResp, execErr := conn.Execute(ctx, poolCommands[0])
				if execErr != nil {
					return execErr
				}
				metaResponses = []*metaResponse{metaResp}
			} else {
				var execErr error
				metaResponses, execErr = conn.ExecuteBatch(ctx, poolCommands)
				if execErr != nil {
					return execErr
				}
			}
			return nil
		})

		if err != nil {
			return nil, err
		}

		// Convert responses
		for i, metaResp := range metaResponses {
			originalKey := poolCommands[i].Key
			resp := protocolToResponse(metaResp, originalKey)
			responses = append(responses, resp)
		}
	}

	return responses, nil
}

// validateCommand validates a command before execution
func (c *Client) validateCommand(cmd *Command) error {
	if cmd == nil {
		return errors.New("memcache: command cannot be nil")
	}

	if err := validateKey(cmd.Key); err != nil {
		return err
	}

	switch cmd.Type {
	case CmdMetaGet, CmdMetaDelete:
		// These commands only need a valid key
	case CmdMetaSet:
		// Set commands need a value
		if cmd.Value == nil {
			return errors.New("memcache: set command requires a value")
		}
	case CmdMetaArithmetic:
		// Arithmetic commands need a key and delta flag
		if _, exists := cmd.GetFlag(FlagDelta); !exists {
			return errors.New("memcache: arithmetic command requires delta flag")
		}
	case CmdMetaDebug, CmdMetaNoOp:
		// Debug and no-op commands are valid as-is
	default:
		return errors.New("memcache: unsupported command type: " + cmd.Type)
	}

	return nil
}

// Ping checks connectivity to all servers
func (c *Client) Ping(ctx context.Context) error {
	if c.closed {
		return ErrClientClosed
	}

	return c.selector.Ping(ctx)
}

// Stats returns statistics from all servers
func (c *Client) Stats() []PoolStats {
	if c.closed {
		return nil
	}

	return c.selector.Stats()
}

// Close closes all connections to all servers
func (c *Client) Close() error {
	if c.closed {
		return nil
	}

	c.closed = true
	return c.selector.Close()
}

// validateKey validates a cache key
func validateKey(key string) error {
	if len(key) == 0 {
		return ErrEmptyKey
	}
	if len(key) > 250 {
		return ErrKeyTooLong
	}
	if !isValidKey(key) {
		return ErrMalformedKey
	}
	return nil
}
