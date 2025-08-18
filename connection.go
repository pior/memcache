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

	bufPool *byteBufferPool

	// Single reader goroutine management
	commandCh  chan []*protocol.Command
	closeCh    chan struct{}
	readerDone chan struct{}
}

// NewConnection creates a new connection with custom timeout
func NewConnection(addr string, timeout time.Duration) (*Connection, error) {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, err
	}

	c := &Connection{
		addr:       addr,
		conn:       conn,
		reader:     bufio.NewReader(conn),
		lastUsed:   time.Now(),
		bufPool:    newByteBufferPool(1024),
		commandCh:  make(chan []*protocol.Command, 100), // buffered channel
		closeCh:    make(chan struct{}),
		readerDone: make(chan struct{}),
	}

	// Start the single reader goroutine
	go c.readerLoop()

	return c, nil
}

// ExecuteBatch sends multiple commands in a pipeline and queues them for response reading
func (c *Connection) ExecuteBatch(ctx context.Context, commands []*protocol.Command) error {
	if len(commands) == 0 {
		return nil
	}

	// Check if context is already cancelled
	if err := ctx.Err(); err != nil {
		return err
	}

	buf := c.bufPool.Get()
	defer c.bufPool.Put(buf)

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
		protocol.WriteCommand(cmd, buf)
		_, err := buf.WriteTo(c.conn)
		if err != nil {
			c.inFlight -= len(commands)
			c.closed = true
			return err
		}
		buf.Reset()
	}

	// Queue commands for response reading
	select {
	case c.commandCh <- commands:
		// Successfully queued
	case <-ctx.Done():
		c.inFlight -= len(commands)
		return ctx.Err()
	case <-c.closeCh:
		c.inFlight -= len(commands)
		return ErrConnectionClosed
	}

	c.lastUsed = time.Now()
	return nil
}

// readerLoop is the main goroutine that reads responses for all commands
func (c *Connection) readerLoop() {
	defer close(c.readerDone)

	for {
		select {
		case commands := <-c.commandCh:
			c.readResponses(commands)
		case <-c.closeCh:
			// Connection is being closed, drain any remaining commands
			for {
				select {
				case commands := <-c.commandCh:
					c.setErrorForAllCommands(commands, nil, ErrConnectionClosed)
				default:
					return
				}
			}
		}
	}
}

// readResponses reads responses for a batch of commands (renamed from readResponsesAsync)
func (c *Connection) readResponses(commands []*protocol.Command) {
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
	for range commands {
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			// Set error responses for all commands that haven't been processed
			c.setErrorForAllCommands(commands, processedOpaques, ErrConnectionClosed)
			return
		}
		c.mu.Unlock()

		resp, err := protocol.ReadResponse(c.reader)
		if err == nil && resp == nil {
			err = fmt.Errorf("memcache: nil response")
		}
		if err != nil {
			c.mu.Lock()
			c.closed = true
			c.mu.Unlock()

			// Set error response for all commands that haven't been processed
			c.setErrorForAllCommands(commands, processedOpaques, err)
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
			c.closed = true
			c.mu.Unlock()

			// Set error for all remaining commands
			c.setErrorForAllCommands(commands, processedOpaques, fmt.Errorf("memcache: response opaque mismatch: got %s", resp.Opaque))
			return
		}
	}
}

// setErrorForAllCommands sets error responses for all unprocessed commands
func (c *Connection) setErrorForAllCommands(commands []*protocol.Command, processedOpaques map[string]bool, err error) {
	for _, cmd := range commands {
		if !processedOpaques[cmd.Opaque] {
			cmd.SetResponse(&protocol.Response{
				Error: err,
			})
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
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()

	// Signal the reader goroutine to stop
	close(c.closeCh)

	// Wait for the reader goroutine to finish
	<-c.readerDone

	// Close the network connection
	return c.conn.Close()
}

// Ping sends a simple command to test if the connection is alive, using the noop command
func (c *Connection) Ping(ctx context.Context) error {
	cmd := NewNoOpCommand()

	err := c.ExecuteBatch(ctx, []*protocol.Command{cmd})
	if err != nil {
		return err
	}

	err = cmd.Wait(ctx)
	if err != nil {
		return err
	}

	return cmd.Response.Error
}
