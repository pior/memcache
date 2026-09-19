package memcache

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"time"

	"github.com/pior/memcache/internal/netconn"
	"github.com/pior/memcache/meta"
)

// NewConnection creates a connection with a per-operation timeout.
// The timeout is a per-operation upper bound: each operation's deadline is the
// earlier of the context deadline and now+timeout (see setDeadline). The cap
// cannot be disabled — a non-positive timeout selects defaultOperationTimeout,
// so a Connection is never left unbounded against a hung-but-connected peer;
// pass a large explicit value when a long budget is genuinely needed.
func NewConnection(conn net.Conn, timeout time.Duration) *Connection {
	if timeout <= 0 {
		timeout = defaultOperationTimeout
	}
	return &Connection{
		conn: conn,
		// The reader's buffer size bounds response line reads (see meta.MaxLineSize).
		Reader:         bufio.NewReaderSize(conn, meta.MaxLineSize),
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
	// even a context that has a later (or no) deadline. Always positive:
	// NewConnection resolves non-positive values to defaultOperationTimeout.
	defaultTimeout time.Duration

	// response is the connection-owned destination for single-request Execute
	// calls. Decoding every response into it lets ReadResponse reuse the Data
	// and Flags buffers across operations. It is safe because a connection
	// serves one operation at a time and Execute only exposes it to fn
	// while the operation is in flight.
	response meta.Response
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
// An operation issued with an already-canceled context is rejected before any
// byte is written. Returns the deadline that was set (always non-zero:
// defaultTimeout is always positive).
func (c *Connection) setDeadline(ctx context.Context) (time.Time, error) {
	if err := ctx.Err(); err != nil {
		return time.Time{}, err
	}

	deadline := time.Now().Add(c.defaultTimeout)

	// A context deadline that is sooner than the default-timeout cap wins; a
	// later one is capped at now+defaultTimeout.
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}

	if err := c.conn.SetDeadline(deadline); err != nil {
		return time.Time{}, err
	}

	return deadline, nil
}

// Execute implements the Executor interface.
// Executes a single request and calls fn with the decoded response.
// The deadline is the earlier of the context deadline and now+defaultTimeout.
//
// The response passed to fn is owned by the connection and reused by the
// next Execute call: it and its Data/Flags storage are only valid until fn
// returns. fn must clone anything it retains.
func (c *Connection) Execute(ctx context.Context, req *meta.Request, fn ResponseFunc) error {
	// Set deadline from context or default timeout
	deadline, err := c.setDeadline(ctx)
	if err != nil {
		return err
	}
	// Clear deadline when done to avoid stale deadlines when connection is reused from pool
	defer c.conn.SetDeadline(time.Time{})

	// Write request to buffered writer
	if err := meta.WriteRequest(c.Writer, req); err != nil {
		return netconn.AttributeIOTimeout(ctx, deadline, err)
	}

	// Flush the buffered writer
	if err := c.Writer.Flush(); err != nil {
		return netconn.AttributeIOTimeout(ctx, deadline, err)
	}

	if err := meta.ReadResponse(c.Reader, &c.response); err != nil {
		return netconn.AttributeIOTimeout(ctx, deadline, err)
	}
	fn(&c.response)
	return nil
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

	// Validate all requests before writing anything, so a rejected request cannot
	// leave earlier requests of the batch sitting in the write buffer.
	hasQuiet := false
	for _, req := range reqs {
		if err := meta.ValidateRequest(req); err != nil {
			return nil, err
		}
		if req.HasFlag(meta.FlagQuiet) {
			hasQuiet = true
		}
	}

	// Set initial deadline for writing all requests
	deadline, err := c.setDeadline(ctx)
	if err != nil {
		return nil, err
	}
	// Clear deadline when done to avoid stale deadlines when connection is reused from pool
	defer c.conn.SetDeadline(time.Time{})

	// Write all requests
	for _, req := range reqs {
		if err := meta.WriteRequest(c.Writer, req); err != nil {
			return nil, netconn.AttributeIOTimeout(ctx, deadline, err)
		}
	}

	// Write NoOp marker to signal end of batch
	noopReq := meta.NewRequest(meta.CmdNoOp, "", nil)
	if err := meta.WriteRequest(c.Writer, noopReq); err != nil {
		return nil, netconn.AttributeIOTimeout(ctx, deadline, err)
	}

	// Flush all writes
	if err := c.Writer.Flush(); err != nil {
		return nil, netconn.AttributeIOTimeout(ctx, deadline, err)
	}

	// Read responses until the NoOp marker. Protocol errors (stored in
	// Response.Error) do not stop the loop: the server keeps processing the
	// pipelined requests that follow, and stopping early would leave their
	// responses unread on the connection.
	responses := make([]*meta.Response, 0, len(reqs))

	for {
		// Extend deadline before each read to prevent cumulative timeout
		// This is critical for large batches - each response gets a full timeout window
		deadline, err = c.setDeadline(ctx)
		if err != nil {
			return responses, err
		}

		var resp meta.Response
		if err := meta.ReadResponse(c.Reader, &resp); err != nil {
			// Return responses collected so far
			return responses, netconn.AttributeIOTimeout(ctx, deadline, err)
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
	deadline, err := c.setDeadline(ctx)
	if err != nil {
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
		return nil, netconn.AttributeIOTimeout(ctx, deadline, err)
	}

	// Flush the buffered writer
	if err := c.Writer.Flush(); err != nil {
		return nil, netconn.AttributeIOTimeout(ctx, deadline, err)
	}

	// Read stats response
	stats, err := meta.ReadStatsResponse(c.Reader)
	if err != nil {
		return nil, netconn.AttributeIOTimeout(ctx, deadline, err)
	}

	return stats, nil
}

// Ping performs a simple health check on a connection using the noop command.
// The check is bounded by the earlier of the context deadline and the
// connection's default timeout.
func (c *Connection) Ping(ctx context.Context) error {
	req := meta.NewRequest(meta.CmdNoOp, "", nil)

	var status meta.StatusType
	if err := c.Execute(ctx, req, func(resp *meta.Response) { status = resp.Status }); err != nil {
		return err
	}
	if status != meta.StatusMN {
		return fmt.Errorf("health check failed: %s", status)
	}
	return nil
}

// checkAlive reports whether an idle pooled connection is still usable, without
// issuing a command or blocking. It returns nil for a healthy connection and an
// error for one that must be discarded.
//
// The check is only meaningful between operations, when the memcache protocol
// guarantees the server sends nothing unsolicited: any readable byte then means
// the peer closed the connection (EOF), reset it, or left protocol garbage
// behind. It first rejects a connection with buffered, undrained bytes, then
// does a non-blocking one-byte peek on the raw socket (see netconn.CheckIdle).
//
// It cannot see through TLS (the raw bytes are encrypted) and platforms without
// syscall.Conn support skip the peek, so on those a dead idle connection is
// still only detected on the next real operation.
func (c *Connection) checkAlive() error {
	// Leftover buffered bytes mean the previous response was not fully drained:
	// the connection is desynchronized and must not be handed out again. This
	// peek bypasses the bufio.Reader, so it has to be checked separately.
	if c.Reader.Buffered() > 0 {
		return netconn.ErrUnexpectedRead
	}
	return netconn.CheckIdle(c.conn)
}
