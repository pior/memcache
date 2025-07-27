package memcache

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

func TestFormatGetCommand(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		flags    []string
		opaque   string
		expected string
	}{
		{
			name:     "simple get",
			key:      "foo",
			flags:    []string{"v"},
			opaque:   "",
			expected: "mg foo v\r\n",
		},
		{
			name:     "get with multiple flags",
			key:      "bar",
			flags:    []string{"v", "f", "t"},
			opaque:   "",
			expected: "mg bar v f t\r\n",
		},
		{
			name:     "get with opaque",
			key:      "baz",
			flags:    []string{"v"},
			opaque:   "123",
			expected: "mg baz v O123\r\n",
		},
		{
			name:     "get with flags and opaque",
			key:      "qux",
			flags:    []string{"v", "f", "c"},
			opaque:   "456",
			expected: "mg qux v f c O456\r\n",
		},
		{
			name:     "empty flags",
			key:      "test",
			flags:    []string{},
			opaque:   "",
			expected: "mg test\r\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatGetCommand(tt.key, tt.flags, tt.opaque)
			if string(result) != tt.expected {
				t.Errorf("FormatGetCommand() = %q, want %q", string(result), tt.expected)
			}
		})
	}
}

func TestFormatGetCommandInvalidKey(t *testing.T) {
	tests := []struct {
		name string
		key  string
	}{
		{"empty key", ""},
		{"key too long", strings.Repeat("a", 251)},
		{"key with space", "foo bar"},
		{"key with control char", "foo\x01"},
		{"key with newline", "foo\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatGetCommand(tt.key, []string{"v"}, "")
			if result != nil {
				t.Errorf("FormatGetCommand() = %v, want nil for invalid key", result)
			}
		})
	}
}

func TestFormatSetCommand(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		value    []byte
		ttl      int
		flags    map[string]string
		opaque   string
		expected string
	}{
		{
			name:     "simple set",
			key:      "foo",
			value:    []byte("hello"),
			ttl:      0,
			flags:    nil,
			opaque:   "",
			expected: "ms foo 5\r\nhello\r\n",
		},
		{
			name:     "set with ttl",
			key:      "bar",
			value:    []byte("world"),
			ttl:      300,
			flags:    nil,
			opaque:   "",
			expected: "ms bar 5 T300\r\nworld\r\n",
		},
		{
			name:     "set with flags",
			key:      "baz",
			value:    []byte("test"),
			ttl:      0,
			flags:    map[string]string{"F": "123"},
			opaque:   "",
			expected: "ms baz 4 F123\r\ntest\r\n",
		},
		{
			name:     "set with opaque",
			key:      "qux",
			value:    []byte("data"),
			ttl:      0,
			flags:    nil,
			opaque:   "789",
			expected: "ms qux 4 O789\r\ndata\r\n",
		},
		{
			name:     "set with all options",
			key:      "complex",
			value:    []byte("complex"),
			ttl:      600,
			flags:    map[string]string{"F": "456", "C": ""},
			opaque:   "999",
			expected: "ms complex 7 T600 F456 C O999\r\ncomplex\r\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatSetCommand(tt.key, tt.value, tt.ttl, tt.flags, tt.opaque)
			resultStr := string(result)

			// For complex test with multiple flags, we need to be flexible about flag order
			if tt.name == "set with all options" {
				if !strings.HasPrefix(resultStr, "ms complex 7 T600") ||
					!strings.HasSuffix(resultStr, "O999\r\ncomplex\r\n") ||
					!strings.Contains(resultStr, "F456") ||
					!strings.Contains(resultStr, "C") {
					t.Errorf("FormatSetCommand() = %q, want something containing all expected parts", resultStr)
				}
			} else {
				if resultStr != tt.expected {
					t.Errorf("FormatSetCommand() = %q, want %q", resultStr, tt.expected)
				}
			}
		})
	}
}

func TestFormatDeleteCommand(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		opaque   string
		expected string
	}{
		{
			name:     "simple delete",
			key:      "foo",
			opaque:   "",
			expected: "md foo\r\n",
		},
		{
			name:     "delete with opaque",
			key:      "bar",
			opaque:   "123",
			expected: "md bar O123\r\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatDeleteCommand(tt.key, tt.opaque)
			if string(result) != tt.expected {
				t.Errorf("FormatDeleteCommand() = %q, want %q", string(result), tt.expected)
			}
		})
	}
}

func TestParseResponse(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected *metaResponse
		wantErr  bool
	}{
		{
			name:  "HD response",
			input: "HD\r\n",
			expected: &metaResponse{
				Status: "HD",
				Flags:  map[string]string{},
			},
		},
		{
			name:  "VA response with value",
			input: "VA 5\r\nhello\r\n",
			expected: &metaResponse{
				Status: "VA",
				Flags:  map[string]string{},
				Value:  []byte("hello"),
			},
		},
		{
			name:  "VA response with value and size flag",
			input: "VA 11 s11\r\nhello world\r\n",
			expected: &metaResponse{
				Status: "VA",
				Flags:  map[string]string{"s": "11"},
				Value:  []byte("hello world"),
			},
		},
		{
			name:  "response with opaque",
			input: "HD O123\r\n",
			expected: &metaResponse{
				Status: "HD",
				Flags:  map[string]string{},
				Opaque: "123",
			},
		},
		{
			name:  "response with flags",
			input: "VA f30 c456\r\n",
			expected: &metaResponse{
				Status: "VA",
				Flags:  map[string]string{"f30": "", "c456": ""},
			},
		},
		{
			name:    "empty response",
			input:   "\r\n",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := bufio.NewReader(strings.NewReader(tt.input))
			result, err := ParseResponse(reader)

			if tt.wantErr {
				if err == nil {
					t.Errorf("ParseResponse() expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("ParseResponse() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if result.Status != tt.expected.Status {
				t.Errorf("ParseResponse() Status = %v, want %v", result.Status, tt.expected.Status)
			}

			if result.Opaque != tt.expected.Opaque {
				t.Errorf("ParseResponse() Opaque = %v, want %v", result.Opaque, tt.expected.Opaque)
			}

			if !bytes.Equal(result.Value, tt.expected.Value) {
				t.Errorf("ParseResponse() Value = %v, want %v", result.Value, tt.expected.Value)
			}

			if len(result.Flags) != len(tt.expected.Flags) {
				t.Errorf("ParseResponse() Flags length = %v, want %v", len(result.Flags), len(tt.expected.Flags))
			}

			for k, v := range tt.expected.Flags {
				if result.Flags[k] != v {
					t.Errorf("ParseResponse() Flags[%s] = %v, want %v", k, result.Flags[k], v)
				}
			}
		})
	}
}

func TestIsValidKey(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		valid bool
	}{
		{"valid key", "foo", true},
		{"valid key with numbers", "foo123", true},
		{"valid key with underscores", "foo_bar", true},
		{"valid key with dashes", "foo-bar", true},
		{"empty key", "", false},
		{"key too long", strings.Repeat("a", 251), false},
		{"key with space", "foo bar", false},
		{"key with tab", "foo\tbar", false},
		{"key with newline", "foo\nbar", false},
		{"key with carriage return", "foo\rbar", false},
		{"key with null", "foo\x00bar", false},
		{"key with DEL", "foo\x7fbar", false},
		{"exactly 250 chars", strings.Repeat("a", 250), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidKey(tt.key)
			if result != tt.valid {
				t.Errorf("isValidKey(%q) = %v, want %v", tt.key, result, tt.valid)
			}
		})
	}
}

func TestCommandToProtocol(t *testing.T) {
	tests := []struct {
		name string
		cmd  *Command
		want string
	}{
		{
			name: "get command",
			cmd:  NewGetCommand("testkey"),
			want: "mg testkey v O",
		},
		{
			name: "set command",
			cmd:  NewSetCommand("testkey", []byte("value"), 0),
			want: "ms testkey 5",
		},
		{
			name: "delete command",
			cmd:  NewDeleteCommand("testkey"),
			want: "md testkey O",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := commandToProtocol(tt.cmd)
			if result == nil {
				t.Error("commandToProtocol returned nil")
				return
			}

			resultStr := string(result)
			// Just check that the command starts correctly since opaque is random
			if tt.cmd.Type == "mg" && !contains(resultStr, "mg testkey v") {
				t.Errorf("Get command should contain 'mg testkey v', got: %s", resultStr)
			}
			if tt.cmd.Type == "ms" && !contains(resultStr, "ms testkey 5") {
				t.Errorf("Set command should contain 'ms testkey 5', got: %s", resultStr)
			}
			if tt.cmd.Type == "md" && !contains(resultStr, "md testkey") {
				t.Errorf("Delete command should contain 'md testkey', got: %s", resultStr)
			}
		})
	}
}

func TestCommandToProtocolUnsupported(t *testing.T) {
	cmd := &Command{
		Type: "unsupported",
		Key:  "test",
	}

	result := commandToProtocol(cmd)
	if result != nil {
		t.Error("unsupported command should return nil")
	}
}

func TestProtocolToResponse(t *testing.T) {
	tests := []struct {
		name           string
		metaResp       *metaResponse
		originalKey    string
		expectError    bool
		expectedStatus string
	}{
		{
			name: "successful response",
			metaResp: &metaResponse{
				Status: "HD",
				Value:  []byte("test_value"),
				Flags:  map[string]string{"s": "10"},
				Opaque: "1234",
			},
			originalKey:    "test_key",
			expectError:    false,
			expectedStatus: "HD",
		},
		{
			name: "cache miss response",
			metaResp: &metaResponse{
				Status: "EN",
				Opaque: "1234",
			},
			originalKey:    "missing_key",
			expectError:    true,
			expectedStatus: "EN",
		},
		{
			name: "unknown status response",
			metaResp: &metaResponse{
				Status: "XX",
				Opaque: "1234",
			},
			originalKey:    "test_key",
			expectError:    true,
			expectedStatus: "XX",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := protocolToResponse(tt.metaResp, tt.originalKey)

			if resp.Key != tt.originalKey {
				t.Errorf("Expected key %s, got %s", tt.originalKey, resp.Key)
			}

			if resp.Status != tt.expectedStatus {
				t.Errorf("Expected status %s, got %s", tt.expectedStatus, resp.Status)
			}

			if tt.expectError && resp.Error == nil {
				t.Error("Expected error but got none")
			}

			if !tt.expectError && resp.Error != nil {
				t.Errorf("Unexpected error: %v", resp.Error)
			}

			if tt.metaResp.Status == "HD" && string(resp.Value) != string(tt.metaResp.Value) {
				t.Errorf("Expected value %s, got %s", tt.metaResp.Value, resp.Value)
			}
		})
	}
}

// Helper function to check if string contains substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && findSubstring(s, substr)
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestGenerateOpaque(t *testing.T) {
	opaque1 := generateOpaque()
	opaque2 := generateOpaque()

	if opaque1 == opaque2 {
		t.Error("generated opaques should be different")
	}

	if len(opaque1) != 8 { // 4 bytes = 8 hex chars
		t.Errorf("expected opaque length 8, got %d", len(opaque1))
	}

	if len(opaque2) != 8 {
		t.Errorf("expected opaque length 8, got %d", len(opaque2))
	}

	// Test that it's valid hex
	for _, char := range opaque1 {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
			t.Errorf("opaque contains invalid hex character: %c", char)
		}
	}
}

// Tests for constants and new protocol features
func TestConstants(t *testing.T) {
	// Test that constants are defined correctly
	tests := []struct {
		name     string
		constant string
		expected string
	}{
		{"Meta Get", CmdMetaGet, "mg"},
		{"Meta Set", CmdMetaSet, "ms"},
		{"Meta Delete", CmdMetaDelete, "md"},
		{"Meta Arithmetic", CmdMetaArithmetic, "ma"},
		{"Meta Debug", CmdMetaDebug, "me"},
		{"Meta NoOp", CmdMetaNoOp, "mn"},
		{"Status HD", StatusHD, "HD"},
		{"Status VA", StatusVA, "VA"},
		{"Status EN", StatusEN, "EN"},
		{"Status NS", StatusNS, "NS"},
		{"Flag Value", FlagValue, "v"},
		{"Flag CAS", FlagCAS, "c"},
		{"Flag TTL", FlagTTL, "t"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.constant != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, tt.constant)
			}
		})
	}
}

func TestFormatArithmeticCommand(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		flags    map[string]string
		opaque   string
		expected string
	}{
		{
			name:     "simple increment",
			key:      "counter",
			flags:    map[string]string{FlagDelta: "5"},
			opaque:   "",
			expected: "ma counter D5\r\n",
		},
		{
			name:     "increment with mode",
			key:      "counter",
			flags:    map[string]string{FlagDelta: "10", FlagMode: ArithIncrement},
			opaque:   "",
			expected: "ma counter D10 MI\r\n",
		},
		{
			name:     "decrement with opaque",
			key:      "counter",
			flags:    map[string]string{FlagDelta: "3", FlagMode: ArithDecrement},
			opaque:   "abc123",
			expected: "ma counter D3 MD Oabc123\r\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatArithmeticCommand(tt.key, tt.flags, tt.opaque)
			if string(result) != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, string(result))
			}
		})
	}
}

func TestFormatDebugCommand(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		flags    map[string]string
		opaque   string
		expected string
	}{
		{
			name:     "simple debug",
			key:      "",
			flags:    map[string]string{},
			opaque:   "",
			expected: "me\r\n",
		},
		{
			name:     "debug with key",
			key:      "debug-key",
			flags:    map[string]string{},
			opaque:   "",
			expected: "me debug-key\r\n",
		},
		{
			name:     "debug with flags",
			key:      "debug-key",
			flags:    map[string]string{"t": ""},
			opaque:   "",
			expected: "me debug-key t\r\n",
		},
		{
			name:     "debug with opaque",
			key:      "",
			flags:    map[string]string{},
			opaque:   "xyz789",
			expected: "me Oxyz789\r\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatDebugCommand(tt.key, tt.flags, tt.opaque)
			if string(result) != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, string(result))
			}
		})
	}
}

func TestFormatNoOpCommand(t *testing.T) {
	tests := []struct {
		name     string
		opaque   string
		expected string
	}{
		{
			name:     "simple noop",
			opaque:   "",
			expected: "mn\r\n",
		},
		{
			name:     "noop with opaque",
			opaque:   "def456",
			expected: "mn Odef456\r\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatNoOpCommand(tt.opaque)
			if string(result) != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, string(result))
			}
		})
	}
}

func TestCommandToProtocolExtended(t *testing.T) {
	tests := []struct {
		name     string
		command  *Command
		contains []string
	}{
		{
			name:     "Meta Arithmetic",
			command:  NewIncrementCommand("counter", 5),
			contains: []string{"ma", "counter", "D5", "MI"},
		},
		{
			name:     "Meta Debug",
			command:  NewDebugCommand("debug-key"),
			contains: []string{"me", "debug-key"},
		},
		{
			name:     "Meta NoOp",
			command:  NewNoOpCommand(),
			contains: []string{"mn"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := commandToProtocol(tt.command)
			if result == nil {
				t.Fatal("commandToProtocol returned nil")
			}

			output := string(result)
			for _, expected := range tt.contains {
				if !strings.Contains(output, expected) {
					t.Errorf("expected output to contain %q, got %q", expected, output)
				}
			}
		})
	}
}

func TestProtocolToResponseExtended(t *testing.T) {
	tests := []struct {
		name        string
		metaResp    *metaResponse
		originalKey string
		expectError bool
		errorType   error
	}{
		{
			name: "Success MN response",
			metaResp: &metaResponse{
				Status: StatusMN,
				Flags:  map[string]string{},
			},
			originalKey: "test-key",
			expectError: false,
		},
		{
			name: "Success ME response",
			metaResp: &metaResponse{
				Status: StatusME,
				Flags:  map[string]string{},
			},
			originalKey: "debug-key",
			expectError: false,
		},
		{
			name: "Not found NF response",
			metaResp: &metaResponse{
				Status: StatusNF,
				Flags:  map[string]string{},
			},
			originalKey: "missing-key",
			expectError: true,
			errorType:   ErrCacheMiss,
		},
		{
			name: "Exists EX response",
			metaResp: &metaResponse{
				Status: StatusEX,
				Flags:  map[string]string{},
			},
			originalKey: "existing-key",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := protocolToResponse(tt.metaResp, tt.originalKey)

			if resp.Key != tt.originalKey {
				t.Errorf("expected key %s, got %s", tt.originalKey, resp.Key)
			}

			if tt.expectError && resp.Error == nil {
				t.Error("expected error but got none")
			}

			if !tt.expectError && resp.Error != nil {
				t.Errorf("expected no error but got: %v", resp.Error)
			}

			if tt.errorType != nil && resp.Error != tt.errorType {
				t.Errorf("expected error type %v, got %v", tt.errorType, resp.Error)
			}
		})
	}
}

func TestKeyValidationWithConstants(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		valid bool
	}{
		{"Valid key", "test-key", true},
		{"Empty key", "", false},
		{"Too long key", strings.Repeat("a", MaxKeyLength+1), false},
		{"Max length key", strings.Repeat("a", MaxKeyLength), true},
		{"Key with space", "test key", false},
		{"Key with control char", "test\x01key", false},
		{"Key with DEL char", "test\x7fkey", false},
		{"Unicode key", "test-ключ", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidKey(tt.key)
			if result != tt.valid {
				t.Errorf("expected %v for key %q, got %v", tt.valid, tt.key, result)
			}
		})
	}
}
