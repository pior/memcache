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
			req:      NewRequest(CmdGet, "mykey", nil),
			expected: "mg mykey\r\n",
		},
		{
			name: "get with value flag",
			req: NewRequest(CmdGet, "mykey", nil,
				Flag{Type: FlagReturnValue},
			),
			expected: "mg mykey v\r\n",
		},
		{
			name: "get with multiple flags",
			req: NewRequest(CmdGet, "mykey", nil,
				Flag{Type: FlagReturnValue},
				Flag{Type: FlagReturnCAS},
				Flag{Type: FlagReturnTTL},
			),
			expected: "mg mykey v c t\r\n",
		},
		{
			name: "get with token flags",
			req: NewRequest(CmdGet, "mykey", nil,
				Flag{Type: FlagReturnValue},
				Flag{Type: FlagOpaque, Token: "mytoken"},
			),
			expected: "mg mykey v Omytoken\r\n",
		},
		{
			name: "get with recache flag",
			req: NewRequest(CmdGet, "mykey", nil,
				Flag{Type: FlagReturnValue},
				Flag{Type: FlagRecache, Token: "30"},
			),
			expected: "mg mykey v R30\r\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			n, err := WriteRequest(&buf, tt.req)
			if err != nil {
				t.Fatalf("WriteRequest failed: %v", err)
			}
			if got := buf.String(); got != tt.expected {
				t.Errorf("WriteRequest() = %q, want %q", got, tt.expected)
			}
			if n != len(tt.expected) {
				t.Errorf("WriteRequest() returned n=%d, want %d", n, len(tt.expected))
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
			name: "set with TTL",
			req: NewRequest(CmdSet, "mykey", []byte("hello"),
				Flag{Type: FlagTTL, Token: "60"},
			),
			expected: "ms mykey 5 T60\r\nhello\r\n",
		},
		{
			name: "set with mode",
			req: NewRequest(CmdSet, "mykey", []byte("hello"),
				Flag{Type: FlagMode, Token: ModeAdd},
			),
			expected: "ms mykey 5 ME\r\nhello\r\n",
		},
		{
			name: "set with CAS and flags",
			req: NewRequest(CmdSet, "mykey", []byte("hello"),
				Flag{Type: FlagCAS, Token: "12345"},
				Flag{Type: FlagClientFlags, Token: "30"},
			),
			expected: "ms mykey 5 C12345 F30\r\nhello\r\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			n, err := WriteRequest(&buf, tt.req)
			if err != nil {
				t.Fatalf("WriteRequest failed: %v", err)
			}
			if got := buf.String(); got != tt.expected {
				t.Errorf("WriteRequest() = %q, want %q", got, tt.expected)
			}
			if n != len(tt.expected) {
				t.Errorf("WriteRequest() returned n=%d, want %d", n, len(tt.expected))
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
			name: "delete with invalidate",
			req: NewRequest(CmdDelete, "mykey", nil,
				Flag{Type: FlagInvalidate},
				Flag{Type: FlagTTL, Token: "30"},
			),
			expected: "md mykey I T30\r\n",
		},
		{
			name: "delete with CAS",
			req: NewRequest(CmdDelete, "mykey", nil,
				Flag{Type: FlagCAS, Token: "12345"},
			),
			expected: "md mykey C12345\r\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			n, err := WriteRequest(&buf, tt.req)
			if err != nil {
				t.Fatalf("WriteRequest failed: %v", err)
			}
			if got := buf.String(); got != tt.expected {
				t.Errorf("WriteRequest() = %q, want %q", got, tt.expected)
			}
			if n != len(tt.expected) {
				t.Errorf("WriteRequest() returned n=%d, want %d", n, len(tt.expected))
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
			req: NewRequest(CmdArithmetic, "counter", nil,
				Flag{Type: FlagReturnValue},
			),
			expected: "ma counter v\r\n",
		},
		{
			name: "increment with delta",
			req: NewRequest(CmdArithmetic, "counter", nil,
				Flag{Type: FlagReturnValue},
				Flag{Type: FlagDelta, Token: "5"},
			),
			expected: "ma counter v D5\r\n",
		},
		{
			name: "decrement",
			req: NewRequest(CmdArithmetic, "counter", nil,
				Flag{Type: FlagReturnValue},
				Flag{Type: FlagMode, Token: ModeDecrement},
			),
			expected: "ma counter v MD\r\n",
		},
		{
			name: "auto-create with initial value",
			req: NewRequest(CmdArithmetic, "counter", nil,
				Flag{Type: FlagReturnValue},
				Flag{Type: FlagVivify, Token: "60"},
				Flag{Type: FlagInitialValue, Token: "100"},
			),
			expected: "ma counter v N60 J100\r\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			n, err := WriteRequest(&buf, tt.req)
			if err != nil {
				t.Fatalf("WriteRequest failed: %v", err)
			}
			if got := buf.String(); got != tt.expected {
				t.Errorf("WriteRequest() = %q, want %q", got, tt.expected)
			}
			if n != len(tt.expected) {
				t.Errorf("WriteRequest() returned n=%d, want %d", n, len(tt.expected))
			}
		})
	}
}

func TestWriteNoOpRequest(t *testing.T) {
	req := NewRequest(CmdNoOp, "", nil)
	var buf bytes.Buffer
	n, err := WriteRequest(&buf, req)
	if err != nil {
		t.Fatalf("WriteRequest failed: %v", err)
	}
	expected := "mn\r\n"
	if got := buf.String(); got != expected {
		t.Errorf("WriteRequest() = %q, want %q", got, expected)
	}
	if n != len(expected) {
		t.Errorf("WriteRequest() returned n=%d, want %d", n, len(expected))
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
					{Type: FlagReturnCAS, Token: "12345"},
					{Type: FlagReturnTTL, Token: "3600"},
				},
			},
		},
		{
			name:  "HD with opaque",
			input: "HD Omytoken\r\n",
			expected: &Response{
				Status: StatusHD,
				Flags: []Flag{
					{Type: FlagOpaque, Token: "mytoken"},
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
				if flag.Token != tt.expected.Flags[i].Token {
					t.Errorf("Flag[%d].Token = %q, want %q", i, flag.Token, tt.expected.Flags[i].Token)
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
					{Type: FlagReturnCAS, Token: "12345"},
					{Type: FlagReturnTTL, Token: "3600"},
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
		NewRequest(CmdGet, "key1", nil, Flag{Type: FlagReturnValue}, Flag{Type: FlagQuiet}),
		NewRequest(CmdGet, "key2", nil, Flag{Type: FlagReturnValue}, Flag{Type: FlagQuiet}),
		NewRequest(CmdGet, "key3", nil, Flag{Type: FlagReturnValue}),
		NewRequest(CmdNoOp, "", nil),
	}

	var buf bytes.Buffer
	var total int
	for _, req := range reqs {
		n, err := WriteRequest(&buf, req)
		if err != nil {
			t.Fatalf("WriteRequest failed: %v", err)
		}
		total += n
	}

	expected := "mg key1 v q\r\nmg key2 v q\r\nmg key3 v\r\nmn\r\n"
	if got := buf.String(); got != expected {
		t.Errorf("Multiple WriteRequest() = %q, want %q", got, expected)
	}
	if total != len(expected) {
		t.Errorf("Multiple WriteRequest() returned n=%d, want %d", total, len(expected))
	}
}

func TestReadResponseBatch(t *testing.T) {
	input := "VA 5 kmykey\r\nhello\r\nHD\r\nMN\r\n"
	r := bufio.NewReader(strings.NewReader(input))

	resps, err := ReadResponseBatch(r, 0, true)
	if err != nil {
		t.Fatalf("ReadResponseBatch failed: %v", err)
	}

	if len(resps) != 3 {
		t.Errorf("ReadResponseBatch() returned %d responses, want 3", len(resps))
	}

	if resps[0].Status != StatusVA {
		t.Errorf("Response[0].Status = %q, want %q", resps[0].Status, StatusVA)
	}
	if resps[1].Status != StatusHD {
		t.Errorf("Response[1].Status = %q, want %q", resps[1].Status, StatusHD)
	}
	if resps[2].Status != StatusMN {
		t.Errorf("Response[2].Status = %q, want %q", resps[2].Status, StatusMN)
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
				{Type: FlagReturnCAS, Token: "12345"},
				{Type: FlagReturnTTL, Token: "3600"},
			},
		}
		if got := resp.GetFlagToken(FlagReturnCAS); got != "12345" {
			t.Errorf("GetFlagToken('c') = %q, want %q", got, "12345")
		}
		if got := resp.GetFlagToken(FlagReturnTTL); got != "3600" {
			t.Errorf("GetFlagToken('t') = %q, want %q", got, "3600")
		}
		if got := resp.GetFlagToken('x'); got != "" {
			t.Errorf("GetFlagToken('x') = %q, want empty", got)
		}
	})
}

func TestRequest_HelperMethods(t *testing.T) {
	t.Run("HasFlag", func(t *testing.T) {
		req := NewRequest(CmdGet, "mykey", nil,
			Flag{Type: FlagReturnValue},
			Flag{Type: FlagReturnCAS},
		)

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
		req := NewRequest(CmdGet, "mykey", nil,
			Flag{Type: FlagRecache, Token: "30"},
		)

		flag, ok := req.GetFlag(FlagRecache)
		if !ok {
			t.Error("GetFlag('R') ok = false, want true")
		}
		if flag.Token != "30" {
			t.Errorf("GetFlag('R').Token = %q, want %q", flag.Token, "30")
		}

		_, ok = req.GetFlag('x')
		if ok {
			t.Error("GetFlag('x') ok = true, want false")
		}
	})

	t.Run("AddFlag", func(t *testing.T) {
		req := NewRequest(CmdGet, "mykey", nil)
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
			status, err := PeekStatus(r)
			if err != nil {
				t.Fatalf("PeekStatus failed: %v", err)
			}
			if status != tt.expected {
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
