//go:build unix

package memcache

import (
	"errors"
	"io"
	"net"
	"syscall"
)

// rawConnCheck does a non-blocking one-byte read on the raw socket to decide
// whether an idle connection is still alive. The runtime keeps the fd in
// non-blocking mode, so the read returns immediately:
//
//   - EAGAIN/EWOULDBLOCK: nothing to read, peer still connected -> healthy (nil)
//   - 0 bytes, no error:  peer sent FIN -> io.EOF (dead)
//   - >0 bytes:           unsolicited data on an idle connection -> desync
//   - any other error:    the socket is broken -> that error
//
// Connections that don't expose a raw fd (a *tls.Conn, or a test fake) can't be
// peeked and are reported healthy; a dead one is then only caught on the next
// real operation. This mirrors go-redis's connCheck.
func rawConnCheck(conn net.Conn) error {
	sysConn, ok := conn.(syscall.Conn)
	if !ok {
		return nil
	}
	rawConn, err := sysConn.SyscallConn()
	if err != nil {
		return err
	}

	var readErr error
	ctrlErr := rawConn.Read(func(fd uintptr) bool {
		var buf [1]byte
		n, err := syscall.Read(int(fd), buf[:])
		switch {
		case n == 0 && err == nil:
			readErr = io.EOF
		case n > 0:
			readErr = errUnexpectedRead
		case errors.Is(err, syscall.EAGAIN), errors.Is(err, syscall.EWOULDBLOCK):
			readErr = nil
		default:
			readErr = err
		}
		// Returning true stops Read from waiting for readability: the syscall
		// above is a single non-blocking attempt, which is the whole point.
		return true
	})
	if ctrlErr != nil {
		return ctrlErr
	}
	return readErr
}
