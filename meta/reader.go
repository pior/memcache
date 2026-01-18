package meta

import (
	"bufio"
	"bytes"
	"io"
	"strconv"
	"strings"
)

// Pre-allocated byte slices for comparisons (avoid allocation in hot path)
var (
	crlfBytes         = []byte(CRLF)
	errorGenericBytes = []byte(ErrorGeneric)
	clientErrorPrefix = []byte(ErrorClientPrefix + " ")
	serverErrorPrefix = []byte(ErrorServerPrefix + " ")
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
//   - Uses ReadSlice for zero-allocation line reading
//   - Parses fields incrementally to minimize allocations
//   - Reads data block in single read operation
func ReadResponse(r *bufio.Reader) (*Response, error) {
	// Read response line using ReadSlice (zero allocation, returns slice into buffer).
	// Falls back to ReadBytes if line exceeds buffer size (allocates).
	//
	// The returned slice points into bufio.Reader's internal buffer, so we must copy
	// any data that can escape this function.
	line, err := r.ReadSlice('\n')
	if err == bufio.ErrBufferFull {
		line, err = r.ReadBytes('\n')
	}
	if err != nil {
		return nil, err
	}

	// Trim CRLF (reslice, no allocation).
	line = trimTrailingNewline(line)

	if len(line) == 0 {
		return nil, &ParseError{Message: "empty response line"}
	}

	// Check for protocol errors first.
	// These comparisons are safe on the bufio slice since we don't keep references.
	if bytes.HasPrefix(line, clientErrorPrefix) {
		msg := string(line[len(clientErrorPrefix):])
		return &Response{Error: &ClientError{Message: msg}}, nil
	}
	if bytes.HasPrefix(line, serverErrorPrefix) {
		msg := string(line[len(serverErrorPrefix):])
		return &Response{Error: &ServerError{Message: msg}}, nil
	}
	if bytes.Equal(line, errorGenericBytes) {
		return &Response{Error: &GenericError{Message: "ERROR"}}, nil
	}

	// Copy into a right-sized owned buffer.
	// This prevents exposing bufio.Reader's internal buffer and keeps retention bounded.
	owned := make([]byte, len(line))
	copy(owned, line)

	// Parse status (first field, typically 2 bytes: HD, VA, EN, etc.).
	statusField, pos, ok := nextField(owned, 0)
	if !ok {
		return nil, &ParseError{Message: "empty response line"}
	}
	if len(statusField) != 2 {
		return nil, &ParseError{Message: "invalid status"}
	}

	resp := &Response{Status: StatusType(string(statusField))}
	// StatusMN is extremely common (pipeline terminator) so check it first.
	if resp.Status == StatusMN {
		return resp, nil
	}

	var dataSize int
	if resp.Status == StatusVA {
		sizeField, nextPos, ok := nextField(owned, pos)
		if !ok {
			return nil, &ParseError{Message: "VA response missing size"}
		}
		dataSize, err = strconv.Atoi(string(sizeField))
		if err != nil {
			return nil, &ParseError{Message: "invalid size in VA response", Err: err}
		}
		if dataSize < 0 {
			return nil, &ParseError{Message: "negative size in VA response"}
		}
		pos = nextPos
	}

	for {
		field, nextPos, ok := nextField(owned, pos)
		if !ok {
			break
		}
		pos = nextPos
		if len(field) == 0 {
			continue
		}

		flag := ResponseFlag{Type: FlagType(field[0])}
		if len(field) > 1 {
			flag.Token = field[1:]
		}
		resp.Flags = append(resp.Flags, flag)
	}

	if resp.Status == StatusVA {
		data := make([]byte, dataSize+2)
		_, err = io.ReadFull(r, data)
		if err != nil {
			return nil, &ParseError{Message: "failed to read data block", Err: err}
		}
		if !bytes.HasSuffix(data, crlfBytes) {
			return nil, &ParseError{Message: "invalid data block terminator"}
		}
		resp.Data = data[:dataSize]
	}

	// StatusME is for debugging and is much less frequent than StatusMN/StatusVA.
	// Evaluate it last to keep the hot path (MN then VA) tight.
	if resp.Status == StatusME {
		parts := strings.Fields(string(owned))
		if len(parts) > 2 {
			resp.Data = []byte(strings.Join(parts[2:], " "))
		}
	}

	return resp, nil
}

func trimTrailingNewline(b []byte) []byte {
	if len(b) == 0 {
		return b
	}
	if b[len(b)-1] == '\n' {
		b = b[:len(b)-1]
	}
	if len(b) > 0 && b[len(b)-1] == '\r' {
		b = b[:len(b)-1]
	}
	return b
}

func nextField(b []byte, idx int) (field []byte, nextIdx int, ok bool) {
	idx = skipSpaces(b, idx)
	if idx >= len(b) {
		return nil, idx, false
	}

	end := idx
	for end < len(b) && !isSpace(b[end]) {
		end++
	}
	return b[idx:end], end, true
}

func skipSpaces(b []byte, idx int) int {
	for idx < len(b) && isSpace(b[idx]) {
		idx++
	}
	return idx
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t'
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
