package meta

import (
	"bufio"
	"bytes"
	"io"
	"strconv"
	"strings"
)

// ReadResponse reads and parses a single response from r.
// Response format: <status> [<flags>*]\r\n[<data>\r\n]
//
// Returns Response with parsed data or error.
//
// Protocol errors (CLIENT_ERROR, SERVER_ERROR, ERROR) from the server are
// returned as Response.Error (not as Go error). The caller should check
// Response.HasError() and use ShouldCloseConnection() to determine connection handling.
//
// Go errors returned indicate I/O or parsing failures:
//   - io.EOF: Connection closed
//   - ParseError: Malformed response, connection should be closed
//   - Other I/O errors: Connection issues, connection should be closed
//
// Performance considerations:
//   - Uses bufio.Reader for efficient line reading
//   - Minimizes allocations for flag parsing
//   - Reads data block in single read operation when possible
func ReadResponse(r *bufio.Reader) (*Response, error) {
	// Read response line
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}

	// Trim CRLF
	line = strings.TrimSuffix(line, CRLF)
	line = strings.TrimSuffix(line, "\n") // Handle LF-only (lenient)

	// Check for protocol errors first
	if msg, ok := strings.CutPrefix(line, ErrorClientPrefix+" "); ok {
		// CLIENT_ERROR - connection should be closed
		return &Response{
			Status: "",
			Error:  &ClientError{Message: msg},
		}, nil
	}

	if msg, ok := strings.CutPrefix(line, ErrorServerPrefix+" "); ok {
		// SERVER_ERROR - server-side error
		return &Response{
			Status: "",
			Error:  &ServerError{Message: msg},
		}, nil
	}

	if line == ErrorGeneric {
		// ERROR - generic error or unknown command
		return &Response{
			Status: "",
			Error:  &GenericError{Message: "ERROR"},
		}, nil
	}

	// Parse response line: <status> [<size>] [<flags>*]
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return nil, &ParseError{Message: "empty response line"}
	}

	resp := &Response{
		Status: StatusType(parts[0]),
	}

	// MN response has no additional data
	if resp.Status == StatusMN {
		return resp, nil
	}

	// Parse remaining parts based on status
	idx := 1

	// VA response has size as second field
	var dataSize int
	if resp.Status == StatusVA {
		if idx >= len(parts) {
			return nil, &ParseError{Message: "VA response missing size"}
		}

		dataSize, err = strconv.Atoi(parts[idx])
		if err != nil {
			return nil, &ParseError{Message: "invalid size in VA response", Err: err}
		}
		if dataSize < 0 {
			return nil, &ParseError{Message: "negative size in VA response"}
		}
		if dataSize > MaxValueSize {
			return nil, &ParseError{Message: "value size exceeds maximum allowed"}
		}
		idx++
	}

	// Parse flags (remaining fields)
	for idx < len(parts) {
		flagStr := parts[idx]
		if len(flagStr) == 0 {
			idx++
			continue
		}

		// First character is flag type
		flag := Flag{
			Type: FlagType(flagStr[0]),
		}

		// Remaining characters are token
		if len(flagStr) > 1 {
			flag.Token = flagStr[1:]
		}

		resp.Flags = append(resp.Flags, flag)
		idx++
	}

	// Read data block for VA responses
	if resp.Status == StatusVA {
		// Read data + CRLF together in single read
		data := make([]byte, dataSize+2)
		_, err = io.ReadFull(r, data)
		if err != nil {
			return nil, &ParseError{Message: "failed to read data block", Err: err}
		}

		// Verify CRLF suffix
		if !bytes.HasSuffix(data, []byte(CRLF)) {
			return nil, &ParseError{Message: "invalid data block terminator"}
		}

		// Truncate CRLF
		resp.Data = data[:dataSize]
	}

	// Handle ME (debug) response
	if resp.Status == StatusME {
		// ME response format: ME <key> <key>=<value>*\r\n
		// Store key=value pairs in Data (skip first part which is the key)
		if len(parts) > 2 {
			// Join all parts after the key (parts[0] is "ME", parts[1] is key)
			resp.Data = []byte(strings.Join(parts[2:], " "))
		}
	}

	return resp, nil
}

// ReadStatsResponse reads a stats response from the server.
// Stats responses consist of multiple "STAT <name> <value>\r\n" lines
// followed by "END\r\n".
//
// Returns a map of stat names to values and any error encountered.
//
// Example response:
//
//	STAT pid 12345
//	STAT uptime 3600
//	STAT time 1609459200
//	END
func ReadStatsResponse(r *bufio.Reader) (map[string]string, error) {
	stats := make(map[string]string)

	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return stats, err
		}

		// Trim CRLF
		line = strings.TrimSuffix(line, CRLF)
		line = strings.TrimSuffix(line, "\n")

		// Check for END marker
		if line == EndMarker {
			return stats, nil
		}

		// Check for errors
		if msg, ok := strings.CutPrefix(line, ErrorClientPrefix+" "); ok {
			return stats, &ClientError{Message: msg}
		}
		if msg, ok := strings.CutPrefix(line, ErrorServerPrefix+" "); ok {
			return stats, &ServerError{Message: msg}
		}
		if line == ErrorGeneric {
			return stats, &GenericError{Message: "ERROR"}
		}

		// Parse STAT line: STAT <name> <value>
		if !strings.HasPrefix(line, StatPrefix+" ") {
			return stats, &ParseError{Message: "invalid stats response line: " + line}
		}

		// Remove "STAT " prefix
		statLine := strings.TrimPrefix(line, StatPrefix+" ")

		// Split into name and value (value may contain spaces)
		parts := strings.SplitN(statLine, " ", 2)
		if len(parts) != 2 {
			return stats, &ParseError{Message: "invalid STAT line format: " + line}
		}

		stats[parts[0]] = parts[1]
	}
}
