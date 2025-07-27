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
			input: "VA s5\r\nhello\r\n",
			expected: &metaResponse{
				Status: "VA",
				Flags:  map[string]string{},
				Value:  []byte("hello"),
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
