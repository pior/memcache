package meta

import (
	"bufio"
	"bytes"
	"testing"
)

// FuzzReadResponse fuzzes the ReadResponse function to find crashes and panics.
// This tests the parser's robustness against malformed, malicious, or unexpected input.
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

// FuzzReadResponseBatch fuzzes the ReadResponseBatch function.
func FuzzReadResponseBatch(f *testing.F) {
	// Seed with valid batch responses
	f.Add([]byte("HD\r\nVA 5\r\nhello\r\nMN\r\n"), 0, true)
	f.Add([]byte("EN\r\nEN\r\nHD\r\n"), 3, false)
	f.Add([]byte("VA 3\r\nabc\r\nVA 2\r\nxy\r\nMN\r\n"), 0, true)
	f.Add([]byte("HD\r\nERROR\r\n"), 0, false)
	f.Add([]byte(""), 0, false)
	f.Add([]byte("MN\r\n"), 0, true)
	f.Add([]byte("HD\r\nHD\r\nHD\r\nHD\r\nHD\r\n"), 5, false)

	f.Fuzz(func(t *testing.T, data []byte, n int, stopOnNoOp bool) {
		// Limit n to reasonable range to avoid infinite loops
		if n < 0 {
			n = 0
		}
		if n > 1000 {
			n = 1000
		}

		r := bufio.NewReader(bytes.NewReader(data))

		// Call ReadResponseBatch - testing for no crash/panic
		responses, err := ReadResponseBatch(r, n, stopOnNoOp)

		// Validate basic invariants
		if responses != nil {
			// If n > 0, we should not have more than n responses (unless error)
			if n > 0 && len(responses) > n && err == nil {
				t.Errorf("Got more responses (%d) than requested (%d) without error", len(responses), n)
			}

			// If stopOnNoOp is true and we got MN, it should be the last response
			if stopOnNoOp && len(responses) > 0 {
				for i, resp := range responses {
					if resp.Status == StatusMN && i < len(responses)-1 {
						t.Errorf("Got MN response at position %d but not last (total: %d)", i, len(responses))
					}
				}
			}

			// All responses should be non-nil
			for i, resp := range responses {
				if resp == nil {
					t.Errorf("Response at index %d is nil", i)
				}
			}
		}

		// Function should never panic
	})
}
