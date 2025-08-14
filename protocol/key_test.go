package protocol

import (
	"strings"
	"testing"
)

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
			result := IsValidKey(tt.key)
			if result != tt.valid {
				t.Errorf("IsValidKey(%q) = %v, want %v", tt.key, result, tt.valid)
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
			result := IsValidKey(tt.key)
			if result != tt.valid {
				t.Errorf("expected %v for key %q, got %v", tt.valid, tt.key, result)
			}
		})
	}
}

func BenchmarkIsValidKey(b *testing.B) {
	tests := []struct {
		name string
		key  string
	}{
		{
			name: "Short",
			key:  "foo",
		},
		{
			name: "Medium",
			key:  "medium_length_key_with_underscores_and_numbers_123",
		},
		{
			name: "Long",
			key:  strings.Repeat("a", 200),
		},
		{
			name: "MaxLength",
			key:  strings.Repeat("a", 250),
		},
		{
			name: "Invalid",
			key:  "key with spaces",
		},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			for b.Loop() {
				IsValidKey(tt.key)
			}
		})
	}
}

func FuzzIsValidKey(f *testing.F) {
	// Seed corpus
	f.Add("foo")
	f.Add("bar_baz")
	f.Add("test-key-123")
	f.Add("")
	f.Add(strings.Repeat("a", 250))
	f.Add(strings.Repeat("a", 251))
	f.Add("key with space")
	f.Add("key\twith\ttab")
	f.Add("key\nwith\nnewline")
	f.Add("key\x00with\x00null")

	f.Fuzz(func(t *testing.T, key string) {
		// Function should not panic
		result := IsValidKey(key)

		// Validate some basic invariants
		if len(key) == 0 && result {
			t.Errorf("Empty key should not be valid")
		}
		if len(key) > 250 && result {
			t.Errorf("Key longer than 250 chars should not be valid")
		}

		// Check for control characters
		for _, b := range []byte(key) {
			if (b <= 32 || b == 127) && result {
				t.Errorf("Key with control character should not be valid: %q", key)
				break
			}
		}
	})
}
