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

// ExecuteBatch sends multiple commands in a pipeline and sets their responses
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
	defer func() { c.inFlight -= len(commands) }()

	// Send all commands
	for _, cmd := range commands {
		protocolBytes := commandToProtocol(cmd)
		if protocolBytes == nil {
			return errors.New("memcache: invalid command")
		}
		if _, err := c.conn.Write(protocolBytes); err != nil {
			c.markClosed()
			return err
		}
	}

	// Read all responses and set them on commands
	for _, cmd := range commands {
		resp, err := readResponse(c.reader)
		if err != nil {
			c.markClosed()
			return err
		}
		// Convert and set response on command
		cmd.setResponse(protocolToResponse(resp, cmd.Key))
	}

	c.lastUsed = time.Now()
	return nil
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
	// We don't care about the response (will likely be "EN" for not found)
	// We only care that we can communicate
	return err
}
