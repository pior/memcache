package netconn

import (
	"context"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// netError is a net.Error that is not os.ErrDeadlineExceeded.
type netError struct{ timeout bool }

func (e netError) Error() string   { return "net error" }
func (e netError) Timeout() bool   { return e.timeout }
func (e netError) Temporary() bool { return false }

func TestAttributeIOTimeout(t *testing.T) {
	// The deadline the connection set for the operation.
	deadline := time.Now().Add(time.Hour)

	withDeadline := func(d time.Time) context.Context {
		ctx, cancel := context.WithDeadline(context.Background(), d)
		t.Cleanup(cancel)
		return ctx
	}
	canceled := func() context.Context {
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		cancel()
		return ctx
	}

	opTimeout := &net.OpError{Op: "read", Net: "tcp", Err: os.ErrDeadlineExceeded}

	t.Run("returned unchanged", func(t *testing.T) {
		tests := []struct {
			name string
			ctx  context.Context
			err  error
		}{
			{"context has no deadline", context.Background(), opTimeout},
			{"connection timeout was the earlier deadline", withDeadline(deadline.Add(time.Second)), opTimeout},
			{"not a net error", withDeadline(deadline), io.EOF},
			{"net error that is not a timeout", withDeadline(deadline), netError{timeout: false}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				require.Equal(t, tt.err, AttributeIOTimeout(tt.ctx, deadline, tt.err))
			})
		}
	})

	t.Run("attributed to the context", func(t *testing.T) {
		tests := []struct {
			name       string
			ctx        context.Context
			err        error
			want       string
			wantCtxErr error
		}{
			{
				// The socket timer fired before the context published its error.
				name:       "context error not yet published",
				ctx:        withDeadline(deadline),
				err:        os.ErrDeadlineExceeded,
				want:       "context deadline exceeded (i/o timeout)",
				wantCtxErr: context.DeadlineExceeded,
			},
			{
				name:       "deadline exceeded wrapped in an OpError",
				ctx:        withDeadline(deadline),
				err:        opTimeout,
				want:       "context deadline exceeded (read tcp: i/o timeout)",
				wantCtxErr: context.DeadlineExceeded,
			},
			{
				name:       "net error that is a timeout",
				ctx:        withDeadline(deadline),
				err:        netError{timeout: true},
				want:       "context deadline exceeded (net error)",
				wantCtxErr: context.DeadlineExceeded,
			},
			{
				name:       "context error is kept",
				ctx:        canceled(),
				err:        opTimeout,
				want:       "context canceled (read tcp: i/o timeout)",
				wantCtxErr: context.Canceled,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				got := AttributeIOTimeout(tt.ctx, deadline, tt.err)
				require.Equal(t, tt.want, got.Error())
				require.ErrorIs(t, got, tt.wantCtxErr)
				require.ErrorIs(t, got, tt.err)
			})
		}
	})
}
