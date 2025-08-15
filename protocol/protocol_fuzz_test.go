package protocol

import (
	"bufio"
	"strings"
	"testing"
)

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
