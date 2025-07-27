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
		expected *MetaResponse
		wantErr  bool
	}{
		{
			name:  "HD response",
			input: "HD\r\n",
			expected: &MetaResponse{
				Status: "HD",
				Flags:  map[string]string{},
			},
		},
		{
			name:  "VA response with value",
			input: "VA s5\r\nhello\r\n",
			expected: &MetaResponse{
				Status: "VA",
				Flags:  map[string]string{},
				Value:  []byte("hello"),
			},
		},
		{
			name:  "response with opaque",
			input: "HD O123\r\n",
			expected: &MetaResponse{
				Status: "HD",
				Flags:  map[string]string{},
				Opaque: "123",
			},
		},
		{
			name:  "response with flags",
			input: "VA f30 c456\r\n",
			expected: &MetaResponse{
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
