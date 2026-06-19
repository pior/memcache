package memcache

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"time"

	"github.com/pior/memcache/meta"
)

// NewConnection creates a connection with an optional default timeout.
// The timeout is a per-operation upper bound: each operation's deadline is the
// earlier of the context deadline and now+timeout (see setDeadline). Zero
// timeout means no cap — the operation is bounded only by the context.
func NewConnection(conn net.Conn, timeout time.Duration) *Connection {
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

	// defaultTimeout is a per-operation upper bound on the deadline, capping
	// even a context that has a later (or no) deadline. Zero means no cap.
	defaultTimeout time.Duration
}

func (c *Connection) Close() error {
	return c.conn.Close()
}

// setDeadline sets the connection deadline to the earlier of the context
// deadline and now+defaultTimeout, so defaultTimeout is a per-operation upper
// bound rather than a fallback that any context deadline disables. This matters
// for a hung-but-connected server: with a long-lived context (e.g. a request-
// or job-scoped one), using the context deadline verbatim would leave the read
// effectively unbounded and let a single unresponsive backend stall the client.
// A zero defaultTimeout means "no cap, defer entirely to the context".
// Returns the deadline that was set (zero if no deadline).
func (c *Connection) setDeadline(ctx context.Context) (time.Time, error) {
	var deadline time.Time

	if c.defaultTimeout > 0 {
		deadline = time.Now().Add(c.defaultTimeout)
	}

	// A context deadline that is sooner than the default-timeout cap wins; a
	// later one is capped at now+defaultTimeout.
	if ctxDeadline, ok := ctx.Deadline(); ok {
		if deadline.IsZero() || ctxDeadline.Before(deadline) {
			deadline = ctxDeadline
		}
	}

	// Set deadline on connection (zero deadline clears it)
	if err := c.conn.SetDeadline(deadline); err != nil {
		return time.Time{}, err
	}

	return deadline, nil
}

// Execute implements the Executor interface.
// Executes a single request and returns the response.
// The deadline is the earlier of the context deadline and now+defaultTimeout.
func (c *Connection) Execute(ctx context.Context, req *meta.Request) (*meta.Response, error) {
	// Set deadline from context or default timeout
	if _, err := c.setDeadline(ctx); err != nil {
		return nil, err
	}
	// Clear deadline when done to avoid stale deadlines when connection is reused from pool
	defer c.conn.SetDeadline(time.Time{})

	// Write request to buffered writer
	if err := meta.WriteRequest(c.Writer, req); err != nil {
		return nil, err
	}

	// Flush the buffered writer
	if err := c.Writer.Flush(); err != nil {
		return nil, err
	}

	var resp meta.Response
	if err := meta.ReadResponse(c.Reader, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ExecuteBatch implements the BatchExecutor interface.
// Executes multiple requests in a pipeline using the NoOp marker strategy.
// Sends all requests followed by a NoOp command, then reads responses until the NoOp response.
//
// Returns responses in the same order as requests.
// Individual request errors are captured in Response.Error (protocol errors).
// I/O errors or connection failures are returned as Go errors.
//
// If no request uses the quiet flag, the response count is guaranteed to match
// the request count; a mismatch is reported as an error since it means the
// connection is desynchronized. With quiet requests, nominal responses are
// suppressed by the server, so fewer responses than requests may be returned
// and the caller must correlate them (e.g. with opaque tokens).
//
// Deadline handling: The deadline is extended before reading each response to prevent
// timeout due to cumulative time across multiple responses (inspired by Grafana PR #16).
func (c *Connection) ExecuteBatch(ctx context.Context, reqs []*meta.Request) ([]*meta.Response, error) {
	if len(reqs) == 0 {
		return nil, nil
	}

	// Validate all keys before writing anything, so a rejected request cannot
	// leave earlier requests of the batch sitting in the write buffer.
	hasQuiet := false
	for _, req := range reqs {
		if req.Command != meta.CmdNoOp && req.Command != meta.CmdStats {
			if err := meta.ValidateKey(req.Key, req.HasFlag(meta.FlagBase64Key)); err != nil {
				return nil, err
			}
		}
		if req.HasFlag(meta.FlagQuiet) {
			hasQuiet = true
		}
	}

	// Set initial deadline for writing all requests
	if _, err := c.setDeadline(ctx); err != nil {
		return nil, err
	}
	// Clear deadline when done to avoid stale deadlines when connection is reused from pool
	defer c.conn.SetDeadline(time.Time{})

	// Write all requests
	for _, req := range reqs {
		if err := meta.WriteRequest(c.Writer, req); err != nil {
			return nil, err
		}
	}

	// Write NoOp marker to signal end of batch
	noopReq := meta.NewRequest(meta.CmdNoOp, "", nil)
	if err := meta.WriteRequest(c.Writer, noopReq); err != nil {
		return nil, err
	}

	// Flush all writes
	if err := c.Writer.Flush(); err != nil {
		return nil, err
	}

	// Read responses until the NoOp marker. Protocol errors (stored in
	// Response.Error) do not stop the loop: the server keeps processing the
	// pipelined requests that follow, and stopping early would leave their
	// responses unread on the connection.
	responses := make([]*meta.Response, 0, len(reqs))

	for {
		// Extend deadline before each read to prevent cumulative timeout
		// This is critical for large batches - each response gets a full timeout window
		if _, err := c.setDeadline(ctx); err != nil {
			return responses, err
		}

		var resp meta.Response
		if err := meta.ReadResponse(c.Reader, &resp); err != nil {
			// Return responses collected so far
			return responses, err
		}

		// Stop when we hit the NoOp marker (not part of the results)
		if resp.Status == meta.StatusMN {
			break
		}

		responses = append(responses, &resp)

		if len(responses) > len(reqs) {
			return responses, &meta.ParseError{Message: "received more responses than requests in batch"}
		}
	}

	if !hasQuiet && len(responses) != len(reqs) {
		return responses, &meta.ParseError{
			Message: fmt.Sprintf("received %d responses for %d requests in batch", len(responses), len(reqs)),
		}
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
	// Clear deadline when done to avoid stale deadlines when connection is reused from pool
	defer c.conn.SetDeadline(time.Time{})

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
// The check is bounded by the earlier of the context deadline and the
// connection's default timeout.
func (c *Connection) Ping(ctx context.Context) error {
	req := meta.NewRequest(meta.CmdNoOp, "", nil)

	resp, err := c.Execute(ctx, req)
	if err != nil {
		return err
	}

	if resp.Status != meta.StatusMN {
		return fmt.Errorf("health check failed: %s", resp.Status)
	}

	return nil
}
