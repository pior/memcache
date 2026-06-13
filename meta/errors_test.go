package meta

import (
	"errors"
	"fmt"
	"io"
	"testing"
)

func TestErrorTypes(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantMessage string
		wantClose   bool
	}{
		{
			name:        "ClientError",
			err:         &ClientError{Message: "bad data chunk"},
			wantMessage: "CLIENT_ERROR: bad data chunk",
			wantClose:   true,
		},
		{
			name:        "ServerError",
			err:         &ServerError{Message: "out of memory"},
			wantMessage: "SERVER_ERROR: out of memory",
			wantClose:   false,
		},
		{
			name:        "GenericError",
			err:         &GenericError{Message: "ERROR"},
			wantMessage: "ERROR",
			wantClose:   true,
		},
		{
			name:        "InvalidKeyError",
			err:         &InvalidKeyError{Message: "key is empty"},
			wantMessage: "key is empty",
			wantClose:   false,
		},
		{
			name:        "ParseError",
			err:         &ParseError{Message: "bad line"},
			wantMessage: "parse error: bad line",
			wantClose:   true,
		},
		{
			name:        "ParseError with underlying error",
			err:         &ParseError{Message: "bad size", Err: errors.New("strconv")},
			wantMessage: "parse error: bad size: strconv",
			wantClose:   true,
		},
		{
			name:        "ConnectionError",
			err:         &ConnectionError{Op: "read", Err: io.EOF},
			wantMessage: "connection error during read: EOF",
			wantClose:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.wantMessage {
				t.Errorf("Error() = %q, want %q", got, tt.wantMessage)
			}
			if got := ShouldCloseConnection(tt.err); got != tt.wantClose {
				t.Errorf("ShouldCloseConnection() = %v, want %v", got, tt.wantClose)
			}
		})
	}
}

func TestErrorUnwrap(t *testing.T) {
	t.Run("ParseError", func(t *testing.T) {
		underlying := errors.New("boom")
		err := &ParseError{Message: "bad", Err: underlying}
		if !errors.Is(err, underlying) {
			t.Error("errors.Is must reach the underlying error")
		}
	})

	t.Run("ConnectionError", func(t *testing.T) {
		err := &ConnectionError{Op: "write", Err: io.ErrClosedPipe}
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Error("errors.Is must reach the underlying error")
		}
	})
}

func TestShouldCloseConnection_SpecialCases(t *testing.T) {
	t.Run("nil error", func(t *testing.T) {
		if ShouldCloseConnection(nil) {
			t.Error("ShouldCloseConnection(nil) = true, want false")
		}
	})

	t.Run("unknown error is conservatively closed", func(t *testing.T) {
		if !ShouldCloseConnection(errors.New("some I/O failure")) {
			t.Error("ShouldCloseConnection(unknown) = false, want true")
		}
	})

	t.Run("wrapped protocol error is found through the chain", func(t *testing.T) {
		wrapped := fmt.Errorf("operation failed: %w", &ServerError{Message: "busy"})
		if ShouldCloseConnection(wrapped) {
			t.Error("wrapped ServerError must not require closing")
		}
	})
}
