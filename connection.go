package memcache

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pior/memcache/protocol"
)

var (
	ErrConnectionClosed = errors.New("memcache: connection closed")
)

type inflightCommand struct {
	cmd    *protocol.Command
	opaque string
}

// Connection represents a single memcache connection
type Connection struct {
	addr     string
	conn     net.Conn
	reader   *bufio.Reader
	mu       sync.Mutex
	inFlight int
	lastUsed time.Time
	closed   bool

	bufPool       *byteBufferPool
	opaqueCounter uint32

	// Single reader goroutine management
	commandCh  chan []inflightCommand
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
		commandCh:  make(chan []inflightCommand, 100), // buffered channel
		closeCh:    make(chan struct{}),
		readerDone: make(chan struct{}),
	}

	// Start the single reader goroutine
	go c.readerLoop()

	return c, nil
}

// Execute sends multiple commands in a pipeline and queues them for response reading
func (c *Connection) Execute(ctx context.Context, commands ...*protocol.Command) error {
	if len(commands) == 0 {
		return nil
	}

	// Check if context is already cancelled
	if err := ctx.Err(); err != nil {
		return err
	}

	// Assign opaque values to each inflight command
	inflightCommands := make([]inflightCommand, len(commands))
	for i, cmd := range commands {
		token := c.getOpaqueToken()
		cmd.WithFlag(protocol.FlagOpaque, token)
		inflightCommands[i] = inflightCommand{cmd: cmd, opaque: token}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return ErrConnectionClosed
	}

	// Set deadline based on context
	if deadline, ok := ctx.Deadline(); ok {
		_ = c.conn.SetDeadline(deadline)
	} else {
		// Clear deadline if context doesn't have one
		_ = c.conn.SetDeadline(time.Time{})
	}

	c.inFlight += len(inflightCommands)

	// Send all commands first
	buf := c.bufPool.Get()
	for _, cmd := range inflightCommands {
		buf.Reset()
		protocol.WriteCommand(cmd.cmd, buf)
		_, err := buf.WriteTo(c.conn)
		if err != nil {
			c.bufPool.Put(buf)
			c.inFlight -= len(inflightCommands)
			c.closed = true
			return err
		}
	}
	c.bufPool.Put(buf)

	// Queue commands for response reading
	select {
	case c.commandCh <- inflightCommands:
		// Successfully queued
	case <-ctx.Done():
		c.inFlight -= len(inflightCommands)
		return ctx.Err()
	case <-c.closeCh:
		c.inFlight -= len(inflightCommands)
		return ErrConnectionClosed
	}

	c.lastUsed = time.Now()
	return nil
}

func (c *Connection) getOpaqueToken() string {
	value := uint16(atomic.AddUint32(&c.opaqueCounter, 1))
	return strconv.Itoa(int(value))
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
					c.setErrorForAllCommands(commands, ErrConnectionClosed)
				default:
					return
				}
			}
		}
	}
}

// readResponses reads responses for a batch of commands (renamed from readResponsesAsync)
func (c *Connection) readResponses(commands []inflightCommand) {
	defer func() {
		c.mu.Lock()
		c.inFlight -= len(commands)
		c.mu.Unlock()
	}()

	// Create a map of opaque -> command for fast lookup
	commandsToProcess := make(map[string]inflightCommand)
	for _, cmd := range commands {
		commandsToProcess[cmd.opaque] = cmd
	}

	// Read exactly the number of responses we expect
	for range commands {
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			// Set error responses for all commands that haven't been processed
			c.setErrorForUnprocessedCommands(commandsToProcess, ErrConnectionClosed)
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
			c.setErrorForUnprocessedCommands(commandsToProcess, err)
			return
		}

		// Find the command that matches this response's opaque
		var matchingCmd *inflightCommand

		// Try opaque-based matching first
		if resp.Opaque != "" {
			if cmd, exists := commandsToProcess[resp.Opaque]; exists {
				delete(commandsToProcess, resp.Opaque)
				matchingCmd = &cmd
			}
		} else {
			// Fallback to order-based matching for responses without opaque
			// Find the first unprocessed command in order
			for _, cmd := range commandsToProcess {
				matchingCmd = &cmd
				delete(commandsToProcess, cmd.opaque)
				break
			}
		}

		if matchingCmd != nil {
			// Convert and set response on the matching command
			matchingCmd.cmd.SetResponse(resp)
		} else {
			// This shouldn't happen in normal operation - duplicate or unknown opaque
			c.mu.Lock()
			c.closed = true
			c.mu.Unlock()

			// Set error for all remaining commands
			c.setErrorForUnprocessedCommands(commandsToProcess, fmt.Errorf("memcache: response opaque mismatch: got %s", resp.Opaque))
			return
		}
	}
}

// setErrorForUnprocessedCommands sets error responses for all unprocessed commands
func (c *Connection) setErrorForUnprocessedCommands(commandsToProcess map[string]inflightCommand, err error) {
	for _, cmd := range commandsToProcess {
		cmd.cmd.SetResponse(&protocol.Response{Error: err})
	}
}

// setErrorForAllCommands sets error responses for all commands
func (c *Connection) setErrorForAllCommands(commands []inflightCommand, err error) {
	for _, cmd := range commands {
		cmd.cmd.SetResponse(&protocol.Response{Error: err})
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

	err := c.Execute(ctx, cmd)
	if err != nil {
		return err
	}

	err = cmd.Wait(ctx)
	if err != nil {
		return err
	}

	return cmd.Response.Error
}
