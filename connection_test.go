package memcache

import (
	"bytes"
	"io"
	"net"
	"os"
	"reflect"
	"testing"
	"time"
)

// mockNetConn is a mock implementation of net.Conn for testing.
type mockNetConn struct {
	readBuffer         bytes.Buffer
	writeBuffer        bytes.Buffer
	simulateWriteError bool // Added to simulate write errors
}

func (m *mockNetConn) Read(b []byte) (n int, err error) {
	return m.readBuffer.Read(b)
}

func (m *mockNetConn) Write(b []byte) (n int, err error) {
	if m.simulateWriteError {
		return 0, io.ErrShortWrite // Simulate a generic write error
	}
	return m.writeBuffer.Write(b)
}

func (m *mockNetConn) Close() error {
	return nil
}

func (m *mockNetConn) LocalAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 12345}
}

func (m *mockNetConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 11211}
}

func (m *mockNetConn) SetDeadline(t time.Time) error {
	return nil
}

func (m *mockNetConn) SetReadDeadline(t time.Time) error {
	return nil
}

func (m *mockNetConn) SetWriteDeadline(t time.Time) error {
	return nil
}

func newTestConn(serverResponse string) (*Conn, *mockNetConn) { // Uses *Conn directly
	mock := &mockNetConn{}
	mock.readBuffer.WriteString(serverResponse)
	conn := NewConn(mock) // Uses NewConn directly
	return conn, mock
}

func TestMetaGet(t *testing.T) {
	tests := []struct {
		name             string
		key              string
		flags            []MetaFlag // Uses MetaFlag directly
		serverResponse   string
		expectedCmd      string
		expectedCode     string
		expectedData     []byte
		expectedSize     int
		expectedCAS      uint64
		expectedFlags    uint32
		expectedTTL      int
		expectedOpaque   string
		expectedKey      string
		expectErr        bool
		skipResponseTest bool // For cases where error occurs before response read
	}{
		{
			name:           "simple get",
			key:            "mykey",
			flags:          []MetaFlag{FlagReturnValue()},
			serverResponse: "VA 5 v\r\nvalue\r\n",
			expectedCmd:    "mg mykey v\r\n",
			expectedCode:   "VA",
			expectedData:   []byte("value"),
			expectedSize:   5,
			expectedTTL:    -1,
		},
		{
			name:           "get with CAS",
			key:            "another",
			flags:          []MetaFlag{FlagReturnValue(), FlagReturnCAS()},
			serverResponse: "VA 6 v c123\r\nmydata\r\n",
			expectedCmd:    "mg another v c\r\n",
			expectedCode:   "VA",
			expectedData:   []byte("mydata"),
			expectedSize:   6,
			expectedCAS:    123,
			expectedTTL:    -1,
		},
		{
			name:           "get miss (EN)",
			key:            "misskey",
			flags:          []MetaFlag{FlagReturnValue()},
			serverResponse: "EN\r\n",
			expectedCmd:    "mg misskey v\r\n",
			expectedCode:   "EN",
			expectedData:   nil,
			expectedTTL:    -1,
		},
		{
			name:           "get hit no value (HD)",
			key:            "keynoval",
			flags:          []MetaFlag{FlagReturnCAS()},
			serverResponse: "HD c789\r\n",
			expectedCmd:    "mg keynoval c\r\n",
			expectedCode:   "HD",
			expectedData:   nil,
			expectedCAS:    789,
			expectedTTL:    -1,
		},
		{
			name:             "network error on send",
			key:              "testkey",
			flags:            []MetaFlag{FlagReturnValue()},
			serverResponse:   "",
			expectedCmd:      "mg testkey v\r\n",
			expectErr:        true,
			skipResponseTest: true,
		},
		{
			name:           "network error on read",
			key:            "readerr",
			flags:          []MetaFlag{FlagReturnValue()},
			serverResponse: "VA 5 v\r\nval", // Incomplete response
			expectedCmd:    "mg readerr v\r\n",
			expectedCode:   "VA",
			expectedData:   []byte("val"), // Expect partial data
			expectErr:      true,          // Expecting io.EOF or similar due to incomplete read
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, mock := newTestConn(tt.serverResponse)
			if tt.name == "network error on send" {
				mock.simulateWriteError = true
			}

			resp, err := conn.MetaGet(tt.key, tt.flags...)

			if tt.expectErr {
				if err == nil {
					t.Errorf("MetaGet() expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("MetaGet() unexpected error: %v", err)
				}
			}

			// Verify command sent
			writtenCmd := mock.writeBuffer.String()
			if !(tt.name == "network error on send" && tt.expectErr && err != nil) {
				if writtenCmd != tt.expectedCmd {
					t.Errorf("MetaGet() sent command = %q, want %q", writtenCmd, tt.expectedCmd)
				}
			}

			if tt.skipResponseTest {
				return
			}

			if resp.Code != tt.expectedCode {
				t.Errorf("MetaGet() code = %q, want %q", resp.Code, tt.expectedCode)
			}
			if !bytes.Equal(resp.Data, tt.expectedData) {
				t.Errorf("MetaGet() data = %q, want %q", resp.Data, tt.expectedData)
			}
			if resp.Size != tt.expectedSize {
				t.Errorf("MetaGet() size = %d, want %d", resp.Size, tt.expectedSize)
			}
			if resp.CAS != tt.expectedCAS {
				t.Errorf("MetaGet() CAS = %d, want %d", resp.CAS, tt.expectedCAS)
			}
			if resp.ClientFlags != tt.expectedFlags {
				t.Errorf("MetaGet() flags = %d, want %d", resp.ClientFlags, tt.expectedFlags)
			}
			if resp.TTL != tt.expectedTTL {
				t.Errorf("MetaGet() TTL = %d, want %d", resp.TTL, tt.expectedTTL)
			}
			if resp.Opaque != tt.expectedOpaque {
				t.Errorf("MetaGet() opaque = %q, want %q", resp.Opaque, tt.expectedOpaque)
			}
			if resp.Key != tt.expectedKey {
				t.Errorf("MetaGet() key = %q, want %q", resp.Key, tt.expectedKey)
			}
		})
	}
}

func TestMetaSet(t *testing.T) {
	tests := []struct {
		name           string
		key            string
		value          []byte
		flags          []MetaFlag // Uses MetaFlag directly
		serverResponse string
		expectedCmd    string // Includes datalen and data for set
		expectedCode   string
		expectedCAS    uint64
		expectedTTL    int
		expectErr      bool
	}{
		{
			name:           "simple set",
			key:            "setkey",
			value:          []byte("datatoset"),
			flags:          []MetaFlag{FlagSetTTL(300)},
			serverResponse: "HD T300\r\n",
			expectedCmd:    "ms setkey 9 T300\r\ndatatoset\r\n",
			expectedCode:   "HD",
			expectedTTL:    300,
		},
		{
			name:           "set with CAS return",
			key:            "setcas",
			value:          []byte("casval"),
			flags:          []MetaFlag{FlagReturnCAS()},
			serverResponse: "HD c456\r\n",
			expectedCmd:    "ms setcas 6 c\r\ncasval\r\n",
			expectedCode:   "HD",
			expectedCAS:    456,
			expectedTTL:    -1,
		},
		{
			name:           "set not stored (NS)",
			key:            "setns",
			value:          []byte("novalue"),
			flags:          []MetaFlag{FlagModeAdd()}, // e.g. add fails if key exists
			serverResponse: "NS\r\n",
			expectedCmd:    "ms setns 7 ME\r\nnovalue\r\n",
			expectedCode:   "NS",
			expectedTTL:    -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, mock := newTestConn(tt.serverResponse)
			resp, err := conn.MetaSet(tt.key, tt.value, tt.flags...)

			if tt.expectErr {
				if err == nil {
					t.Errorf("MetaSet() expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("MetaSet() unexpected error: %v", err)
				}
			}

			writtenCmd := mock.writeBuffer.String()
			if writtenCmd != tt.expectedCmd {
				t.Errorf("MetaSet() sent command = %q, want %q", writtenCmd, tt.expectedCmd)
			}
			if resp.Code != tt.expectedCode {
				t.Errorf("MetaSet() code = %q, want %q", resp.Code, tt.expectedCode)
			}
			if resp.CAS != tt.expectedCAS {
				t.Errorf("MetaSet() CAS = %d, want %d", resp.CAS, tt.expectedCAS)
			}
			if resp.TTL != tt.expectedTTL {
				t.Errorf("MetaSet() TTL = %d, want %d", resp.TTL, tt.expectedTTL)
			}
		})
	}
}

func TestMetaDelete(t *testing.T) {
	tests := []struct {
		name           string
		key            string
		flags          []MetaFlag // Uses MetaFlag directly
		serverResponse string
		expectedCmd    string
		expectedCode   string
		expectedOpaque string
		expectErr      bool
	}{
		{
			name:           "simple delete",
			key:            "delkey",
			flags:          []MetaFlag{},
			serverResponse: "HD\r\n",
			expectedCmd:    "md delkey\r\n",
			expectedCode:   "HD",
		},
		{
			name:           "delete not found (NF)",
			key:            "delnf",
			flags:          []MetaFlag{},
			serverResponse: "NF\r\n",
			expectedCmd:    "md delnf\r\n",
			expectedCode:   "NF",
		},
		{
			name:           "delete with opaque",
			key:            "delop",
			flags:          []MetaFlag{FlagOpaque("myop")},
			serverResponse: "HD omyop\r\n",
			expectedCmd:    "md delop Omyop\r\n",
			expectedCode:   "HD",
			expectedOpaque: "myop",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, mock := newTestConn(tt.serverResponse)
			resp, err := conn.MetaDelete(tt.key, tt.flags...)

			if tt.expectErr {
				if err == nil {
					t.Errorf("MetaDelete() expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("MetaDelete() unexpected error: %v", err)
				}
			}

			writtenCmd := mock.writeBuffer.String()
			if writtenCmd != tt.expectedCmd {
				t.Errorf("MetaDelete() sent command = %q, want %q", writtenCmd, tt.expectedCmd)
			}
			if resp.Code != tt.expectedCode {
				t.Errorf("MetaDelete() code = %q, want %q", resp.Code, tt.expectedCode)
			}
			if resp.Opaque != tt.expectedOpaque {
				t.Errorf("MetaDelete() opaque = %q, want %q", resp.Opaque, tt.expectedOpaque)
			}
		})
	}
}

func TestMetaArithmetic(t *testing.T) {
	tests := []struct {
		name           string
		key            string
		flags          []MetaFlag // Uses MetaFlag directly
		serverResponse string
		expectedCmd    string
		expectedCode   string
		expectedData   []byte
		expectedValue  uint64
		expectedSize   int
		expectErr      bool
	}{
		{
			name:           "simple incr (default delta 1)",
			key:            "inckey",
			flags:          []MetaFlag{FlagModeIncr(), FlagReturnValue()},
			serverResponse: "VA 1 v\r\n5\r\n",
			expectedCmd:    "ma inckey MI v\r\n",
			expectedCode:   "VA",
			expectedData:   []byte("5"),
			expectedValue:  5,
			expectedSize:   1,
		},
		{
			name:           "decr with delta",
			key:            "deckey",
			flags:          []MetaFlag{FlagModeDecr(), FlagDelta(10), FlagReturnValue()},
			serverResponse: "VA 2 v\r\n15\r\n",
			expectedCmd:    "ma deckey MD D10 v\r\n",
			expectedCode:   "VA",
			expectedData:   []byte("15"),
			expectedValue:  15,
			expectedSize:   2,
		},
		{
			name:           "arithmetic not found (NF)",
			key:            "arithnf",
			flags:          []MetaFlag{FlagModeIncr()},
			serverResponse: "NF\r\n",
			expectedCmd:    "ma arithnf MI\r\n",
			expectedCode:   "NF",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, mock := newTestConn(tt.serverResponse)
			resp, err := conn.MetaArithmetic(tt.key, tt.flags...)

			if tt.expectErr {
				if err == nil {
					t.Errorf("MetaArithmetic() expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("MetaArithmetic() unexpected error: %v", err)
				}
			}

			writtenCmd := mock.writeBuffer.String()
			if writtenCmd != tt.expectedCmd {
				t.Errorf("MetaArithmetic() sent command = %q, want %q", writtenCmd, tt.expectedCmd)
			}
			if resp.Code != tt.expectedCode {
				t.Errorf("MetaArithmetic() code = %q, want %q", resp.Code, tt.expectedCode)
			}
			if !bytes.Equal(resp.Data, tt.expectedData) {
				t.Errorf("MetaArithmetic() data = %q, want %q", resp.Data, tt.expectedData)
			}
			if resp.Value != tt.expectedValue {
				t.Errorf("MetaArithmetic() value = %d, want %d", resp.Value, tt.expectedValue)
			}
			if resp.Size != tt.expectedSize {
				t.Errorf("MetaArithmetic() size = %d, want %d", resp.Size, tt.expectedSize)
			}
		})
	}
}

func TestMetaNoop(t *testing.T) {
	tests := []struct {
		name           string
		serverResponse string
		expectedCmd    string
		expectedCode   string
		expectErr      bool
	}{
		{
			name:           "simple noop",
			serverResponse: "MN\r\n",
			expectedCmd:    "mn \r\n", // Note: key is empty for mn
			expectedCode:   "MN",
		},
		{
			name:           "noop with unexpected response (should still parse code)",
			serverResponse: "XX somearg\r\n",
			expectedCmd:    "mn \r\n",
			expectedCode:   "XX", // The code is what's parsed
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, mock := newTestConn(tt.serverResponse)
			resp, err := conn.MetaNoop()

			if tt.expectErr {
				if err == nil {
					t.Errorf("MetaNoop() expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("MetaNoop() unexpected error: %v", err)
				}
			}

			writtenCmd := mock.writeBuffer.String()
			if writtenCmd != tt.expectedCmd {
				t.Errorf("MetaNoop() sent command = %q, want %q", writtenCmd, tt.expectedCmd)
			}

			if resp.Code != tt.expectedCode {
				t.Errorf("MetaNoop() code = %q, want %q", resp.Code, tt.expectedCode)
			}
		})
	}
}

func TestSendCommand(t *testing.T) {
	tests := []struct {
		name        string
		cmd         string
		key         string
		datalen     int
		flags       []MetaFlag // Uses MetaFlag directly
		data        []byte
		expectedOut string
	}{
		{"get no flags", "mg", "mykey", 0, nil, nil, "mg mykey\r\n"},
		{"get with flags", "mg", "key2", 0, []MetaFlag{"v", "c"}, nil, "mg key2 v c\r\n"},
		{"set no flags", "ms", "setkey", 5, nil, []byte("value"), "ms setkey 5\r\nvalue\r\n"},
		{"set with flags", "ms", "setkeyF", 7, []MetaFlag{"T300", "c"}, []byte("dataval"), "ms setkeyF 7 T300 c\r\ndataval\r\n"},
		{"set zero len data", "ms", "zerokey", 0, []MetaFlag{"NX"}, []byte{}, "ms zerokey 0 NX\r\n\r\n"}, // data block is empty but \r\n is sent
		{"delete", "md", "delkey1", 0, []MetaFlag{"q", "Otoken"}, nil, "md delkey1 q Otoken\r\n"},
		{"arithmetic", "ma", "count", 0, []MetaFlag{"D1", "N60"}, nil, "ma count D1 N60\r\n"},
		{"noop", "mn", "", 0, nil, nil, "mn \r\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, mock := newTestConn("") // serverResponse not used for sendCommand directly
			err := conn.sendCommand(tt.cmd, tt.key, tt.datalen, tt.flags, tt.data)
			if err != nil {
				t.Fatalf("sendCommand() error = %v, wantErr nil", err)
			}
			got := mock.writeBuffer.String()
			if got != tt.expectedOut {
				t.Errorf("sendCommand() output = %q, want %q", got, tt.expectedOut)
			}
		})
	}
}

func TestReadResponse(t *testing.T) {
	tests := []struct {
		name           string
		serverResponse string
		expectedCode   string
		expectedArgs   [][]byte // This was correctly changed before
		expectedData   []byte
		expectErr      bool
	}{
		{"HD response", "HD T300 c123\r\n", "HD", [][]byte{[]byte("T300"), []byte("c123")}, nil, false},
		{"VA response", "VA 5 v\r\nvalue\r\n", "VA", [][]byte{[]byte("5"), []byte("v")}, []byte("value"), false},
		{"VA response with spaces in data", "VA 11 v\r\nhello world\r\n", "VA", [][]byte{[]byte("11"), []byte("v")}, []byte("hello world"), false},
		{"VA response empty data", "VA 0 v\r\n\r\n", "VA", [][]byte{[]byte("0"), []byte("v")}, []byte{}, false},
		{"EN response", "EN\r\n", "EN", [][]byte{}, nil, false},
		{"MN response", "MN\r\n", "MN", [][]byte{}, nil, false},
		{"NF response", "NF\r\n", "NF", [][]byte{}, nil, false},
		{"NS response", "NS\r\n", "NS", [][]byte{}, nil, false},
		{"EX response", "EX\r\n", "EX", [][]byte{}, nil, false},
		{"CLIENT_ERROR response", "CLIENT_ERROR bad command format\r\n", "CLIENT_ERROR", [][]byte{[]byte("bad"), []byte("command"), []byte("format")}, nil, false},
		{"SERVER_ERROR response", "SERVER_ERROR out of memory\r\n", "SERVER_ERROR", [][]byte{[]byte("out"), []byte("of"), []byte("memory")}, nil, false},
		{"Empty response", "", "", nil, nil, true}, // Expect EOF
		{"Incomplete VA data", "VA 10 v\r\nshort", "VA", [][]byte{[]byte("10"), []byte("v")}, []byte("short"), true},
		{"Malformed VA size", "VA ten v\r\nvalue\r\n", "VA", [][]byte{[]byte("ten"), []byte("v")}, nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, _ := newTestConn(tt.serverResponse)
			code, args, data, err := conn.readResponse()

			if tt.expectErr {
				if err == nil {
					t.Errorf("readResponse() expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("readResponse() unexpected error: %v", err)
				}
			}

			if code != tt.expectedCode {
				t.Errorf("readResponse() code = %q, want %q", code, tt.expectedCode)
			}
			if !reflect.DeepEqual(args, tt.expectedArgs) {
				t.Errorf("readResponse() args = %v, want %v", args, tt.expectedArgs)
			}
			if !bytes.Equal(data, tt.expectedData) {
				t.Errorf("readResponse() data = %q, want %q", data, tt.expectedData)
			}
		})
	}
}

func TestNewConn(t *testing.T) {
	mock := &mockNetConn{}
	conn := NewConn(mock) // Uses NewConn directly
	if conn.c != mock {
		t.Errorf("NewConn().c = %p, want %p", conn.c, mock)
	}
	if conn.r == nil {
		t.Error("NewConn().r is nil, want *bufio.Reader")
	}
	if conn.w == nil {
		t.Error("NewConn().w is nil, want *bufio.Writer")
	}
}

// setupBenchmarkConn establishes a connection for benchmark tests.
func setupBenchmarkConn(b *testing.B) *Conn { // Uses *Conn directly
	// Ensure memcached is running for benchmarks
	if os.Getenv("MEMCACHED_HOST") == "" && os.Getenv("CI") == "" { // Allow override for CI or specific setups
		// Attempt to connect to default local memcached
		connNet, err := net.DialTimeout("tcp", testMemcachedHost, 200*time.Millisecond)
		if err != nil {
			b.Skipf("Skipping benchmarks, memcached not available at %s: %v", testMemcachedHost, err)
			return nil
		}
		connNet.Close()
	}

	serverAddr := testMemcachedHost
	if os.Getenv("MEMCACHED_HOST") != "" {
		serverAddr = os.Getenv("MEMCACHED_HOST")
	}

	connNet, err := net.Dial("tcp", serverAddr)
	if err != nil {
		b.Fatalf("Failed to connect to memcached for benchmark: %v", err)
	}
	return NewConn(connNet) // Uses NewConn directly
}

// Benchmarks
// Ensure all benchmarks use *Conn directly and NewConn directly if they create connections.
// Example for BenchmarkMetaGet:
func BenchmarkMetaGet(b *testing.B) {
	conn := setupBenchmarkConn(b)
	if conn == nil { // setupBenchmarkConn now returns *Conn
		return
	}
	defer conn.Close()

	key := "benchmarkGet"
	flags := []MetaFlag{FlagReturnValue()} // Uses MetaFlag directly

	for i := 0; i < b.N; i++ {
		_, err := conn.MetaGet(key, flags...)
		if err != nil {
			b.Errorf("MetaGet() error = %v", err)
		}
	}
}

// BenchmarkMetaSet
func BenchmarkMetaSet(b *testing.B) {
	conn := setupBenchmarkConn(b)
	if conn == nil {
		return
	}
	defer conn.Close()

	key := "benchmarkSet"
	value := []byte("benchmarkValue1234567890") // A typical small-ish value
	flags := []MetaFlag{FlagSetTTL(300)}        // Common flags

	// Ensure the key is set once before the benchmark loop if needed for read-after-write,
	// but for a pure set benchmark, this is fine.
	// If the benchmark is sensitive to key existence, pre-populate or delete.
	// For now, we assume repeated sets are the target.

	b.ResetTimer() // Reset timer after setup and before the loop
	for i := 0; i < b.N; i++ {
		_, err := conn.MetaSet(key, value, flags...)
		if err != nil {
			b.Fatalf("MetaSet() error = %v", err)
		}
	}
}

// BenchmarkMetaSetSmallValue
func BenchmarkMetaSetSmallValue(b *testing.B) {
	conn := setupBenchmarkConn(b)
	if conn == nil {
		return
	}
	defer conn.Close()

	key := "benchmarkSetSmall"
	value := []byte("smval")
	flags := []MetaFlag{FlagSetTTL(300)}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := conn.MetaSet(key, value, flags...)
		if err != nil {
			b.Fatalf("MetaSet() [small value] error = %v", err)
		}
	}
}

// BenchmarkMetaSetLargeValue
func BenchmarkMetaSetLargeValue(b *testing.B) {
	conn := setupBenchmarkConn(b)
	if conn == nil {
		return
	}
	defer conn.Close()

	key := "benchmarkSetLarge"
	// Create a 4KB value
	value := make([]byte, 4*1024)
	for j := range value {
		// Fill with a repeating character to ensure content
		value[j] = byte('A' + (j % 26))
	}
	flags := []MetaFlag{FlagSetTTL(300)}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := conn.MetaSet(key, value, flags...)
		if err != nil {
			b.Fatalf("MetaSet() [large value] error = %v", err)
		}
	}
}

// Similar adjustments for:
// BenchmarkMetaDelete
// BenchmarkMetaArithmeticIncr
// BenchmarkMetaNoop
// BenchmarkSendCommandGet
// BenchmarkSendCommandSet
// BenchmarkReadResponseHD
// BenchmarkReadResponseVA

// Ensure all instances of memcache.Conn, memcache.NewConn, memcache.MetaFlag are replaced
// with Conn, NewConn, MetaFlag respectively throughout the file.
