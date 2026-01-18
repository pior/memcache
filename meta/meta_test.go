package meta

import (
	"bufio"
	"bytes"
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
			req:      NewRequest(CmdGet, "mykey", nil, nil),
			expected: "mg mykey\r\n",
		},
		{
			name: "get with value flag",
			req: NewRequest(CmdGet, "mykey", nil, []Flag{
				{Type: FlagReturnValue},
			}),
			expected: "mg mykey v\r\n",
		},
		{
			name: "get with multiple flags",
			req: NewRequest(CmdGet, "mykey", nil, []Flag{
				{Type: FlagReturnValue},
				{Type: FlagReturnCAS},
				{Type: FlagReturnTTL},
			}),
			expected: "mg mykey v c t\r\n",
		},
		{
			name: "get with token flags",
			req: NewRequest(CmdGet, "mykey", nil, []Flag{
				{Type: FlagReturnValue},
				{Type: FlagOpaque, Token: []byte("mytoken")},
			}),
			expected: "mg mykey v Omytoken\r\n",
		},
		{
			name: "get with recache flag",
			req: NewRequest(CmdGet, "mykey", nil, []Flag{
				{Type: FlagReturnValue},
				{Type: FlagRecache, Token: []byte("30")},
			}),
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
			req:      NewRequest(CmdSet, "mykey", []byte("hello"), nil),
			expected: "ms mykey 5\r\nhello\r\n",
		},
		{
			name:     "set with zero-length value",
			req:      NewRequest(CmdSet, "mykey", []byte(""), nil),
			expected: "ms mykey 0\r\n\r\n",
		},
		{
			name: "set with TTL",
			req: NewRequest(CmdSet, "mykey", []byte("hello"), []Flag{
				{Type: FlagTTL, Token: []byte("60")},
			}),
			expected: "ms mykey 5 T60\r\nhello\r\n",
		},
		{
			name: "set with mode",
			req: NewRequest(CmdSet, "mykey", []byte("hello"), []Flag{
				{Type: FlagMode, Token: []byte(ModeAdd)},
			}),
			expected: "ms mykey 5 ME\r\nhello\r\n",
		},
		{
			name: "set with CAS and flags",
			req: NewRequest(CmdSet, "mykey", []byte("hello"), []Flag{
				{Type: FlagCAS, Token: []byte("12345")},
				{Type: FlagClientFlags, Token: []byte("30")},
			}),
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
			req:      NewRequest(CmdDelete, "mykey", nil, nil),
			expected: "md mykey\r\n",
		},
		{
			name: "delete with invalidate",
			req: NewRequest(CmdDelete, "mykey", nil, []Flag{
				{Type: FlagInvalidate},
				{Type: FlagTTL, Token: []byte("30")},
			}),
			expected: "md mykey I T30\r\n",
		},
		{
			name: "delete with CAS",
			req: NewRequest(CmdDelete, "mykey", nil, []Flag{
				{Type: FlagCAS, Token: []byte("12345")},
			}),
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
			name: "basic increment",
			req: NewRequest(CmdArithmetic, "counter", nil, []Flag{
				{Type: FlagReturnValue},
			}),
			expected: "ma counter v\r\n",
		},
		{
			name: "increment with delta",
			req: NewRequest(CmdArithmetic, "counter", nil, []Flag{
				{Type: FlagReturnValue},
				{Type: FlagDelta, Token: []byte("5")},
			}),
			expected: "ma counter v D5\r\n",
		},
		{
			name: "decrement",
			req: NewRequest(CmdArithmetic, "counter", nil, []Flag{
				{Type: FlagReturnValue},
				{Type: FlagMode, Token: []byte(ModeDecrement)},
			}),
			expected: "ma counter v MD\r\n",
		},
		{
			name: "auto-create with initial value",
			req: NewRequest(CmdArithmetic, "counter", nil, []Flag{
				{Type: FlagReturnValue},
				{Type: FlagVivify, Token: []byte("60")},
				{Type: FlagInitialValue, Token: []byte("100")},
			}),
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
	req := NewRequest(CmdNoOp, "", nil, nil)
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
				Flags:  []Flag{},
			},
		},
		{
			name:  "HD with flags",
			input: "HD c12345 t3600\r\n",
			expected: &Response{
				Status: StatusHD,
				Flags: []Flag{
					{Type: FlagReturnCAS, Token: []byte("12345")},
					{Type: FlagReturnTTL, Token: []byte("3600")},
				},
			},
		},
		{
			name:  "HD with opaque",
			input: "HD Omytoken\r\n",
			expected: &Response{
				Status: StatusHD,
				Flags: []Flag{
					{Type: FlagOpaque, Token: []byte("mytoken")},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := bufio.NewReader(strings.NewReader(tt.input))
			resp, err := ReadResponse(r)
			if err != nil {
				t.Fatalf("ReadResponse failed: %v", err)
			}
			if resp.Status != tt.expected.Status {
				t.Errorf("Status = %q, want %q", resp.Status, tt.expected.Status)
			}
			if len(resp.Flags) != len(tt.expected.Flags) {
				t.Errorf("Flags length = %d, want %d", len(resp.Flags), len(tt.expected.Flags))
			}
			for i, flag := range resp.Flags {
				if flag.Type != tt.expected.Flags[i].Type {
					t.Errorf("Flag[%d].Type = %c, want %c", i, flag.Type, tt.expected.Flags[i].Type)
				}
				if !bytes.Equal(flag.Token, tt.expected.Flags[i].Token) {
					t.Errorf("Flag[%d].Token = %q, want %q", i, string(flag.Token), string(tt.expected.Flags[i].Token))
				}
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
				Flags:  []Flag{},
			},
		},
		{
			name:  "VA with flags",
			input: "VA 5 c12345 t3600\r\nhello\r\n",
			expected: &Response{
				Status: StatusVA,
				Data:   []byte("hello"),
				Flags: []Flag{
					{Type: FlagReturnCAS, Token: []byte("12345")},
					{Type: FlagReturnTTL, Token: []byte("3600")},
				},
			},
		},
		{
			name:  "VA with win flag",
			input: "VA 5 W\r\nhello\r\n",
			expected: &Response{
				Status: StatusVA,
				Data:   []byte("hello"),
				Flags: []Flag{
					{Type: FlagWin},
				},
			},
		},
		{
			name:  "VA with stale and win",
			input: "VA 5 X W\r\nhello\r\n",
			expected: &Response{
				Status: StatusVA,
				Data:   []byte("hello"),
				Flags: []Flag{
					{Type: FlagStale},
					{Type: FlagWin},
				},
			},
		},
		{
			name:  "VA zero-length",
			input: "VA 0\r\n\r\n",
			expected: &Response{
				Status: StatusVA,
				Data:   []byte{},
				Flags:  []Flag{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := bufio.NewReader(strings.NewReader(tt.input))
			resp, err := ReadResponse(r)
			if err != nil {
				t.Fatalf("ReadResponse failed: %v", err)
			}
			if resp.Status != tt.expected.Status {
				t.Errorf("Status = %q, want %q", resp.Status, tt.expected.Status)
			}
			if !bytes.Equal(resp.Data, tt.expected.Data) {
				t.Errorf("Data = %q, want %q", resp.Data, tt.expected.Data)
			}
			if len(resp.Flags) != len(tt.expected.Flags) {
				t.Errorf("Flags length = %d, want %d", len(resp.Flags), len(tt.expected.Flags))
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
			_, err := ReadResponse(r)
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
			resp, err := ReadResponse(r)
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
			resp, err := ReadResponse(r)
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
		NewRequest(CmdGet, "key1", nil, []Flag{{Type: FlagReturnValue}, {Type: FlagQuiet}}),
		NewRequest(CmdGet, "key2", nil, []Flag{{Type: FlagReturnValue}, {Type: FlagQuiet}}),
		NewRequest(CmdGet, "key3", nil, []Flag{{Type: FlagReturnValue}}),
		NewRequest(CmdNoOp, "", nil, nil),
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

	t.Run("HasWinFlag", func(t *testing.T) {
		resp := &Response{
			Flags: []Flag{
				{Type: FlagWin},
			},
		}
		if !resp.HasWinFlag() {
			t.Error("HasWinFlag() = false, want true")
		}
	})

	t.Run("GetFlagToken", func(t *testing.T) {
		resp := &Response{
			Flags: []Flag{
				{Type: FlagReturnCAS, Token: []byte("12345")},
				{Type: FlagReturnTTL, Token: []byte("3600")},
			},
		}
		if got := resp.GetFlagTokenString(FlagReturnCAS); got != "12345" {
			t.Errorf("GetFlagTokenString('c') = %q, want %q", got, "12345")
		}
		if got := resp.GetFlagTokenString(FlagReturnTTL); got != "3600" {
			t.Errorf("GetFlagTokenString('t') = %q, want %q", got, "3600")
		}
		if got := resp.GetFlagTokenString('x'); got != "" {
			t.Errorf("GetFlagTokenString('x') = %q, want empty", got)
		}
	})
}

func TestRequest_HelperMethods(t *testing.T) {
	t.Run("HasFlag", func(t *testing.T) {
		req := NewRequest(CmdGet, "mykey", nil, []Flag{
			{Type: FlagReturnValue},
			{Type: FlagReturnCAS},
		})

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

	t.Run("GetFlag", func(t *testing.T) {
		req := NewRequest(CmdGet, "mykey", nil, []Flag{
			{Type: FlagRecache, Token: []byte("30")},
		})

		flag, ok := req.GetFlag(FlagRecache)
		if !ok {
			t.Error("GetFlag('R') ok = false, want true")
		}
		if string(flag.Token) != "30" {
			t.Errorf("GetFlag('R').Token = %q, want %q", string(flag.Token), "30")
		}

		_, ok = req.GetFlag('x')
		if ok {
			t.Error("GetFlag('x') ok = true, want false")
		}
	})

	t.Run("AddFlag", func(t *testing.T) {
		req := NewRequest(CmdGet, "mykey", nil, nil)
		req.AddFlag(Flag{Type: FlagReturnValue})

		if !req.HasFlag(FlagReturnValue) {
			t.Error("HasFlag('v') after AddFlag = false, want true")
		}
	})
}

// Test PeekStatus

func TestPeekStatus(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "HD status",
			input:    "HD\r\n",
			expected: "HD",
		},
		{
			name:     "VA status",
			input:    "VA 5\r\nhello\r\n",
			expected: "VA",
		},
		{
			name:     "EN status",
			input:    "EN\r\n",
			expected: "EN",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := bufio.NewReader(strings.NewReader(tt.input))
			status, err := r.Peek(2)
			if err != nil {
				t.Fatalf("PeekStatus failed: %v", err)
			}
			if string(status) != tt.expected {
				t.Errorf("PeekStatus() = %q, want %q", status, tt.expected)
			}

			// Verify peek doesn't consume data
			resp, err := ReadResponse(r)
			if err != nil {
				t.Fatalf("ReadResponse after peek failed: %v", err)
			}
			if string(resp.Status) != tt.expected {
				t.Errorf("Response.Status after peek = %q, want %q", resp.Status, tt.expected)
			}
		})
	}
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
			req:  NewRequest(CmdGet, "", nil, nil),
		},
		{
			name: "key too long",
			req:  NewRequest(CmdGet, string(make([]byte, 251)), nil, nil),
		},
		{
			name: "key with space",
			req:  NewRequest(CmdGet, "my key", nil, nil),
		},
		{
			name: "key with tab",
			req:  NewRequest(CmdGet, "my\tkey", nil, nil),
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
	req := NewRequest(CmdGet, "bXkga2V5", nil, []Flag{{Type: FlagBase64Key}})

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

	resp, err := ReadResponse(r)
	if err != nil {
		t.Fatalf("ReadResponse() error = %v", err)
	}

	if resp.Status != StatusME {
		t.Errorf("ReadResponse().Status = %q, want %q", resp.Status, StatusME)
	}

	if resp.Data != nil {
		t.Errorf("ReadResponse().Data = %v, want nil (no debug params)", resp.Data)
	}
}

func TestReadResponse_ME_WithParams(t *testing.T) {
	input := "ME mykey size=1024 ttl=3600\r\n"
	r := bufio.NewReader(strings.NewReader(input))

	resp, err := ReadResponse(r)
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

// Test FormatFlagInt with cached and non-cached values
func TestFormatFlagInt(t *testing.T) {
	tests := []struct {
		name      string
		flagType  FlagType
		value     int
		wantType  FlagType
		wantToken string
	}{
		// Small values (0-100) are handled by strconv.Itoa's internal cache
		{name: "small: zero", flagType: FlagTTL, value: 0, wantType: FlagTTL, wantToken: "0"},
		{name: "small: delta 1", flagType: FlagDelta, value: 1, wantType: FlagDelta, wantToken: "1"},
		{name: "small: 1 minute", flagType: FlagTTL, value: 60, wantType: FlagTTL, wantToken: "60"},

		// Larger TTL values cached by our map
		{name: "cached: 5 minutes", flagType: FlagTTL, value: 300, wantType: FlagTTL, wantToken: "300"},
		{name: "cached: 1 hour", flagType: FlagTTL, value: 3600, wantType: FlagTTL, wantToken: "3600"},
		{name: "cached: 1 day", flagType: FlagTTL, value: 86400, wantType: FlagTTL, wantToken: "86400"},

		// Non-cached values
		{name: "non-cached: custom TTL", flagType: FlagTTL, value: 42, wantType: FlagTTL, wantToken: "42"},
		{name: "non-cached: large TTL", flagType: FlagTTL, value: 99999, wantType: FlagTTL, wantToken: "99999"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flag := FormatFlagInt(tt.flagType, tt.value)
			if flag.Type != tt.wantType {
				t.Errorf("FormatFlagInt().Type = %v, want %v", flag.Type, tt.wantType)
			}
			if got := string(flag.Token); got != tt.wantToken {
				t.Errorf("FormatFlagInt().Token = %q, want %q", got, tt.wantToken)
			}
		})
	}
}
