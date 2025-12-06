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
		// Allocate buffer for data
		data := make([]byte, dataSize)

		// Read data block
		_, err = io.ReadFull(r, data)
		if err != nil {
			return nil, &ParseError{Message: "failed to read data block", Err: err}
		}

		resp.Data = data

		// Read trailing CRLF
		crlf := make([]byte, 2)
		_, err = io.ReadFull(r, crlf)
		if err != nil {
			return nil, &ParseError{Message: "failed to read data block CRLF", Err: err}
		}

		// Verify CRLF (optional, for strict parsing)
		if !bytes.Equal(crlf, []byte(CRLF)) {
			// Try reading just LF if CR is missing (lenient)
			if crlf[0] != '\n' {
				return nil, &ParseError{Message: "invalid data block terminator"}
			}
			// Push back the second byte if it wasn't LF
			if crlf[1] != '\n' {
				if err := r.UnreadByte(); err != nil {
					return nil, &ParseError{Message: "failed to unread byte", Err: err}
				}
			}
		}
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

// ReadResponseBatch reads multiple responses in sequence.
// Useful for reading pipelined response batches.
//
// Stops reading when:
//  1. n responses have been read (if n > 0)
//  2. MN (no-op) response is encountered (if stopOnNoOp is true)
//  3. Error is encountered
//  4. EOF is reached
//
// Example for pipelined requests with quiet mode:
//
//	// Sent: mg key1 v q\r\n mg key2 v q\r\n mg key3 v\r\n mn\r\n
//	resps, err := ReadResponseBatch(r, 0, true)
//	// Reads responses until MN is encountered
//
// Returns slice of responses and first error encountered (if any).
// Responses read before error are still returned.
func ReadResponseBatch(r *bufio.Reader, n int, stopOnNoOp bool) ([]*Response, error) {
	var responses []*Response
	var count int

	for n == 0 || count < n {
		resp, err := ReadResponse(r)
		if err != nil {
			// Return responses collected so far
			return responses, err
		}

		responses = append(responses, resp)
		count++

		// Stop on MN if requested
		if stopOnNoOp && resp.Status == StatusMN {
			break
		}

		// Stop on error response
		if resp.HasError() {
			break
		}
	}

	return responses, nil
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
