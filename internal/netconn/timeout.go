package netconn

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"time"
)

// AttributeIOTimeout preserves the socket timeout while adding the caller's
// context error when that context supplied the binding deadline. This lets
// callers and circuit breakers distinguish a caller-imposed budget from the
// connection's operator-configured timeout.
func AttributeIOTimeout(ctx context.Context, effectiveDeadline time.Time, err error) error {
	ctxDeadline, hasContextDeadline := ctx.Deadline()
	if !hasContextDeadline || !ctxDeadline.Equal(effectiveDeadline) {
		return err
	}

	var netErr net.Error
	if !errors.Is(err, os.ErrDeadlineExceeded) && (!errors.As(err, &netErr) || !netErr.Timeout()) {
		return err
	}

	ctxErr := ctx.Err()
	if ctxErr == nil {
		// The socket and context timers share the same deadline, but the socket
		// timeout can be observed just before the context publishes its error.
		ctxErr = context.DeadlineExceeded
	}

	return fmt.Errorf("%w (%w)", ctxErr, err)
}
