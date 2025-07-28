package memcache

import (
	"bufio"
	"context"
	"errors"
	"net"
	"sync"
	"time"
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
func (c *Connection) ExecuteBatch(ctx context.Context, commands []*Command) error {
	if len(commands) == 0 {
		return nil
	}

	// Check if context is already cancelled
	if err := ctx.Err(); err != nil {
		return err
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
		protocolBytes := commandToProtocol(cmd)
		if protocolBytes == nil {
			c.inFlight -= len(commands)
			return errors.New("memcache: invalid command")
		}
		if _, err := c.conn.Write(protocolBytes); err != nil {
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
func (c *Connection) readResponsesAsync(commands []*Command) {
	defer func() {
		c.mu.Lock()
		c.inFlight -= len(commands)
		c.mu.Unlock()
	}()

	for i, cmd := range commands {
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			// Set error responses for remaining commands
			for j := i; j < len(commands); j++ {
				resp := &Response{
					Key:   commands[j].Key,
					Error: ErrConnectionClosed,
				}
				commands[j].setResponse(resp)
			}
			return
		}
		c.mu.Unlock()

		resp, err := readResponse(c.reader)
		if err != nil {
			c.mu.Lock()
			c.markClosed()
			c.mu.Unlock()

			// Set error response for this command and all remaining commands
			for j := i; j < len(commands); j++ {
				errorResp := &Response{
					Key:   commands[j].Key,
					Error: err,
				}
				commands[j].setResponse(errorResp)
			}
			return
		}

		// Convert and set response on command
		cmd.setResponse(protocolToResponse(resp, cmd.Key))
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

// Ping sends a simple command to test if the connection is alive
func (c *Connection) Ping(ctx context.Context) error {
	// Use a simple meta get command to a non-existent key
	cmd := NewGetCommand("_ping_test")

	err := c.ExecuteBatch(ctx, []*Command{cmd})
	if err != nil {
		return err
	}

	// For ping, we need to wait for the response to verify the connection works
	resp, err := cmd.GetResponse(ctx)
	if err != nil {
		return err
	}

	if resp.Error != nil {
		return resp.Error
	}

	// We don't care about the actual response content (will likely be "EN" for not found)
	// We only care that we can communicate and get a response
	return nil
}
