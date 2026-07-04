package meta

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// failingWriter fails after writing n bytes successfully.
type failingWriter struct {
	remaining int
}

var errWriteFailed = errors.New("write failed")

func (w *failingWriter) Write(p []byte) (int, error) {
	if len(p) > w.remaining {
		n := w.remaining
		w.remaining = 0
		return n, errWriteFailed
	}
	w.remaining -= len(p)
	return len(p), nil
}

func TestWriteRequest_Stats(t *testing.T) {
	t.Run("without args", func(t *testing.T) {
		var buf bytes.Buffer
		err := WriteRequest(&buf, &Request{Command: CmdStats})
		if err != nil {
			t.Fatalf("WriteRequest failed: %v", err)
		}
		if got := buf.String(); got != "stats\r\n" {
			t.Errorf("wire = %q, want %q", got, "stats\r\n")
		}
	})

	t.Run("with args", func(t *testing.T) {
		var buf bytes.Buffer
		err := WriteRequest(&buf, &Request{Command: CmdStats, Key: "items"})
		if err != nil {
			t.Fatalf("WriteRequest failed: %v", err)
		}
		if got := buf.String(); got != "stats items\r\n" {
			t.Errorf("wire = %q, want %q", got, "stats items\r\n")
		}
	})
}

func TestValidateRequest(t *testing.T) {
	duplicateOpaque := NewRequest(CmdGet, "key", nil).
		AddOpaque("ok").
		AddOpaque(strings.Repeat("x", MaxOpaqueLength+1))

	tests := []struct {
		name    string
		req     *Request
		wantErr bool
	}{
		{name: "ordinary request", req: NewRequest(CmdGet, "key", nil)},
		{name: "stats argument with spaces", req: &Request{Command: CmdStats, Key: "cachedump 1 10"}},
		{name: "32-byte opaque", req: NewRequest(CmdGet, "key", nil).AddOpaque(strings.Repeat("x", MaxOpaqueLength))},
		{name: "stats carriage return", req: &Request{Command: CmdStats, Key: "items\rflush_all"}, wantErr: true},
		{name: "stats newline", req: &Request{Command: CmdStats, Key: "items\nflush_all"}, wantErr: true},
		{name: "opaque carriage return", req: NewRequest(CmdGet, "key", nil).AddOpaque("tok\rmn"), wantErr: true},
		{name: "opaque newline", req: NewRequest(CmdGet, "key", nil).AddOpaque("tok\nmn"), wantErr: true},
		{name: "custom mode CRLF", req: NewRequest(CmdSet, "key", nil).AddMode("S\r\nflush_all"), wantErr: true},
		{name: "33-byte opaque", req: NewRequest(CmdGet, "key", nil).AddOpaque(strings.Repeat("x", MaxOpaqueLength+1)), wantErr: true},
		{name: "second opaque too long", req: duplicateOpaque, wantErr: true},
		{name: "key with space", req: NewRequest(CmdGet, "bad key", nil), wantErr: true},
		// The base64 flag must not bypass CR/LF rejection: the encoded form is
		// what travels on the wire, and valid base64 contains no whitespace.
		{name: "base64 key CRLF", req: NewRequest(CmdGet, "abc\r\nmn", nil).AddBase64Key(), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRequest(tt.req)
			if !tt.wantErr {
				if err != nil {
					t.Errorf("ValidateRequest() unexpected error: %v", err)
				}
				return
			}

			var invalidRequest *InvalidRequestError
			if !errors.As(err, &invalidRequest) {
				t.Errorf("ValidateRequest() error = %v, want InvalidRequestError", err)
			}
		})
	}
}

func TestWriteRequest_InvalidRequestWritesNothing(t *testing.T) {
	tests := []struct {
		name string
		req  *Request
	}{
		{name: "stats carriage return", req: &Request{Command: CmdStats, Key: "items\rflush_all"}},
		{name: "stats newline", req: &Request{Command: CmdStats, Key: "items\nflush_all"}},
		{name: "opaque carriage return", req: NewRequest(CmdGet, "key", nil).AddOpaque("tok\rmn")},
		{name: "opaque newline", req: NewRequest(CmdGet, "key", nil).AddOpaque("tok\nmn")},
		{name: "custom mode CRLF", req: NewRequest(CmdSet, "key", nil).AddMode("S\r\nflush_all")},
		{name: "opaque too long", req: NewRequest(CmdGet, "key", nil).AddOpaque(strings.Repeat("x", MaxOpaqueLength+1))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := WriteRequest(&buf, tt.req)
			var invalidRequest *InvalidRequestError
			if !errors.As(err, &invalidRequest) {
				t.Errorf("WriteRequest() error = %v, want InvalidRequestError", err)
			}
			if buf.Len() != 0 {
				t.Errorf("WriteRequest() wrote %q for invalid request", buf.String())
			}
		})
	}
}

func TestWriteRequest_SetWithEmptyData(t *testing.T) {
	var buf bytes.Buffer
	err := WriteRequest(&buf, NewRequest(CmdSet, "key", nil))
	if err != nil {
		t.Fatalf("WriteRequest failed: %v", err)
	}
	if got := buf.String(); got != "ms key 0\r\n\r\n" {
		t.Errorf("wire = %q, want %q", got, "ms key 0\r\n\r\n")
	}
}

func TestWriteRequest_WriteErrors(t *testing.T) {
	tests := []struct {
		name      string
		req       *Request
		remaining int // bytes the writer accepts before failing
	}{
		{name: "header write fails", req: NewRequest(CmdSet, "key", []byte("hello")), remaining: 0},
		{name: "data write fails", req: NewRequest(CmdSet, "key", []byte("hello")), remaining: len("ms key 5\r\n")},
		{name: "terminator write fails", req: NewRequest(CmdSet, "key", []byte("hello")), remaining: len("ms key 5\r\nhello")},
		{name: "noop write fails", req: NewRequest(CmdNoOp, "", nil), remaining: 0},
		{name: "stats write fails", req: &Request{Command: CmdStats}, remaining: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := WriteRequest(&failingWriter{remaining: tt.remaining}, tt.req)
			if !errors.Is(err, errWriteFailed) {
				t.Errorf("error = %v, want errWriteFailed", err)
			}
		})
	}
}

// A request with very large flags must not be a problem (and exercises the
// buffer pool's drop-oversized-buffers path).
func TestWriteRequest_LargeFlags(t *testing.T) {
	req := NewRequest(CmdGet, "key", nil)
	for range 2048 {
		req.AddQuiet()
	}

	var buf bytes.Buffer
	if err := WriteRequest(&buf, req); err != nil {
		t.Fatalf("WriteRequest failed: %v", err)
	}
	if !strings.HasPrefix(buf.String(), "mg key q q") {
		t.Errorf("wire = %q..., want prefix %q", buf.String()[:20], "mg key q q")
	}
}

func TestResponse_TypedGetters_InvalidTokens(t *testing.T) {
	resp := responseWithFlags(" tabc sxyz hX l?? f-1")

	if _, ok := resp.TTL(); ok {
		t.Error("TTL with non-numeric token must return false")
	}
	if _, ok := resp.Size(); ok {
		t.Error("Size with non-numeric token must return false")
	}
	if v, ok := resp.Hit(); !ok || v {
		t.Error("Hit with unexpected token must return false value")
	}
	if _, ok := resp.LastAccess(); ok {
		t.Error("LastAccess with non-numeric token must return false")
	}
	if _, ok := resp.ClientFlags(); ok {
		t.Error("ClientFlags with negative token must return false")
	}
}
