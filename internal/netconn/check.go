// Package netconn holds protocol-agnostic helpers for net.Conn: idle
// connection liveness checks and I/O timeout attribution.
package netconn

import "errors"

// ErrUnexpectedRead is returned by CheckIdle when an idle connection has bytes
// waiting to be read. On a request/response protocol whose server sends nothing
// unsolicited, this means the peer pushed data or a previous response was
// under-consumed (protocol desync); either way the connection is not safe to
// reuse.
var ErrUnexpectedRead = errors.New("netconn: unexpected data on idle connection")
