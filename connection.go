package memcache

import (
	"bufio"
	"context"
	"fmt"
	"net"

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

// Connection wraps a network connection with buffered reader and writer for efficient I/O.
type Connection struct {
	conn   net.Conn
	Reader *bufio.Reader
	Writer *bufio.Writer
}

func (c *Connection) Close() error {
	return c.conn.Close()
}

func (c *Connection) Send(req *meta.Request) (*meta.Response, error) {
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

// Execute implements the Executor interface.
// Executes a single request and returns the response.
// The context is currently not used but is part of the interface for future timeout support.
func (c *Connection) Execute(ctx context.Context, req *meta.Request) (*meta.Response, error) {
	return c.Send(req)
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
	// ReadResponseBatch(r, 0, true) reads until StatusMN (NoOp marker)
	responses, err := meta.ReadResponseBatch(c.Reader, 0, true)
	if err != nil {
		return nil, err
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

	resp, err := c.Send(req)
	if err != nil {
		return err
	}

	if resp.Status != meta.StatusMN {
		return fmt.Errorf("health check failed: %s", resp.Status)
	}

	return nil
}
