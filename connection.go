package memcache

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/pior/memcache/protocol"
)

var (
	ErrConnectionClosed = errors.New("memcache: connection closed")
)

// Connection represents a single memcache connection
type Connection struct {
	addr     string
	conn     net.Conn
	reader   *bufio.Reader
	mu       sync.Mutex
	inFlight int
	lastUsed time.Time
	closed   bool
}

// NewConnection creates a new connection with custom timeout
func NewConnection(addr string, timeout time.Duration) (*Connection, error) {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, err
	}

	return &Connection{
		addr:     addr,
		conn:     conn,
		reader:   bufio.NewReader(conn),
		lastUsed: time.Now(),
	}, nil
}

// ExecuteBatch sends multiple commands in a pipeline and starts reading responses asynchronously
func (c *Connection) ExecuteBatch(ctx context.Context, commands []*protocol.Command) error {
	if len(commands) == 0 {
		return nil
	}

	// Check if context is already cancelled
	if err := ctx.Err(); err != nil {
		return err
	}

	for _, cmd := range commands {
		protocol.SetRandomOpaque(cmd)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return ErrConnectionClosed
	}

	// Set deadline based on context
	if deadline, ok := ctx.Deadline(); ok {
		c.conn.SetDeadline(deadline)
	} else {
		// Clear deadline if context doesn't have one
		c.conn.SetDeadline(time.Time{})
	}

	c.inFlight += len(commands)

	// Send all commands first
	for _, cmd := range commands {
		_, err := protocol.WriteCommand(cmd, c.conn)
		if err != nil {
			c.inFlight -= len(commands)
			c.markClosed()
			return err
		}
	}

	// Start reading responses asynchronously
	go c.readResponsesAsync(commands)

	c.lastUsed = time.Now()
	return nil
}

// readResponsesAsync reads responses for commands asynchronously
func (c *Connection) readResponsesAsync(commands []*protocol.Command) {
	defer func() {
		c.mu.Lock()
		c.inFlight -= len(commands)
		c.mu.Unlock()
	}()

	// Create a map of opaque -> command for fast lookup
	opaqueToCommand := make(map[string]*protocol.Command)
	processedOpaques := make(map[string]bool)
	for _, cmd := range commands {
		opaqueToCommand[cmd.Opaque] = cmd
	}

	// Read exactly the number of responses we expect
	for i := 0; i < len(commands); i++ {
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			// Set error responses for all commands that haven't been processed
			for _, cmd := range commands {
				if !processedOpaques[cmd.Opaque] {
					cmd.SetResponse(&protocol.Response{
						Error: ErrConnectionClosed,
					})
				}
			}
			return
		}
		c.mu.Unlock()

		resp, err := protocol.ReadResponse(c.reader)
		if err == nil && resp == nil {
			err = fmt.Errorf("memcache: nil response")
		}
		if err != nil {
			c.mu.Lock()
			c.markClosed()
			c.mu.Unlock()

			// Set error response for all commands that haven't been processed
			for _, cmd := range commands {
				if !processedOpaques[cmd.Opaque] {
					errorResp := &protocol.Response{
						Error: err,
					}
					cmd.SetResponse(errorResp)
				}
			}
			return
		}

		// Find the command that matches this response's opaque
		var targetCmd *protocol.Command

		// Try opaque-based matching first
		if resp.Opaque != "" {
			if cmd, exists := opaqueToCommand[resp.Opaque]; exists && !processedOpaques[resp.Opaque] {
				targetCmd = cmd
			}
		} else {
			// Fallback to order-based matching for responses without opaque
			// Find the first unprocessed command in order
			for _, cmd := range commands {
				if !processedOpaques[cmd.Opaque] {
					targetCmd = cmd
					break
				}
			}
		}

		if targetCmd != nil {
			// Convert and set response on the matching command
			targetCmd.SetResponse(resp)
			processedOpaques[targetCmd.Opaque] = true
		} else {
			// This shouldn't happen in normal operation - duplicate or unknown opaque
			c.mu.Lock()
			c.markClosed()
			c.mu.Unlock()

			// Set error for all remaining commands
			for _, cmd := range commands {
				if !processedOpaques[cmd.Opaque] {
					errorResp := &protocol.Response{
						Error: fmt.Errorf("memcache: response opaque mismatch: got %s", resp.Opaque),
					}
					cmd.SetResponse(errorResp)
				}
			}
			return
		}
	}
}

// InFlight returns the number of requests currently in flight
func (c *Connection) InFlight() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.inFlight
}

// LastUsed returns when the connection was last used
func (c *Connection) LastUsed() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastUsed
}

// IsClosed returns whether the connection is closed
func (c *Connection) IsClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// Addr returns the connection address
func (c *Connection) Addr() string {
	return c.addr
}

// Close closes the connection
func (c *Connection) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}

	c.markClosed()
	return c.conn.Close()
}

// markClosed marks the connection as closed (must be called with lock held)
func (c *Connection) markClosed() {
	c.closed = true
}

// Ping sends a simple command to test if the connection is alive, using the noop command
func (c *Connection) Ping(ctx context.Context) error {
	cmd := NewNoOpCommand()

	err := c.ExecuteBatch(ctx, []*protocol.Command{cmd})
	if err != nil {
		return err
	}

	err = WaitAll(ctx, cmd)
	if err != nil {
		return err
	}

	return cmd.Response.Error
}
