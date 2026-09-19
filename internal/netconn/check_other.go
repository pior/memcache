//go:build !unix

package netconn

import "net"

// CheckIdle is a no-op on platforms without a portable non-blocking raw-fd
// read (e.g. Windows, js/wasm). Idle connections are trusted at checkout there,
// so a connection that died while idle is only detected on the next operation.
func CheckIdle(conn net.Conn) error {
	return nil
}
