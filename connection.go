package memcache

import (
	"bufio"
	"context"
	"errors"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pior/memcache/protocol"
)

var (
	ErrConnectionClosed           = errors.New("memcache: connection closed")
	ErrConnectionResponseMismatch = errors.New("memcache: response mismatch")
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

	bufPool        *byteBufferPool
	commandCounter uint32

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

// Execute sends multiple commands in a pipeline and queues them for response reading
func (c *Connection) Execute(ctx context.Context, commands ...*protocol.Command) error {
	if len(commands) == 0 {
		return nil
	}

	// Check if context is already cancelled
	if err := ctx.Err(); err != nil {
		return err
	}

	// Increment the command counter and assign opaque values if allowed
	for _, cmd := range commands {
		commandIndex := uint16(atomic.AddUint32(&c.commandCounter, 1))

		if cmd.Type != protocol.CmdNoOp && cmd.Type != protocol.CmdDebug {
			token := strconv.Itoa(int(commandIndex))
			cmd.Flags.Set(protocol.FlagOpaque, token)
		}
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

	c.inFlight += len(commands)

	// Send all commands first
	buf := c.bufPool.Get()
	for _, cmd := range commands {
		buf.Reset()
		protocol.WriteCommand(cmd, buf)
		_, err := buf.WriteTo(c.conn)
		if err != nil {
			c.bufPool.Put(buf)
			c.inFlight -= len(commands)
			c.closed = true
			return err
		}
	}
	c.bufPool.Put(buf)

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
					for _, cmd := range commands {
						cmd.SetResponse(&protocol.Response{Error: ErrConnectionClosed})
					}
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

	// Read exactly the number of responses we expect
	for range commands {
		c.readResponse(commands)
	}
}

func (c *Connection) readResponse(commands []*protocol.Command) {
	c.mu.Lock()
	if c.closed {
		// Set error responses for all commands that haven't been processed
		for _, cmd := range commands {
			if cmd.Response == nil {
				cmd.SetResponse(&protocol.Response{Error: ErrConnectionClosed})
			}
		}
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()

	resp, err := protocol.ReadResponse(c.reader)
	if err != nil {
		c.mu.Lock()
		c.closed = true
		c.mu.Unlock()

		// Set error response for all commands that haven't been processed
		for _, cmd := range commands {
			if cmd.Response == nil {
				cmd.SetResponse(&protocol.Response{Error: err})
			}
		}
		return
	}

	// Try opaque-based matching first
	if resp.Opaque != "" {
		for _, cmd := range commands {
			if cmd.Response != nil {
				continue
			}
			if val, ok := cmd.Flags.Get(protocol.FlagOpaque); ok && val == resp.Opaque {
				cmd.SetResponse(resp)
				return
			}
		}
	}

	// Fallback to order-based matching for responses without opaque
	// Pick the first unprocessed command in order
	for _, cmd := range commands {
		if cmd.Response == nil {
			commands[0].SetResponse(resp)
			return
		}
	}

	// We received an extra response, this shouldn't happen in normal operation
	// Close the connection
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
}

// InFlightCommands returns the number of requests currently in flight
func (c *Connection) InFlightCommands() int {
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
