package memcache

import (
	"bytes"
	"io"
	"net"
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

func newTestConn(serverResponse string) (*Conn, *mockNetConn) {
	mock := &mockNetConn{}
	mock.readBuffer.WriteString(serverResponse)
	conn := NewConn(mock)
	return conn, mock
}

func TestMetaGet(t *testing.T) {
	tests := []struct {
		name             string
		key              string
		flags            []MetaFlag
		serverResponse   string
		expectedCmd      string
		expectedCode     string
		expectedArgs     []string
		expectedData     []byte
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
			expectedArgs:   []string{"5", "v"},
			expectedData:   []byte("value"),
		},
		{
			name:           "get with CAS",
			key:            "another",
			flags:          []MetaFlag{FlagReturnValue(), FlagReturnCAS()},
			serverResponse: "VA 6 v c123\r\nmydata\r\n", // Changed data length from 7 to 6
			expectedCmd:    "mg another v c\r\n",
			expectedCode:   "VA",
			expectedArgs:   []string{"6", "v", "c123"}, // Changed expected size arg from "7" to "6"
			expectedData:   []byte("mydata"),
		},
		{
			name:           "get miss (EN)",
			key:            "misskey",
			flags:          []MetaFlag{FlagReturnValue()},
			serverResponse: "EN\r\n",
			expectedCmd:    "mg misskey v\r\n",
			expectedCode:   "EN",
			expectedArgs:   []string{},
			expectedData:   nil,
		},
		{
			name:           "get hit no value (HD)",
			key:            "keynoval",
			flags:          []MetaFlag{FlagReturnCAS()},
			serverResponse: "HD c789\r\n",
			expectedCmd:    "mg keynoval c\r\n",
			expectedCode:   "HD",
			expectedArgs:   []string{"c789"},
			expectedData:   nil,
		},
		{
			name:             "network error on send",
			key:              "testkey",
			flags:            []MetaFlag{FlagReturnValue()},
			serverResponse:   "", // No response as send fails
			expectedCmd:      "mg testkey v\r\n",
			expectErr:        true,
			skipResponseTest: true, // Error happens during send
			// Custom mock behavior for send error:
			// We'll achieve this by making the mockNetConn's Write method return an error.
			// This requires a way to trigger that, perhaps by a special key or by modifying the mock setup.
			// For simplicity, we'll assume the test setup for this specific case would involve
			// a mockNetConn that is pre-configured to fail on Write.
			// Here, we'll simulate it by checking for error and not response.
		},
		{
			name:           "network error on read",
			key:            "readerr",
			flags:          []MetaFlag{FlagReturnValue()},
			serverResponse: "VA 5 v\r\nval", // Incomplete response
			expectedCmd:    "mg readerr v\r\n",
			expectedCode:   "VA",               // Expect partial code
			expectedArgs:   []string{"5", "v"}, // Expect partial args
			expectedData:   []byte("val"),      // Expect partial data
			expectErr:      true,               // Expecting io.EOF or similar due to incomplete read
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, mock := newTestConn(tt.serverResponse)
			if tt.name == "network error on send" {
				// Simulate write error by setting the flag on the mock
				mock.simulateWriteError = true
				// No need to defer, as each subtest gets a new mock from newTestConn
				// or ensure it's reset if the mock were shared across sub-tests of this loop.
				// However, newTestConn creates a fresh mock, so this is fine.
			}

			code, args, data, err := conn.MetaGet(tt.key, tt.flags...)

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
			// If a write error was expected and occurred, the writtenCmd might be empty or partial.
			// The primary check is that the error occurred. Only check full command if no error was expected during send.
			if !(tt.name == "network error on send" && tt.expectErr && err != nil) {
				if writtenCmd != tt.expectedCmd {
					t.Errorf("MetaGet() sent command = %q, want %q", writtenCmd, tt.expectedCmd)
				}
			}

			if tt.skipResponseTest {
				return
			}

			if code != tt.expectedCode {
				t.Errorf("MetaGet() code = %q, want %q", code, tt.expectedCode)
			}
			if !reflect.DeepEqual(args, tt.expectedArgs) {
				t.Errorf("MetaGet() args = %v, want %v", args, tt.expectedArgs)
			}
			if !bytes.Equal(data, tt.expectedData) {
				t.Errorf("MetaGet() data = %q, want %q", data, tt.expectedData)
			}
		})
	}
}

func TestMetaSet(t *testing.T) {
	tests := []struct {
		name           string
		key            string
		value          []byte
		flags          []MetaFlag
		serverResponse string
		expectedCmd    string // Includes datalen and data for set
		expectedCode   string
		expectedArgs   []string
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
			expectedArgs:   []string{"T300"},
		},
		{
			name:           "set with CAS return",
			key:            "setcas",
			value:          []byte("casval"),
			flags:          []MetaFlag{FlagReturnCAS()},
			serverResponse: "HD c456\r\n",
			expectedCmd:    "ms setcas 6 c\r\ncasval\r\n",
			expectedCode:   "HD",
			expectedArgs:   []string{"c456"},
		},
		{
			name:           "set not stored (NS)",
			key:            "setns",
			value:          []byte("novalue"),
			flags:          []MetaFlag{FlagModeAdd()}, // e.g. add fails if key exists
			serverResponse: "NS\r\n",
			expectedCmd:    "ms setns 7 ME\r\nnovalue\r\n",
			expectedCode:   "NS",
			expectedArgs:   []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, mock := newTestConn(tt.serverResponse)
			code, args, err := conn.MetaSet(tt.key, tt.value, tt.flags...)

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
			if code != tt.expectedCode {
				t.Errorf("MetaSet() code = %q, want %q", code, tt.expectedCode)
			}
			if !reflect.DeepEqual(args, tt.expectedArgs) {
				t.Errorf("MetaSet() args = %v, want %v", args, tt.expectedArgs)
			}
		})
	}
}

func TestMetaDelete(t *testing.T) {
	tests := []struct {
		name           string
		key            string
		flags          []MetaFlag
		serverResponse string
		expectedCmd    string
		expectedCode   string
		expectedArgs   []string
		expectErr      bool
	}{
		{
			name:           "simple delete",
			key:            "delkey",
			flags:          []MetaFlag{},
			serverResponse: "HD\r\n",
			expectedCmd:    "md delkey\r\n",
			expectedCode:   "HD",
			expectedArgs:   []string{},
		},
		{
			name:           "delete not found (NF)",
			key:            "delnf",
			flags:          []MetaFlag{},
			serverResponse: "NF\r\n",
			expectedCmd:    "md delnf\r\n",
			expectedCode:   "NF",
			expectedArgs:   []string{},
		},
		{
			name:           "delete with opaque",
			key:            "delop",
			flags:          []MetaFlag{FlagOpaque("myop")},
			serverResponse: "HD Omyop\r\n",
			expectedCmd:    "md delop Omyop\r\n",
			expectedCode:   "HD",
			expectedArgs:   []string{"Omyop"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, mock := newTestConn(tt.serverResponse)
			code, args, err := conn.MetaDelete(tt.key, tt.flags...)

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
			if code != tt.expectedCode {
				t.Errorf("MetaDelete() code = %q, want %q", code, tt.expectedCode)
			}
			if !reflect.DeepEqual(args, tt.expectedArgs) {
				t.Errorf("MetaDelete() args = %v, want %v", args, tt.expectedArgs)
			}
		})
	}
}

func TestMetaArithmetic(t *testing.T) {
	tests := []struct {
		name           string
		key            string
		flags          []MetaFlag
		serverResponse string
		expectedCmd    string
		expectedCode   string
		expectedArgs   []string
		expectedData   []byte
		expectErr      bool
	}{
		{
			name:           "simple incr (default delta 1)",
			key:            "inckey",
			flags:          []MetaFlag{FlagModeIncr(), FlagReturnValue()},
			serverResponse: "VA 1 v\r\n5\r\n",
			expectedCmd:    "ma inckey MI v\r\n",
			expectedCode:   "VA",
			expectedArgs:   []string{"1", "v"},
			expectedData:   []byte("5"),
		},
		{
			name:           "decr with delta",
			key:            "deckey",
			flags:          []MetaFlag{FlagModeDecr(), FlagDelta(10), FlagReturnValue()},
			serverResponse: "VA 2 v\r\n15\r\n",
			expectedCmd:    "ma deckey MD D10 v\r\n",
			expectedCode:   "VA",
			expectedArgs:   []string{"2", "v"},
			expectedData:   []byte("15"),
		},
		{
			name:           "arithmetic not found (NF)",
			key:            "arithnf",
			flags:          []MetaFlag{FlagModeIncr()},
			serverResponse: "NF\r\n",
			expectedCmd:    "ma arithnf MI\r\n",
			expectedCode:   "NF",
			expectedArgs:   []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, mock := newTestConn(tt.serverResponse)
			code, args, data, err := conn.MetaArithmetic(tt.key, tt.flags...)

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
			if code != tt.expectedCode {
				t.Errorf("MetaArithmetic() code = %q, want %q", code, tt.expectedCode)
			}
			if !reflect.DeepEqual(args, tt.expectedArgs) {
				t.Errorf("MetaArithmetic() args = %v, want %v", args, tt.expectedArgs)
			}
			if !bytes.Equal(data, tt.expectedData) {
				t.Errorf("MetaArithmetic() data = %q, want %q", data, tt.expectedData)
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
		expectedArgs   []string
		expectErr      bool
	}{
		{
			name:           "simple noop",
			serverResponse: "MN\r\n",
			expectedCmd:    "mn \r\n", // Note: key is empty for mn
			expectedCode:   "MN",
			expectedArgs:   []string{},
		},
		{
			name:           "noop with unexpected response (should still parse code)",
			serverResponse: "XX somearg\r\n",
			expectedCmd:    "mn \r\n",
			expectedCode:   "XX", // The code is what's parsed
			expectedArgs:   []string{"somearg"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, mock := newTestConn(tt.serverResponse)
			code, args, err := conn.MetaNoop()

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
			// Adjust expected command for MetaNoop as it sends an empty key
			if writtenCmd != tt.expectedCmd {
				t.Errorf("MetaNoop() sent command = %q, want %q", writtenCmd, tt.expectedCmd)
			}

			if code != tt.expectedCode {
				t.Errorf("MetaNoop() code = %q, want %q", code, tt.expectedCode)
			}
			if !reflect.DeepEqual(args, tt.expectedArgs) {
				t.Errorf("MetaNoop() args = %v, want %v", args, tt.expectedArgs)
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
		flags       []string
		data        []byte
		expectedOut string
	}{
		{"get no flags", "mg", "mykey", 0, nil, nil, "mg mykey\r\n"},
		{"get with flags", "mg", "key2", 0, []string{"v", "c"}, nil, "mg key2 v c\r\n"},
		{"set no flags", "ms", "setkey", 5, nil, []byte("value"), "ms setkey 5\r\nvalue\r\n"},
		{"set with flags", "ms", "setkeyF", 7, []string{"T300", "c"}, []byte("dataval"), "ms setkeyF 7 T300 c\r\ndataval\r\n"},
		{"set zero len data", "ms", "zerokey", 0, []string{"NX"}, []byte{}, "ms zerokey 0 NX\r\n\r\n"}, // data block is empty but \r\n is sent
		{"delete", "md", "delkey1", 0, []string{"q", "Otoken"}, nil, "md delkey1 q Otoken\r\n"},
		{"arithmetic", "ma", "count", 0, []string{"D1", "N60"}, nil, "ma count D1 N60\r\n"},
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
		expectedArgs   []string
		expectedData   []byte
		expectErr      bool
	}{
		{"HD response", "HD T300 c123\r\n", "HD", []string{"T300", "c123"}, nil, false},
		{"VA response", "VA 5 v\r\nvalue\r\n", "VA", []string{"5", "v"}, []byte("value"), false},
		{"VA response with spaces in data", "VA 11 v\r\nhello world\r\n", "VA", []string{"11", "v"}, []byte("hello world"), false},
		{"VA response empty data", "VA 0 v\r\n\r\n", "VA", []string{"0", "v"}, []byte{}, false},
		{"EN response", "EN\r\n", "EN", []string{}, nil, false},
		{"MN response", "MN\r\n", "MN", []string{}, nil, false},
		{"NF response", "NF\r\n", "NF", []string{}, nil, false},
		{"NS response", "NS\r\n", "NS", []string{}, nil, false},
		{"EX response", "EX\r\n", "EX", []string{}, nil, false},
		{"CLIENT_ERROR response", "CLIENT_ERROR bad command format\r\n", "CLIENT_ERROR", []string{"bad", "command", "format"}, nil, false},
		{"SERVER_ERROR response", "SERVER_ERROR out of memory\r\n", "SERVER_ERROR", []string{"out", "of", "memory"}, nil, false},
		{"Empty response", "", "", nil, nil, true}, // Expect EOF
		{"Incomplete VA data", "VA 10 v\r\nshort", "VA", []string{"10", "v"}, []byte("short"), true},
		{"Malformed VA size", "VA ten v\r\nvalue\r\n", "VA", []string{"ten", "v"}, nil, true}, // Expect Atoi error, args will have "ten"
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
	conn := NewConn(mock)
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
