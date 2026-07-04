package meta

import (
	"bufio"
	"bytes"
	"errors"
	"strings"
	"testing"
)

// Test request serialization

func TestWriteGetRequest(t *testing.T) {
	tests := []struct {
		name     string
		req      *Request
		expected string
	}{
		{
			name:     "basic get",
			req:      NewRequest(CmdGet, "mykey", nil),
			expected: "mg mykey\r\n",
		},
		{
			name:     "get with value flag",
			req:      NewRequest(CmdGet, "mykey", nil).AddReturnValue(),
			expected: "mg mykey v\r\n",
		},
		{
			name:     "get with multiple flags",
			req:      NewRequest(CmdGet, "mykey", nil).AddReturnValue().AddReturnCAS().AddReturnTTL(),
			expected: "mg mykey v c t\r\n",
		},
		{
			name:     "get with token flags",
			req:      NewRequest(CmdGet, "mykey", nil).AddReturnValue().AddOpaque("mytoken"),
			expected: "mg mykey v Omytoken\r\n",
		},
		{
			name:     "get with recache flag",
			req:      NewRequest(CmdGet, "mykey", nil).AddReturnValue().AddRecache(30),
			expected: "mg mykey v R30\r\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := WriteRequest(&buf, tt.req)
			if err != nil {
				t.Fatalf("WriteRequest failed: %v", err)
			}
			if got := buf.String(); got != tt.expected {
				t.Errorf("WriteRequest() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestWriteSetRequest(t *testing.T) {
	tests := []struct {
		name     string
		req      *Request
		expected string
	}{
		{
			name:     "basic set",
			req:      NewRequest(CmdSet, "mykey", []byte("hello")),
			expected: "ms mykey 5\r\nhello\r\n",
		},
		{
			name:     "set with zero-length value",
			req:      NewRequest(CmdSet, "mykey", []byte("")),
			expected: "ms mykey 0\r\n\r\n",
		},
		{
			name:     "set with TTL",
			req:      NewRequest(CmdSet, "mykey", []byte("hello")).AddTTL(60),
			expected: "ms mykey 5 T60\r\nhello\r\n",
		},
		{
			name:     "set with mode",
			req:      NewRequest(CmdSet, "mykey", []byte("hello")).AddModeAdd(),
			expected: "ms mykey 5 ME\r\nhello\r\n",
		},
		{
			name:     "set with CAS and flags",
			req:      NewRequest(CmdSet, "mykey", []byte("hello")).AddCAS(12345).AddClientFlags(30),
			expected: "ms mykey 5 C12345 F30\r\nhello\r\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := WriteRequest(&buf, tt.req)
			if err != nil {
				t.Fatalf("WriteRequest failed: %v", err)
			}
			if got := buf.String(); got != tt.expected {
				t.Errorf("WriteRequest() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestWriteDeleteRequest(t *testing.T) {
	tests := []struct {
		name     string
		req      *Request
		expected string
	}{
		{
			name:     "basic delete",
			req:      NewRequest(CmdDelete, "mykey", nil),
			expected: "md mykey\r\n",
		},
		{
			name:     "delete with invalidate",
			req:      NewRequest(CmdDelete, "mykey", nil).AddInvalidate().AddTTL(30),
			expected: "md mykey I T30\r\n",
		},
		{
			name:     "delete with CAS",
			req:      NewRequest(CmdDelete, "mykey", nil).AddCAS(12345),
			expected: "md mykey C12345\r\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := WriteRequest(&buf, tt.req)
			if err != nil {
				t.Fatalf("WriteRequest failed: %v", err)
			}
			if got := buf.String(); got != tt.expected {
				t.Errorf("WriteRequest() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestWriteArithmeticRequest(t *testing.T) {
	tests := []struct {
		name     string
		req      *Request
		expected string
	}{
		{
			name:     "basic increment",
			req:      NewRequest(CmdArithmetic, "counter", nil).AddReturnValue(),
			expected: "ma counter v\r\n",
		},
		{
			name:     "increment with delta",
			req:      NewRequest(CmdArithmetic, "counter", nil).AddReturnValue().AddDelta(5),
			expected: "ma counter v D5\r\n",
		},
		{
			name:     "decrement",
			req:      NewRequest(CmdArithmetic, "counter", nil).AddReturnValue().AddModeDecrement(),
			expected: "ma counter v MD\r\n",
		},
		{
			name:     "auto-create with initial value",
			req:      NewRequest(CmdArithmetic, "counter", nil).AddReturnValue().AddVivify(60).AddInitialValue(100),
			expected: "ma counter v N60 J100\r\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := WriteRequest(&buf, tt.req)
			if err != nil {
				t.Fatalf("WriteRequest failed: %v", err)
			}
			if got := buf.String(); got != tt.expected {
				t.Errorf("WriteRequest() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestWriteNoOpRequest(t *testing.T) {
	req := NewRequest(CmdNoOp, "", nil)
	var buf bytes.Buffer
	err := WriteRequest(&buf, req)
	if err != nil {
		t.Fatalf("WriteRequest failed: %v", err)
	}
	expected := "mn\r\n"
	if got := buf.String(); got != expected {
		t.Errorf("WriteRequest() = %q, want %q", got, expected)
	}
}

// Test response parsing

func TestReadResponse_HD(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected *Response
	}{
		{
			name:  "HD basic",
			input: "HD\r\n",
			expected: &Response{
				Status: StatusHD,
				Flags:  nil,
			},
		},
		{
			name:  "HD with flags",
			input: "HD c12345 t3600\r\n",
			expected: &Response{
				Status: StatusHD,
				Flags:  []byte(" c12345 t3600"),
			},
		},
		{
			name:  "HD with opaque",
			input: "HD Omytoken\r\n",
			expected: &Response{
				Status: StatusHD,
				Flags:  []byte(" Omytoken"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := bufio.NewReader(strings.NewReader(tt.input))
			var resp Response
			err := ReadResponse(r, &resp)
			if err != nil {
				t.Fatalf("ReadResponse failed: %v", err)
			}
			if resp.Status != tt.expected.Status {
				t.Errorf("Status = %q, want %q", resp.Status, tt.expected.Status)
			}
			if !bytes.Equal(resp.Flags, tt.expected.Flags) {
				t.Errorf("Flags = %q, want %q", string(resp.Flags), string(tt.expected.Flags))
			}
		})
	}
}

func TestReadResponse_VA(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected *Response
	}{
		{
			name:  "VA basic",
			input: "VA 5\r\nhello\r\n",
			expected: &Response{
				Status: StatusVA,
				Data:   []byte("hello"),
				Flags:  nil,
			},
		},
		{
			name:  "VA with flags",
			input: "VA 5 c12345 t3600\r\nhello\r\n",
			expected: &Response{
				Status: StatusVA,
				Data:   []byte("hello"),
				Flags:  []byte(" c12345 t3600"),
			},
		},
		{
			name:  "VA with win flag",
			input: "VA 5 W\r\nhello\r\n",
			expected: &Response{
				Status: StatusVA,
				Data:   []byte("hello"),
				Flags:  []byte(" W"),
			},
		},
		{
			name:  "VA with stale and win",
			input: "VA 5 X W\r\nhello\r\n",
			expected: &Response{
				Status: StatusVA,
				Data:   []byte("hello"),
				Flags:  []byte(" X W"),
			},
		},
		{
			name:  "VA zero-length",
			input: "VA 0\r\n\r\n",
			expected: &Response{
				Status: StatusVA,
				Data:   []byte{},
				Flags:  nil,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := bufio.NewReader(strings.NewReader(tt.input))
			var resp Response
			err := ReadResponse(r, &resp)
			if err != nil {
				t.Fatalf("ReadResponse failed: %v", err)
			}
			if resp.Status != tt.expected.Status {
				t.Errorf("Status = %q, want %q", resp.Status, tt.expected.Status)
			}
			if !bytes.Equal(resp.Data, tt.expected.Data) {
				t.Errorf("Data = %q, want %q", resp.Data, tt.expected.Data)
			}
			if !bytes.Equal(resp.Flags, tt.expected.Flags) {
				t.Errorf("Flags = %q, want %q", string(resp.Flags), string(tt.expected.Flags))
			}
		})
	}
}

func TestReadResponse_InvalidVASize(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		expectedError string
	}{
		{
			name:          "negative size",
			input:         "VA -1\r\n",
			expectedError: "negative size in VA response",
		},
		{
			name:          "missing size",
			input:         "VA\r\n",
			expectedError: "VA response missing size",
		},
		{
			name:          "invalid size format",
			input:         "VA abc\r\n",
			expectedError: "invalid size in VA response",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := bufio.NewReader(strings.NewReader(tt.input))
			var resp Response
			err := ReadResponse(r, &resp)
			if err == nil {
				t.Fatal("Expected error, got nil")
			}
			parseErr, ok := err.(*ParseError)
			if !ok {
				t.Fatalf("Expected ParseError, got %T", err)
			}
			if parseErr.Message != tt.expectedError {
				t.Errorf("Error message = %q, want %q", parseErr.Message, tt.expectedError)
			}
		})
	}
}

func TestReadResponse_Errors(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		expectError bool
		errorType   string
		shouldClose bool
	}{
		{
			name:        "CLIENT_ERROR",
			input:       "CLIENT_ERROR bad command line format\r\n",
			expectError: true,
			errorType:   "*meta.ClientError",
			shouldClose: true,
		},
		{
			name:        "SERVER_ERROR",
			input:       "SERVER_ERROR out of memory\r\n",
			expectError: true,
			errorType:   "*meta.ServerError",
			shouldClose: false,
		},
		{
			name:        "ERROR",
			input:       "ERROR\r\n",
			expectError: true,
			errorType:   "*meta.GenericError",
			shouldClose: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := bufio.NewReader(strings.NewReader(tt.input))
			var resp Response
			err := ReadResponse(r, &resp)
			if err != nil {
				t.Fatalf("ReadResponse returned error: %v", err)
			}
			if !resp.HasError() {
				t.Errorf("HasError() = false, want true")
			}
			if ShouldCloseConnection(resp.Error) != tt.shouldClose {
				t.Errorf("ShouldCloseConnection() = %v, want %v",
					ShouldCloseConnection(resp.Error), tt.shouldClose)
			}
		})
	}
}

func TestReadResponse_OtherStatuses(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected StatusType
	}{
		{
			name:     "EN",
			input:    "EN\r\n",
			expected: StatusEN,
		},
		{
			name:     "NF",
			input:    "NF\r\n",
			expected: StatusNF,
		},
		{
			name:     "NS",
			input:    "NS\r\n",
			expected: StatusNS,
		},
		{
			name:     "EX",
			input:    "EX\r\n",
			expected: StatusEX,
		},
		{
			name:     "MN",
			input:    "MN\r\n",
			expected: StatusMN,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := bufio.NewReader(strings.NewReader(tt.input))
			var resp Response
			err := ReadResponse(r, &resp)
			if err != nil {
				t.Fatalf("ReadResponse failed: %v", err)
			}
			if resp.Status != tt.expected {
				t.Errorf("Status = %q, want %q", resp.Status, tt.expected)
			}
		})
	}
}

// Test batch operations

func TestWriteMultipleRequests(t *testing.T) {
	reqs := []*Request{
		NewRequest(CmdGet, "key1", nil).AddReturnValue().AddQuiet(),
		NewRequest(CmdGet, "key2", nil).AddReturnValue().AddQuiet(),
		NewRequest(CmdGet, "key3", nil).AddReturnValue(),
		NewRequest(CmdNoOp, "", nil),
	}

	var buf bytes.Buffer

	for _, req := range reqs {
		err := WriteRequest(&buf, req)
		if err != nil {
			t.Fatalf("WriteRequest failed: %v", err)
		}
	}

	expected := "mg key1 v q\r\nmg key2 v q\r\nmg key3 v\r\nmn\r\n"
	if got := buf.String(); got != expected {
		t.Errorf("Multiple WriteRequest() = %q, want %q", got, expected)
	}
}

// Test helper methods

func TestResponse_HelperMethods(t *testing.T) {
	t.Run("IsSuccess", func(t *testing.T) {
		tests := []struct {
			status   StatusType
			expected bool
		}{
			{StatusHD, true},
			{StatusVA, true},
			{StatusMN, true},
			{StatusEN, false},
			{StatusNF, false},
			{StatusNS, false},
			{StatusEX, false},
		}

		for _, tt := range tests {
			resp := &Response{Status: tt.status}
			if got := resp.IsSuccess(); got != tt.expected {
				t.Errorf("IsSuccess() for %q = %v, want %v", tt.status, got, tt.expected)
			}
		}
	})

	t.Run("IsMiss", func(t *testing.T) {
		tests := []struct {
			status   StatusType
			expected bool
		}{
			{StatusEN, true},
			{StatusNF, true},
			{StatusHD, false},
			{StatusVA, false},
		}

		for _, tt := range tests {
			resp := &Response{Status: tt.status}
			if got := resp.IsMiss(); got != tt.expected {
				t.Errorf("IsMiss() for %q = %v, want %v", tt.status, got, tt.expected)
			}
		}
	})

	t.Run("Win", func(t *testing.T) {
		resp := &Response{Flags: []byte(" W")}
		if !resp.Win() {
			t.Error("Win() = false, want true")
		}
	})

	t.Run("GetFlagToken", func(t *testing.T) {
		resp := &Response{Flags: []byte(" c12345 t3600")}

		if got, _ := resp.GetFlagToken(FlagReturnCAS); string(got) != "12345" {
			t.Errorf("CAS token = %q, want %q", string(got), "12345")
		}
		if got, _ := resp.GetFlagToken(FlagReturnTTL); string(got) != "3600" {
			t.Errorf("TTL token = %q, want %q", string(got), "3600")
		}
		if got, ok := resp.GetFlagToken('x'); ok || got != nil {
			t.Errorf("unknown token = (%q, %v), want (nil, false)", string(got), ok)
		}
	})
}

func TestRequest_HelperMethods(t *testing.T) {
	t.Run("HasFlag", func(t *testing.T) {
		req := NewRequest(CmdGet, "mykey", nil).AddReturnValue().AddReturnCAS()

		if !req.HasFlag(FlagReturnValue) {
			t.Error("HasFlag('v') = false, want true")
		}
		if !req.HasFlag(FlagReturnCAS) {
			t.Error("HasFlag('c') = false, want true")
		}
		if req.HasFlag(FlagReturnTTL) {
			t.Error("HasFlag('t') = true, want false")
		}
	})

	t.Run("GetFlagToken", func(t *testing.T) {
		req := NewRequest(CmdGet, "mykey", nil).AddRecache(30)

		token, ok := req.GetFlagToken(FlagRecache)
		if !ok {
			t.Error("GetFlagToken('R') ok = false, want true")
		}
		if got := string(token); got != "30" {
			t.Errorf("GetFlagToken('R') = %q, want %q", got, "30")
		}

		_, ok = req.GetFlagToken('x')
		if ok {
			t.Error("GetFlagToken('x') ok = true, want false")
		}
	})

	t.Run("AddReturnValue", func(t *testing.T) {
		req := NewRequest(CmdGet, "mykey", nil).AddReturnValue()

		if !req.HasFlag(FlagReturnValue) {
			t.Error("HasFlag('v') after AddReturnValue = false, want true")
		}
	})
}

func TestValidateKey(t *testing.T) {
	tests := []struct {
		name          string
		key           string
		hasBase64Flag bool
		wantErr       bool
		errContains   string
	}{
		{
			name:    "valid simple key",
			key:     "mykey",
			wantErr: false,
		},
		{
			name:    "valid key with numbers",
			key:     "key123",
			wantErr: false,
		},
		{
			name:    "valid key with special chars",
			key:     "key:foo-bar_baz.v1",
			wantErr: false,
		},
		{
			name:        "empty key",
			key:         "",
			wantErr:     true,
			errContains: "empty",
		},
		{
			name:        "key too long",
			key:         string(make([]byte, 251)),
			wantErr:     true,
			errContains: "maximum length",
		},
		{
			name:        "key with space",
			key:         "my key",
			wantErr:     true,
			errContains: "whitespace",
		},
		{
			name:        "key with tab",
			key:         "my\tkey",
			wantErr:     true,
			errContains: "whitespace",
		},
		{
			name:        "key with newline",
			key:         "my\nkey",
			wantErr:     true,
			errContains: "whitespace",
		},
		{
			name:          "key with space but base64 flag",
			key:           "bXkga2V5", // base64 for "my key"
			hasBase64Flag: true,
			wantErr:       false,
		},
		{
			name:    "max length key",
			key:     string(make([]byte, 250)),
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateKey(tt.key, tt.hasBase64Flag)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ValidateKey() expected error containing %q, got nil", tt.errContains)
				} else if !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("ValidateKey() error = %v, want error containing %q", err, tt.errContains)
				}
			} else {
				if err != nil {
					t.Errorf("ValidateKey() unexpected error: %v", err)
				}
			}
		})
	}
}

func TestWriteRequest_InvalidKey(t *testing.T) {
	tests := []struct {
		name string
		req  *Request
	}{
		{
			name: "empty key",
			req:  NewRequest(CmdGet, "", nil),
		},
		{
			name: "key too long",
			req:  NewRequest(CmdGet, string(make([]byte, 251)), nil),
		},
		{
			name: "key with space",
			req:  NewRequest(CmdGet, "my key", nil),
		},
		{
			name: "key with tab",
			req:  NewRequest(CmdGet, "my\tkey", nil),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := WriteRequest(&buf, tt.req)
			if err == nil {
				t.Error("WriteRequest() expected error for invalid key, got nil")
			}
		})
	}
}

func TestWriteRequest_ValidKeyWithBase64Flag(t *testing.T) {
	// Key with space should be allowed if base64 flag is present
	req := NewRequest(CmdGet, "bXkga2V5", nil).AddBase64Key()

	var buf bytes.Buffer
	err := WriteRequest(&buf, req)
	if err != nil {
		t.Errorf("WriteRequest() unexpected error for base64 key: %v", err)
	}

	expected := "mg bXkga2V5 b\r\n"
	if buf.String() != expected {
		t.Errorf("WriteRequest() = %q, want %q", buf.String(), expected)
	}
}

// Test ParseDebugParams

func TestParseDebugParams_Empty(t *testing.T) {
	params := ParseDebugParams([]byte(""))
	if len(params) != 0 {
		t.Errorf("ParseDebugParams(empty) = %v, want empty map", params)
	}
}

func TestParseDebugParams_SingleParam(t *testing.T) {
	params := ParseDebugParams([]byte("size=1024"))
	if len(params) != 1 {
		t.Errorf("ParseDebugParams() returned %d params, want 1", len(params))
	}
	if params["size"] != "1024" {
		t.Errorf("ParseDebugParams()[\"size\"] = %q, want %q", params["size"], "1024")
	}
}

func TestParseDebugParams_MultipleParams(t *testing.T) {
	params := ParseDebugParams([]byte("size=1024 ttl=3600 flags=0"))

	expected := map[string]string{
		"size":  "1024",
		"ttl":   "3600",
		"flags": "0",
	}

	if len(params) != len(expected) {
		t.Errorf("ParseDebugParams() returned %d params, want %d", len(params), len(expected))
	}

	for key, want := range expected {
		if got := params[key]; got != want {
			t.Errorf("ParseDebugParams()[%q] = %q, want %q", key, got, want)
		}
	}
}

func TestParseDebugParams_EmptyValue(t *testing.T) {
	params := ParseDebugParams([]byte("key1= key2=value"))

	if params["key1"] != "" {
		t.Errorf("ParseDebugParams()[\"key1\"] = %q, want empty string", params["key1"])
	}
	if params["key2"] != "value" {
		t.Errorf("ParseDebugParams()[\"key2\"] = %q, want %q", params["key2"], "value")
	}
}

// Test ME response parsing

func TestReadResponse_ME_NoParams(t *testing.T) {
	input := "ME mykey\r\n"
	r := bufio.NewReader(strings.NewReader(input))

	var resp Response
	err := ReadResponse(r, &resp)
	if err != nil {
		t.Fatalf("ReadResponse() error = %v", err)
	}

	if resp.Status != StatusME {
		t.Errorf("ReadResponse().Status = %q, want %q", resp.Status, StatusME)
	}

	if len(resp.Data) != 0 {
		t.Errorf("ReadResponse().Data = %q, want empty (no debug params)", resp.Data)
	}
}

func TestReadResponse_ME_WithParams(t *testing.T) {
	input := "ME mykey size=1024 ttl=3600\r\n"
	r := bufio.NewReader(strings.NewReader(input))

	var resp Response
	err := ReadResponse(r, &resp)
	if err != nil {
		t.Fatalf("ReadResponse() error = %v", err)
	}

	if resp.Status != StatusME {
		t.Errorf("ReadResponse().Status = %q, want %q", resp.Status, StatusME)
	}

	expectedData := "size=1024 ttl=3600"
	if string(resp.Data) != expectedData {
		t.Errorf("ReadResponse().Data = %q, want %q", string(resp.Data), expectedData)
	}

	// Test parsing the debug params
	params := ParseDebugParams(resp.Data)
	if params["size"] != "1024" {
		t.Errorf("params[\"size\"] = %q, want %q", params["size"], "1024")
	}
	if params["ttl"] != "3600" {
		t.Errorf("params[\"ttl\"] = %q, want %q", params["ttl"], "3600")
	}
}

// Test Flags.AddInt with cached and non-cached values.
func TestFlags_AddInt(t *testing.T) {
	tests := []struct {
		name      string
		flagType  FlagType
		value     int
		wantToken string
	}{
		// Small values (0-100) are handled by strconv.Itoa's internal cache.
		{name: "small: zero", flagType: FlagTTL, value: 0, wantToken: "0"},
		{name: "small: delta 1", flagType: FlagDelta, value: 1, wantToken: "1"},
		{name: "small: 1 minute", flagType: FlagTTL, value: 60, wantToken: "60"},

		// Larger TTL values cached by our map.
		{name: "cached: 5 minutes", flagType: FlagTTL, value: 300, wantToken: "300"},
		{name: "cached: 1 hour", flagType: FlagTTL, value: 3600, wantToken: "3600"},
		{name: "cached: 1 day", flagType: FlagTTL, value: 86400, wantToken: "86400"},

		// Non-cached values.
		{name: "non-cached: custom TTL", flagType: FlagTTL, value: 42, wantToken: "42"},
		{name: "non-cached: large TTL", flagType: FlagTTL, value: 99999, wantToken: "99999"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var flags Flags
			flags.AddInt(tt.flagType, tt.value)
			want := " " + string(tt.flagType) + tt.wantToken
			if got := string(flags); got != want {
				t.Errorf("Flags.AddInt() = %q, want %q", got, want)
			}
		})
	}
}

// Test that unknown response statuses are rejected: accepting them silently
// would let a desynchronized stream go unnoticed.
func TestReadResponse_UnknownStatus(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "unknown two-letter status", input: "ZZ\r\n"},
		{name: "unknown status with tokens", input: "FOO bar baz\r\n"},
		{name: "lowercase status", input: "hd\r\n"},
		{name: "value data read as status", input: "somevaluedata\r\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := bufio.NewReader(strings.NewReader(tt.input))
			var resp Response
			err := ReadResponse(r, &resp)

			var parseErr *ParseError
			if !errors.As(err, &parseErr) {
				t.Fatalf("ReadResponse(%q) error = %v, want ParseError", tt.input, err)
			}
		})
	}
}

// Test that ME responses don't leak their key and debug tokens into Flags.
func TestReadResponse_ME_NoFlagPollution(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("ME mykey exp=3600 la=12 cas=5\r\n"))
	var resp Response
	if err := ReadResponse(r, &resp); err != nil {
		t.Fatalf("ReadResponse failed: %v", err)
	}

	if got := string(resp.Flags); got != "" {
		t.Errorf("ME response Flags = %q, want empty", got)
	}
	if got := string(resp.Data); got != "exp=3600 la=12 cas=5" {
		t.Errorf("ME response Data = %q, want %q", got, "exp=3600 la=12 cas=5")
	}
}

// Test that absurd VA sizes are rejected before allocating memory for them.
func TestReadResponse_VASizeTooLarge(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("VA 1099511627776\r\n")) // 1 TiB
	var resp Response
	err := ReadResponse(r, &resp)

	var parseErr *ParseError
	if !errors.As(err, &parseErr) {
		t.Fatalf("ReadResponse error = %v, want ParseError", err)
	}
}

// TestReadResponse_LineTooLong verifies that a status/flag line with no newline
// within the reader's buffer is rejected as a ParseError rather than allocated
// unbounded. This guards the memory-amplification path: the VA data block has a
// declared length, but the response line does not, so a hostile or buggy server
// could otherwise stream bytes forever and force the client to buffer all of it.
func TestReadResponse_LineTooLong(t *testing.T) {
	// A run of flag bytes much larger than the bufio buffer, with the newline
	// only at the very end so it is never seen before the buffer fills.
	const bufSize = MaxLineSize
	line := "HD " + strings.Repeat("x", 4*bufSize) + "\r\n"

	r := bufio.NewReaderSize(strings.NewReader(line), bufSize)
	var resp Response
	err := ReadResponse(r, &resp)

	var parseErr *ParseError
	if !errors.As(err, &parseErr) {
		t.Fatalf("ReadResponse error = %T (%v), want *ParseError", err, err)
	}
	if got, want := parseErr.Message, "response line exceeds maximum length"; got != want {
		t.Errorf("ParseError.Message = %q, want %q", got, want)
	}
	if !errors.Is(err, bufio.ErrBufferFull) {
		t.Errorf("error must wrap bufio.ErrBufferFull, got %v", err)
	}
}

// TestReadResponse_LineAtBufferBoundary verifies that a long-but-legitimate
// line that still fits within the buffer (delimiter included) parses correctly,
// so the bound rejects only truly unterminated lines.
func TestReadResponse_LineAtBufferBoundary(t *testing.T) {
	const bufSize = MaxLineSize
	// "HD " + flags + "\r\n" sized to exactly fill the buffer.
	flags := strings.Repeat("a", bufSize-len("HD ")-len(CRLF))
	line := "HD " + flags + CRLF
	if len(line) != bufSize {
		t.Fatalf("test setup: line len = %d, want %d", len(line), bufSize)
	}

	r := bufio.NewReaderSize(strings.NewReader(line), bufSize)
	var resp Response
	if err := ReadResponse(r, &resp); err != nil {
		t.Fatalf("ReadResponse returned error for boundary line: %v", err)
	}
	if resp.Status != StatusHD {
		t.Errorf("Status = %q, want %q", resp.Status, StatusHD)
	}
}

// TestReadResponse_LineSurvivesSubsequentRead guards against a zero-copy
// regression: the parsed line must be copied out of the bufio buffer so that a
// Response handed to the caller stays valid after the connection is reused for
// the next response. If readLine ever aliased the bufio buffer, the first
// response's Status/Data would be silently corrupted by the second read.
func TestReadResponse_LineSurvivesSubsequentRead(t *testing.T) {
	// First a VA response (value is copied via io.ReadFull, status/flags from the
	// line), then a different response that reuses the same buffer region.
	input := "VA 5 c123\r\nhello\r\n" + "EN somelongflagtoken_" + strings.Repeat("z", 64) + "\r\n"
	r := bufio.NewReader(strings.NewReader(input))

	var first Response
	if err := ReadResponse(r, &first); err != nil {
		t.Fatalf("first ReadResponse: %v", err)
	}
	// Snapshot the fields that must remain stable.
	gotStatus := first.Status
	gotData := string(first.Data)

	var second Response
	if err := ReadResponse(r, &second); err != nil {
		t.Fatalf("second ReadResponse: %v", err)
	}

	if first.Status != gotStatus {
		t.Errorf("first.Status mutated by second read: got %q, want %q", first.Status, gotStatus)
	}
	if string(first.Data) != gotData {
		t.Errorf("first.Data mutated by second read: got %q, want %q", first.Data, gotData)
	}
	if gotStatus != StatusVA || gotData != "hello" {
		t.Errorf("first response = (%q, %q), want (VA, hello)", gotStatus, gotData)
	}
}

func TestReadResponse_ReusesBuffers(t *testing.T) {
	t.Run("reuses sufficient data and flags capacity", func(t *testing.T) {
		data := make([]byte, 0, 64)
		flags := make(Flags, 0, 64)
		dataAddr := &data[:cap(data)][0]
		flagsAddr := &flags[:cap(flags)][0]
		resp := Response{
			Status: StatusEN,
			Data:   data,
			Flags:  flags,
			Error:  errors.New("previous response"),
		}

		r := bufio.NewReader(strings.NewReader("VA 5 c123 t60\r\nhello\r\n"))
		if err := ReadResponse(r, &resp); err != nil {
			t.Fatalf("ReadResponse failed: %v", err)
		}

		if got := string(resp.Data); got != "hello" {
			t.Errorf("Data = %q, want %q", got, "hello")
		}
		if got := string(resp.Flags); got != " c123 t60" {
			t.Errorf("Flags = %q, want %q", got, " c123 t60")
		}
		if &resp.Data[0] != dataAddr {
			t.Error("Data did not reuse the supplied backing array")
		}
		if &resp.Flags[0] != flagsAddr {
			t.Error("Flags did not reuse the supplied backing array")
		}
		if resp.Error != nil {
			t.Errorf("Error = %v, want nil", resp.Error)
		}
	})

	t.Run("retains buffers across responses without data or flags", func(t *testing.T) {
		r := bufio.NewReader(strings.NewReader(
			"VA 5 c123\r\nhello\r\n" +
				"EN\r\n" +
				"VA 5 c456\r\nworld\r\n",
		))
		var resp Response

		if err := ReadResponse(r, &resp); err != nil {
			t.Fatalf("first ReadResponse failed: %v", err)
		}
		dataAddr := &resp.Data[0]
		flagsAddr := &resp.Flags[0]

		if err := ReadResponse(r, &resp); err != nil {
			t.Fatalf("second ReadResponse failed: %v", err)
		}
		if resp.Status != StatusEN {
			t.Errorf("second Status = %q, want %q", resp.Status, StatusEN)
		}
		if len(resp.Data) != 0 || len(resp.Flags) != 0 {
			t.Errorf("second response retained contents: Data=%q Flags=%q", resp.Data, resp.Flags)
		}

		if err := ReadResponse(r, &resp); err != nil {
			t.Fatalf("third ReadResponse failed: %v", err)
		}
		if &resp.Data[0] != dataAddr {
			t.Error("Data capacity was not retained across a miss")
		}
		if &resp.Flags[0] != flagsAddr {
			t.Error("Flags capacity was not retained across a response without flags")
		}
		if got := string(resp.Data); got != "world" {
			t.Errorf("third Data = %q, want %q", got, "world")
		}
	})

	t.Run("grows an insufficient data buffer", func(t *testing.T) {
		data := make([]byte, 0, 1)
		dataAddr := &data[:cap(data)][0]
		resp := Response{Data: data}

		r := bufio.NewReader(strings.NewReader("VA 5\r\nhello\r\n"))
		if err := ReadResponse(r, &resp); err != nil {
			t.Fatalf("ReadResponse failed: %v", err)
		}

		if &resp.Data[0] == dataAddr {
			t.Error("Data reused a backing array with insufficient capacity")
		}
	})

	t.Run("reuses data capacity for debug responses", func(t *testing.T) {
		data := make([]byte, 0, 64)
		dataAddr := &data[:cap(data)][0]
		resp := Response{Data: data}

		r := bufio.NewReader(strings.NewReader("ME key exp=60 la=2\r\n"))
		if err := ReadResponse(r, &resp); err != nil {
			t.Fatalf("ReadResponse failed: %v", err)
		}

		if &resp.Data[0] != dataAddr {
			t.Error("Data did not reuse the supplied backing array")
		}
		if got := string(resp.Data); got != "exp=60 la=2" {
			t.Errorf("Data = %q, want %q", got, "exp=60 la=2")
		}
	})

	t.Run("drops an oversized retained data buffer", func(t *testing.T) {
		data := make([]byte, 0, maxRetainedBufferSize+1)
		dataAddr := &data[:cap(data)][0]
		resp := Response{Data: data}

		r := bufio.NewReader(strings.NewReader("VA 5\r\nhello\r\n"))
		if err := ReadResponse(r, &resp); err != nil {
			t.Fatalf("ReadResponse failed: %v", err)
		}

		if &resp.Data[0] == dataAddr {
			t.Error("Data retained a backing array above the 1 MiB retention limit")
		}
	})

	t.Run("truncates data to the bytes read on a partial read failure", func(t *testing.T) {
		var resp Response
		r := bufio.NewReader(strings.NewReader("VA 5\r\nhello\r\n"))
		if err := ReadResponse(r, &resp); err != nil {
			t.Fatalf("ReadResponse failed: %v", err)
		}

		r = bufio.NewReader(strings.NewReader("VA 5\r\nhe"))
		if err := ReadResponse(r, &resp); err == nil {
			t.Fatal("ReadResponse succeeded on a truncated data block")
		}
		if got := string(resp.Data); got != "he" {
			t.Errorf("Data after partial read = %q, want %q (no stale bytes from the previous response)", got, "he")
		}
	})

	t.Run("drops an oversized retained flags buffer", func(t *testing.T) {
		flags := make(Flags, 0, maxRetainedBufferSize+1)
		flagsAddr := &flags[:cap(flags)][0]
		resp := Response{Flags: flags}

		r := bufio.NewReader(strings.NewReader("HD c123\r\n"))
		if err := ReadResponse(r, &resp); err != nil {
			t.Fatalf("ReadResponse failed: %v", err)
		}

		if &resp.Flags[0] == flagsAddr {
			t.Error("Flags retained a backing array above the 1 MiB retention limit")
		}
	})
}
