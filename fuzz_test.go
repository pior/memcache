package memcache

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"
)

// fuzzConn is a mock connection that allows us to inject arbitrary data
type fuzzConn struct {
	readData    []byte
	readPos     int
	writeBuffer bytes.Buffer
	closed      bool
	readError   error
	writeError  error
}

func (fc *fuzzConn) Read(b []byte) (n int, err error) {
	if fc.readError != nil {
		return 0, fc.readError
	}
	if fc.readPos >= len(fc.readData) {
		return 0, io.EOF
	}
	n = copy(b, fc.readData[fc.readPos:])
	fc.readPos += n
	return n, nil
}

func (fc *fuzzConn) Write(b []byte) (n int, err error) {
	if fc.writeError != nil {
		return 0, fc.writeError
	}
	return fc.writeBuffer.Write(b)
}

func (fc *fuzzConn) Close() error {
	fc.closed = true
	return nil
}

func (fc *fuzzConn) LocalAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 12345}
}

func (fc *fuzzConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 11211}
}

func (fc *fuzzConn) SetDeadline(t time.Time) error {
	return nil
}

func (fc *fuzzConn) SetReadDeadline(t time.Time) error {
	return nil
}

func (fc *fuzzConn) SetWriteDeadline(t time.Time) error {
	return nil
}

func newFuzzConn(data []byte) *fuzzConn {
	return &fuzzConn{
		readData: data,
		readPos:  0,
	}
}

// FuzzReadResponse tests the readResponse function with arbitrary input data
func FuzzReadResponse(f *testing.F) {
	// Seed with some valid responses
	f.Add([]byte("HD\r\n"))
	f.Add([]byte("VA 5\r\nhello\r\n"))
	f.Add([]byte("EN\r\n"))
	f.Add([]byte("VA 0\r\n\r\n"))
	f.Add([]byte("HD c123 f456\r\n"))

	// Seed with some malformed responses
	f.Add([]byte("VA abc\r\nhello\r\n"))
	f.Add([]byte("VA 5\r\nhi"))
	f.Add([]byte("VA -1\r\n\r\n"))
	f.Add([]byte("\r\n"))
	f.Add([]byte("INVALID"))

	f.Fuzz(func(t *testing.T, data []byte) {
		// Skip empty input as it's expected to return EOF
		if len(data) == 0 {
			return
		}

		fc := newFuzzConn(data)
		conn := NewConn(fc)

		// readResponse should never panic
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("readResponse panicked with input %q: %v", data, r)
			}
		}()

		code, args, responseData, err := conn.readResponse()

		// Validate that we don't have inconsistent state
		if err == nil {
			// If no error, we should have at least a code
			if code == "" {
				t.Errorf("readResponse returned no error but empty code for input %q", data)
			}
		}

		// Response data should never be nil when we have a VA response without error
		if err == nil && code == "VA" && responseData == nil && len(args) > 0 {
			t.Errorf("readResponse returned nil data for successful VA response")
		}
	})
}

// FuzzMetaGet tests MetaGet with various server responses
func FuzzMetaGet(f *testing.F) {
	// Seed with valid responses
	f.Add([]byte("VA 5 v\r\nhello\r\n"))
	f.Add([]byte("EN\r\n"))
	f.Add([]byte("HD c123\r\n"))
	f.Add([]byte("VA 0 v\r\n\r\n"))

	// Seed with malformed responses
	f.Add([]byte("VA\r\n"))
	f.Add([]byte("VA abc\r\nhello\r\n"))
	f.Add([]byte("VA 5\r\nhi"))
	f.Add([]byte("INVALID RESPONSE\r\n"))

	f.Fuzz(func(t *testing.T, data []byte) {
		fc := newFuzzConn(data)
		conn := NewConn(fc)

		// MetaGet should never panic
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("MetaGet panicked with input %q: %v", data, r)
			}
		}()

		resp, err := conn.MetaGet("testkey", FlagReturnValue())

		// Validate response consistency
		if err == nil {
			// If no error, response should be valid
			if resp.Code == "" {
				t.Errorf("MetaGet returned no error but empty response code")
			}

			// For VA responses, data and size should be consistent
			if resp.Code == "VA" {
				if resp.Size < 0 {
					t.Errorf("MetaGet returned negative size for VA response: %d", resp.Size)
				}
			}
		}

		// TTL should be -1 if not set, or a valid value
		if resp.TTL < -1 {
			t.Errorf("MetaGet returned invalid TTL: %d", resp.TTL)
		}
	})
}

// FuzzMetaSet tests MetaSet with various server responses
func FuzzMetaSet(f *testing.F) {
	// Seed with valid responses
	f.Add([]byte("HD\r\n"))
	f.Add([]byte("NS\r\n"))
	f.Add([]byte("HD c123\r\n"))

	// Seed with malformed responses
	f.Add([]byte(""))
	f.Add([]byte("INVALID\r\n"))
	f.Add([]byte("HD c\r\n"))

	f.Fuzz(func(t *testing.T, data []byte) {
		fc := newFuzzConn(data)
		conn := NewConn(fc)

		// MetaSet should never panic
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("MetaSet panicked with input %q: %v", data, r)
			}
		}()

		resp, err := conn.MetaSet("testkey", []byte("testvalue"))

		// Validate response consistency
		if err == nil && resp.Code == "" {
			t.Errorf("MetaSet returned no error but empty response code")
		}

		// CAS should be a valid value (uint64 can't be negative anyway)
		// Just ensure it's accessible without panicking
		_ = resp.CAS

		// TTL should be -1 if not set, or a valid value
		if resp.TTL < -1 {
			t.Errorf("MetaSet returned invalid TTL: %d", resp.TTL)
		}
	})
}

// FuzzMetaDelete tests MetaDelete with various server responses
func FuzzMetaDelete(f *testing.F) {
	// Seed with valid responses
	f.Add([]byte("HD\r\n"))
	f.Add([]byte("NF\r\n"))
	f.Add([]byte("HD omytoken\r\n"))
	f.Add([]byte("HD c123 t300\r\n"))

	// Seed with malformed responses
	f.Add([]byte(""))
	f.Add([]byte("INVALID\r\n"))
	f.Add([]byte("HD c\r\n"))

	f.Fuzz(func(t *testing.T, data []byte) {
		fc := newFuzzConn(data)
		conn := NewConn(fc)

		// MetaDelete should never panic
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("MetaDelete panicked with input %q: %v", data, r)
			}
		}()

		resp, err := conn.MetaDelete("testkey")

		// Validate response consistency
		if err == nil && resp.Code == "" {
			t.Errorf("MetaDelete returned no error but empty response code")
		}

		// CAS should be a valid value (uint64 can't be negative anyway)
		// Just ensure it's accessible without panicking
		_ = resp.CAS

		// TTL should be -1 if not set, or a valid value
		if resp.TTL < -1 {
			t.Errorf("MetaDelete returned invalid TTL: %d", resp.TTL)
		}
	})
}

// FuzzMetaArithmetic tests MetaArithmetic with various server responses
func FuzzMetaArithmetic(f *testing.F) {
	// Seed with valid responses
	f.Add([]byte("VA 3 v\r\n123\r\n"))
	f.Add([]byte("NF\r\n"))
	f.Add([]byte("VA 0 v\r\n\r\n"))
	f.Add([]byte("HD\r\n"))
	f.Add([]byte("VA 10 v c123 t300\r\n1234567890\r\n"))

	// Seed with malformed responses
	f.Add([]byte("VA 3 v\r\nabc\r\n"))
	f.Add([]byte("VA 5 v\r\n12\r\n"))
	f.Add([]byte("VA abc v\r\n123\r\n"))
	f.Add([]byte("VA -5 v\r\n123\r\n"))

	f.Fuzz(func(t *testing.T, data []byte) {
		fc := newFuzzConn(data)
		conn := NewConn(fc)

		// MetaArithmetic should never panic
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("MetaArithmetic panicked with input %q: %v", data, r)
			}
		}()

		resp, err := conn.MetaArithmetic("testkey", FlagModeIncr(), FlagReturnValue())

		// Validate response consistency
		if err == nil {
			if resp.Code == "" {
				t.Errorf("MetaArithmetic returned no error but empty response code")
			}

			// For VA responses, size should be non-negative
			if resp.Code == "VA" && resp.Size < 0 {
				t.Errorf("MetaArithmetic returned negative size: %d", resp.Size)
			}
		}

		// TTL should be -1 if not set, or a valid value
		if resp.TTL < -1 {
			t.Errorf("MetaArithmetic returned invalid TTL: %d", resp.TTL)
		}
	})
}

// FuzzMetaNoop tests MetaNoop with various server responses
func FuzzMetaNoop(f *testing.F) {
	// Seed with valid responses
	f.Add([]byte("MN\r\n"))
	f.Add([]byte("HD\r\n"))
	f.Add([]byte("MN omytoken\r\n"))

	// Seed with malformed responses
	f.Add([]byte(""))
	f.Add([]byte("INVALID\r\n"))
	f.Add([]byte("MN c\r\n"))

	f.Fuzz(func(t *testing.T, data []byte) {
		fc := newFuzzConn(data)
		conn := NewConn(fc)

		// MetaNoop should never panic
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("MetaNoop panicked with input %q: %v", data, r)
			}
		}()

		resp, err := conn.MetaNoop()

		// Validate response consistency
		if err == nil && resp.Code == "" {
			t.Errorf("MetaNoop returned no error but empty response code")
		}

		// TTL should be -1 if not set, or a valid value
		if resp.TTL < -1 {
			t.Errorf("MetaNoop returned invalid TTL: %d", resp.TTL)
		}
	})
}

// TestFuzzSmoke runs a quick smoke test to ensure our fuzz tests work
func TestFuzzSmoke(t *testing.T) {
	t.Run("readResponse", func(t *testing.T) {
		fc := newFuzzConn([]byte("HD\r\n"))
		conn := NewConn(fc)
		code, args, data, err := conn.readResponse()
		if err != nil || code != "HD" || len(args) != 0 || data != nil {
			t.Errorf("Smoke test failed: code=%q, args=%v, data=%q, err=%v", code, args, data, err)
		}
	})
}
