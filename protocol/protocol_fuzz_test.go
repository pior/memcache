package protocol

import (
	"bufio"
	"strings"
	"testing"
)

func FuzzFormatGetCommand(f *testing.F) {
	// Seed corpus with various inputs
	f.Add("foo", "v", "")
	f.Add("bar", "v,f,t", "123")
	f.Add("", "v", "")
	f.Add("test_key_123", "", "456")
	f.Add(strings.Repeat("a", 250), "v", "999")
	f.Add(strings.Repeat("a", 251), "v", "")

	f.Fuzz(func(t *testing.T, key, flagsStr, opaque string) {
		// Convert comma-separated flags string to slice
		var flags []string
		if flagsStr != "" {
			flags = strings.Split(flagsStr, ",")
		}

		// Function should not panic
		result := formatGetCommand(key, flags, opaque)

		// If result is not nil, it should be valid
		if result != nil {
			if len(result) < 3 {
				t.Errorf("Result too short: %q", string(result))
			}
			if !strings.HasPrefix(string(result), "mg ") {
				t.Errorf("Result should start with 'mg ': %q", string(result))
			}
			if !strings.HasSuffix(string(result), "\r\n") {
				t.Errorf("Result should end with \\r\\n: %q", string(result))
			}
		}
	})
}

func FuzzFormatSetCommand(f *testing.F) {
	// Seed corpus
	f.Add("foo", "hello", 0, "", "")
	f.Add("bar", "world", 300, "F=123", "456")
	f.Add("", "test", 0, "", "")
	f.Add(strings.Repeat("a", 250), "data", 600, "C=", "999")

	f.Fuzz(func(t *testing.T, key, value string, ttl int, flagsStr, opaque string) {
		// Parse flags string into map
		flags := make(map[string]string)
		if flagsStr != "" {
			for _, pair := range strings.Split(flagsStr, ",") {
				if strings.Contains(pair, "=") {
					parts := strings.SplitN(pair, "=", 2)
					flags[parts[0]] = parts[1]
				} else {
					flags[pair] = ""
				}
			}
		}

		// Function should not panic
		result := formatSetCommand(key, []byte(value), ttl, mapToFlags(flags), opaque)

		// If result is not nil, it should be valid
		if result != nil {
			resultStr := string(result)
			if !strings.HasPrefix(resultStr, "ms ") {
				t.Errorf("Result should start with 'ms ': %q", resultStr)
			}
			if !strings.HasSuffix(resultStr, "\r\n") {
				t.Errorf("Result should end with \\r\\n: %q", resultStr)
			}
			// Should contain the value and proper structure
			if !strings.Contains(resultStr, value) {
				t.Errorf("Result should contain the value: %q", resultStr)
			}
		}
	})
}

func FuzzFormatDeleteCommand(f *testing.F) {
	// Seed corpus
	f.Add("foo", "")
	f.Add("bar", "123")
	f.Add("", "")
	f.Add(strings.Repeat("a", 250), "999")
	f.Add("key with space", "")
	f.Add("key\nwith\nnewlines", "456")

	f.Fuzz(func(t *testing.T, key, opaque string) {
		// Function should not panic
		result := formatDeleteCommand(key, opaque)

		// If result is not nil, it should be valid
		if result != nil {
			resultStr := string(result)
			if !strings.HasPrefix(resultStr, "md ") {
				t.Errorf("Result should start with 'md ': %q", resultStr)
			}
			if !strings.HasSuffix(resultStr, "\r\n") {
				t.Errorf("Result should end with \\r\\n: %q", resultStr)
			}
		}
	})
}

func FuzzParseResponse(f *testing.F) {
	// Seed corpus with various response formats
	f.Add("HD\r\n")
	f.Add("VA 5 s5\r\nhello\r\n")
	f.Add("HD O123\r\n")
	f.Add("VA f30 c456\r\n")
	f.Add("EN\r\n")
	f.Add("NS\r\n")
	f.Add("EX\r\n")
	f.Add("NF\r\n")
	f.Add("VA 0 s0\r\n\r\n")

	f.Fuzz(func(t *testing.T, input string) {
		reader := bufio.NewReader(strings.NewReader(input))

		// Function should not panic
		resp, err := ReadResponse(reader)

		// If no error, response should be valid
		if err == nil && resp != nil {
			if resp.Status == "" {
				t.Errorf("Status should not be empty for valid response")
			}
			if resp.Flags == nil {
				t.Errorf("Flags should not be nil")
			}
		}
	})
}
