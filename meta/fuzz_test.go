package meta

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

// FuzzReadResponse fuzzes the ReadResponse function to find crashes and panics.
// This tests the parser's robustness against malformed, malicious, or unexpected input.
// Run with: go test -fuzz='^FuzzReadResponse$' -fuzztime=60s ./meta
func FuzzReadResponse(f *testing.F) {
	// Seed corpus with valid responses covering all status types
	f.Add([]byte("HD\r\n"))                         // Hit/deleted
	f.Add([]byte("VA 5\r\nhello\r\n"))              // Value response
	f.Add([]byte("VA 0\r\n\r\n"))                   // Empty value
	f.Add([]byte("EN\r\n"))                         // Not found
	f.Add([]byte("NF\r\n"))                         // Not found (delete)
	f.Add([]byte("NS\r\n"))                         // Not stored
	f.Add([]byte("EX\r\n"))                         // Already exists
	f.Add([]byte("MN\r\n"))                         // No-op response
	f.Add([]byte("CLIENT_ERROR invalid key\r\n"))   // Client error
	f.Add([]byte("SERVER_ERROR out of memory\r\n")) // Server error
	f.Add([]byte("ERROR\r\n"))                      // Generic error
	f.Add([]byte("VA 10 v\r\n0123456789\r\n"))      // Value with flags
	f.Add([]byte("HD c123 t456\r\n"))               // Hit with CAS and TTL
	f.Add([]byte("VA 3 W Z\r\nabc\r\n"))            // Value with win and stale flags
	f.Add([]byte("ME key foo=bar baz=qux\r\n"))     // Debug response
	f.Add([]byte("HD O12345\r\n"))                  // Hit with opaque

	// Seed corpus with edge cases
	f.Add([]byte("VA 5\r\nhello\n"))   // LF only (lenient)
	f.Add([]byte("HD \r\n"))           // Extra space
	f.Add([]byte("VA 1 \r\nx\r\n"))    // Space before size
	f.Add([]byte("\r\n"))              // Empty line
	f.Add([]byte(""))                  // Empty input
	f.Add([]byte("UNKNOWN\r\n"))       // Unknown status
	f.Add([]byte("VA\r\n"))            // Missing size
	f.Add([]byte("VA abc\r\n"))        // Invalid size
	f.Add([]byte("VA -1\r\n"))         // Negative size
	f.Add([]byte("VA 2097152\r\n"))    // Size exceeds maximum (2MB > 1MB limit)
	f.Add([]byte("VA 5\r\nabc"))       // Truncated data
	f.Add([]byte("VA 5\r\nhello"))     // Missing CRLF
	f.Add([]byte("VA 5\r\nhelloXX"))   // Wrong terminator
	f.Add([]byte("CLIENT_ERROR \r\n")) // Empty error message
	f.Add([]byte("SERVER_ERROR\r\n"))  // No space in error

	// Seed corpus with protocol boundary cases
	f.Add([]byte("HD v v v v v\r\n"))               // Many flags
	f.Add([]byte("VA 5 c1 c2 c3\r\nhello\r\n"))     // Multiple CAS flags
	f.Add([]byte("HD flag1 flag2 flag3 flag4\r\n")) // Multiple flags

	f.Fuzz(func(t *testing.T, data []byte) {
		// Create a bufio.Reader from the fuzz input
		r := bufio.NewReader(bytes.NewReader(data))

		// Call ReadResponse - we're testing that it doesn't crash/panic
		resp, err := ReadResponse(r)

		// If we got a response, validate basic invariants
		if err == nil && resp != nil {
			// Status should not be empty unless it's an error response
			if resp.Status == "" && !resp.HasError() {
				t.Errorf("Got empty status without error")
			}

			// If Status is VA, we should have data
			if resp.Status == StatusVA {
				// Data size should match what was claimed
				// (already validated by ReadResponse, but check invariant)
				if resp.Data == nil {
					t.Errorf("VA response has nil data")
				}
			}

			// Error responses should have Error field set
			if resp.HasError() && resp.Error == nil {
				t.Errorf("HasError() is true but Error is nil")
			}

			// Non-error responses should not have Error field set
			if !resp.HasError() && resp.Error != nil {
				t.Errorf("HasError() is false but Error is not nil")
			}
		}

		// If we got a parse error, it should contain a message
		if err != nil {
			if parseErr, ok := err.(*ParseError); ok {
				if parseErr.Message == "" {
					t.Errorf("ParseError has empty message")
				}
			}
		}

		// The function should never panic - this is the main test
		// If we reach here without panicking, the test passes
	})
}

// FuzzWriteRequest fuzzes the WriteRequest function to find crashes and panics.
// This tests the serialization robustness against malformed or unexpected requests.
// Run with: go test -fuzz='^FuzzWriteRequest$' -fuzztime=60s ./meta
func FuzzWriteRequest(f *testing.F) {
	// Seed corpus with valid requests covering all command types
	f.Add([]byte("mg"), []byte("test-key"), []byte{})             // Get request
	f.Add([]byte("ms"), []byte("test-key"), []byte("test-value")) // Set request
	f.Add([]byte("md"), []byte("test-key"), []byte{})             // Delete request
	f.Add([]byte("ma"), []byte("test-key"), []byte{})             // Arithmetic request
	f.Add([]byte("me"), []byte("test-key"), []byte{})             // Debug request
	f.Add([]byte("mn"), []byte(""), []byte{})                     // No-op request

	// Seed corpus with requests containing flags
	f.Add([]byte("mg v"), []byte("test-key"), []byte{})                 // Get with return value
	f.Add([]byte("ms T60"), []byte("test-key"), []byte("value"))        // Set with TTL
	f.Add([]byte("ms T3600 c123"), []byte("test-key"), []byte("value")) // Set with TTL and CAS
	f.Add([]byte("ma N5"), []byte("test-key"), []byte{})                // Arithmetic with delta
	f.Add([]byte("me foo=bar"), []byte("test-key"), []byte{})           // Debug with params

	// Seed corpus with edge cases
	f.Add([]byte(""), []byte(""), []byte{})                                                                            // Empty command
	f.Add([]byte("xx"), []byte(""), []byte{})                                                                          // Invalid command
	f.Add([]byte("mg"), []byte(""), []byte{})                                                                          // Empty key
	f.Add([]byte("ms"), []byte("key"), []byte{})                                                                       // Empty value for set
	f.Add([]byte("mg"), []byte("key with spaces"), []byte{})                                                           // Key with spaces
	f.Add([]byte("mg"), []byte("very-long-key-that-exceeds-normal-limits-and-should-be-handled-gracefully"), []byte{}) // Long key

	f.Fuzz(func(t *testing.T, cmdBytes, keyBytes, dataBytes []byte) {
		// Convert bytes to strings, but limit size to prevent excessive memory usage
		cmd := string(cmdBytes)
		key := string(keyBytes)
		data := dataBytes

		// Limit sizes to prevent excessive fuzzing time
		if len(cmd) > 100 {
			cmd = cmd[:100]
		}
		if len(key) > 500 {
			key = key[:500]
		}
		if len(data) > 10000 {
			data = data[:10000]
		}

		// Try to parse the command as a valid CmdType
		var cmdType CmdType
		if len(cmd) >= 2 {
			switch cmd[:2] {
			case "mg":
				cmdType = CmdGet
			case "ms":
				cmdType = CmdSet
			case "md":
				cmdType = CmdDelete
			case "ma":
				cmdType = CmdArithmetic
			case "me":
				cmdType = CmdDebug
			case "mn":
				cmdType = CmdNoOp
			default:
				// Invalid command, but we'll still try to create a request
				cmdType = CmdType(cmd[:2])
			}
		}

		// Create a request with the fuzzed data
		req := &Request{
			Command: cmdType,
			Key:     key,
			Data:    data,
		}

		// Try to parse flags from the remaining command string
		if len(cmd) > 2 {
			flagStr := cmd[2:]
			// Simple flag parsing - this is best effort for fuzzing
			// Split on spaces and try to parse each flag
			parts := strings.Fields(flagStr)
			for _, part := range parts {
				if len(part) > 0 {
					flagType := FlagType(part[0])
					token := ""
					if len(part) > 1 {
						token = part[1:]
					}
					req.Flags = append(req.Flags, Flag{
						Type:  flagType,
						Token: token,
					})
				}
			}
		}

		// Create a buffer to write to
		var buf bytes.Buffer

		// Call WriteRequest - we're testing that it doesn't crash/panic
		err := WriteRequest(&buf, req)

		// If we got no error, validate the output is reasonable
		if err == nil {
			output := buf.String()

			// Basic sanity checks
			if len(output) == 0 {
				t.Errorf("WriteRequest produced empty output")
			}

			// Should end with CRLF
			if !strings.HasSuffix(output, "\r\n") {
				t.Errorf("WriteRequest output should end with CRLF")
			}

			// Should contain the command
			if len(cmd) >= 2 && !strings.Contains(output, cmd[:2]) {
				t.Errorf("Output should contain command %s", cmd[:2])
			}
		}

		// The function should never panic - this is the main test
		// If we reach here without panicking, the test passes
	})
}
