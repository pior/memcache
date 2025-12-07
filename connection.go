package memcache

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"time"

	"github.com/pior/memcache/meta"
)

type Dialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

func NewConnection(conn net.Conn) *Connection {
	return &Connection{
		conn:   conn,
		Reader: bufio.NewReader(conn),
		Writer: bufio.NewWriter(conn),
	}
}

// NewConnectionWithTimeout creates a connection with a default operation timeout.
// The timeout is used when the context passed to Execute has no deadline.
func NewConnectionWithTimeout(conn net.Conn, timeout time.Duration) *Connection {
	return &Connection{
		conn:           conn,
		Reader:         bufio.NewReader(conn),
		Writer:         bufio.NewWriter(conn),
		defaultTimeout: timeout,
	}
}

// Connection wraps a network connection with buffered reader and writer for efficient I/O.
type Connection struct {
	conn   net.Conn
	Reader *bufio.Reader
	Writer *bufio.Writer

	// defaultTimeout is used when context has no deadline.
	// Zero means no timeout.
	defaultTimeout time.Duration
}

func (c *Connection) Close() error {
	return c.conn.Close()
}

// setDeadline sets the connection deadline based on context and default timeout.
// Priority: context deadline > default timeout > no deadline.
// Returns the deadline that was set (zero if no deadline).
func (c *Connection) setDeadline(ctx context.Context) (time.Time, error) {
	var deadline time.Time

	// Check if context has a deadline
	if ctxDeadline, ok := ctx.Deadline(); ok {
		deadline = ctxDeadline
	} else if c.defaultTimeout > 0 {
		// Use default timeout if context has no deadline
		deadline = time.Now().Add(c.defaultTimeout)
	}

	// Set deadline on connection (zero deadline clears it)
	if err := c.conn.SetDeadline(deadline); err != nil {
		return time.Time{}, err
	}

	return deadline, nil
}

// Execute implements the Executor interface.
// Executes a single request and returns the response.
// Uses context deadline if present, otherwise uses the connection's default timeout.
func (c *Connection) Execute(ctx context.Context, req *meta.Request) (*meta.Response, error) {
	// Set deadline from context or default timeout
	if _, err := c.setDeadline(ctx); err != nil {
		return nil, err
	}

	// Write request to buffered writer
	if err := meta.WriteRequest(c.Writer, req); err != nil {
		return nil, err
	}

	// Flush the buffered writer
	if err := c.Writer.Flush(); err != nil {
		return nil, err
	}

	resp, err := meta.ReadResponse(c.Reader)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// ExecuteBatch implements the BatchExecutor interface.
// Executes multiple requests in a pipeline using the NoOp marker strategy.
// Sends all requests followed by a NoOp command, then reads responses until the NoOp response.
//
// Returns responses in the same order as requests.
// Individual request errors are captured in Response.Error (protocol errors).
// I/O errors or connection failures are returned as Go errors.
func (c *Connection) ExecuteBatch(ctx context.Context, reqs []*meta.Request) ([]*meta.Response, error) {
	if len(reqs) == 0 {
		return nil, nil
	}

	// Write all requests
	for _, req := range reqs {
		if err := meta.WriteRequest(c.Writer, req); err != nil {
			return nil, err
		}
	}

	// Write NoOp marker to signal end of batch
	noopReq := meta.NewRequest(meta.CmdNoOp, "", nil, nil)
	if err := meta.WriteRequest(c.Writer, noopReq); err != nil {
		return nil, err
	}

	// Flush all writes
	if err := c.Writer.Flush(); err != nil {
		return nil, err
	}

	// Read responses until NoOp
	// Pre-allocate slice for requests + NoOp marker
	responses := make([]*meta.Response, 0, len(reqs)+1)

	for {
		resp, err := meta.ReadResponse(c.Reader)
		if err != nil {
			// Return responses collected so far
			return responses, err
		}

		responses = append(responses, resp)

		// Stop when we hit the NoOp marker
		if resp.Status == meta.StatusMN {
			break
		}

		// Stop on protocol error
		if resp.HasError() {
			break
		}
	}

	// Remove the NoOp response from the end
	if len(responses) > 0 && responses[len(responses)-1].Status == meta.StatusMN {
		responses = responses[:len(responses)-1]
	}

	return responses, nil
}

// ExecuteStats implements the StatsExecutor interface.
// Executes the stats command and returns the stats as a map.
func (c *Connection) ExecuteStats(ctx context.Context, args ...string) (map[string]string, error) {
	// Set deadline from context or default timeout
	if _, err := c.setDeadline(ctx); err != nil {
		return nil, err
	}

	// Build stats request
	statsArg := ""
	if len(args) > 0 {
		statsArg = args[0]
	}
	req := &meta.Request{
		Command: meta.CmdStats,
		Key:     statsArg, // stats uses Key field for optional args
	}

	// Send stats request
	if err := meta.WriteRequest(c.Writer, req); err != nil {
		return nil, err
	}

	// Flush the buffered writer
	if err := c.Writer.Flush(); err != nil {
		return nil, err
	}

	// Read stats response
	stats, err := meta.ReadStatsResponse(c.Reader)
	if err != nil {
		return nil, err
	}

	return stats, nil
}

// Ping performs a simple health check on a connection using the noop command.
func (c *Connection) Ping() error {
	req := meta.NewRequest(meta.CmdNoOp, "", nil, nil)

	resp, err := c.Execute(context.Background(), req)
	if err != nil {
		return err
	}

	if resp.Status != meta.StatusMN {
		return fmt.Errorf("health check failed: %s", resp.Status)
	}

	return nil
}
