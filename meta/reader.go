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
	// Read response line using ReadSlice (zero allocation, returns slice into buffer)
	// Falls back to ReadBytes if line exceeds buffer size
	line, err := r.ReadSlice('\n')
	if err == bufio.ErrBufferFull {
		// Line exceeds buffer, fall back to ReadBytes (allocates)
		line, err = r.ReadBytes('\n')
	}
	if err != nil {
		return nil, err
	}

	// Trim CRLF (reslice, no allocation)
	line = bytes.TrimSuffix(line, crlfBytes)

	// Check for protocol errors first
	if bytes.HasPrefix(line, clientErrorPrefix) {
		msg := string(line[len(clientErrorPrefix):])
		return &Response{
			Status: "",
			Error:  &ClientError{Message: msg},
		}, nil
	}

	if bytes.HasPrefix(line, serverErrorPrefix) {
		msg := string(line[len(serverErrorPrefix):])
		return &Response{
			Status: "",
			Error:  &ServerError{Message: msg},
		}, nil
	}

	if bytes.Equal(line, errorGenericBytes) {
		return &Response{
			Status: "",
			Error:  &GenericError{Message: "ERROR"},
		}, nil
	}

	// Parse status (first field, typically 2 bytes: HD, VA, EN, etc.)
	if len(line) < 2 {
		return nil, &ParseError{Message: "empty response line"}
	}

	// Find end of status
	statusEnd := bytes.IndexByte(line, ' ')
	if statusEnd == -1 {
		statusEnd = len(line)
	}

	resp := &Response{
		Status: StatusType(line[:statusEnd]),
	}

	// MN response has no additional data
	if resp.Status == StatusMN {
		return resp, nil
	}

	// Position after status
	pos := statusEnd

	// VA response has size as second field
	var dataSize int
	if resp.Status == StatusVA {
		// Skip space
		for pos < len(line) && line[pos] == ' ' {
			pos++
		}

		// Find end of size field
		sizeEnd := bytes.IndexByte(line[pos:], ' ')
		var sizeBytes []byte
		if sizeEnd == -1 {
			sizeBytes = line[pos:]
			pos = len(line)
		} else {
			sizeBytes = line[pos : pos+sizeEnd]
			pos = pos + sizeEnd
		}

		if len(sizeBytes) == 0 {
			return nil, &ParseError{Message: "VA response missing size"}
		}

		dataSize, err = strconv.Atoi(string(sizeBytes))
		if err != nil {
			return nil, &ParseError{Message: "invalid size in VA response", Err: err}
		}
		if dataSize < 0 {
			return nil, &ParseError{Message: "negative size in VA response"}
		}
	}

	// Parse flags (remaining fields)
	for pos < len(line) {
		// Skip spaces
		for pos < len(line) && line[pos] == ' ' {
			pos++
		}
		if pos >= len(line) {
			break
		}

		// Find end of this flag
		flagEnd := bytes.IndexByte(line[pos:], ' ')
		var flagBytes []byte
		if flagEnd == -1 {
			flagBytes = line[pos:]
			pos = len(line)
		} else {
			flagBytes = line[pos : pos+flagEnd]
			pos = pos + flagEnd
		}

		if len(flagBytes) == 0 {
			continue
		}

		// First byte is flag type
		flag := Flag{
			Type: FlagType(flagBytes[0]),
		}

		// Remaining bytes are token (only allocate string if token exists)
		if len(flagBytes) > 1 {
			flag.Token = string(flagBytes[1:])
		}

		resp.Flags = append(resp.Flags, flag)
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
		if !bytes.HasSuffix(data, crlfBytes) {
			return nil, &ParseError{Message: "invalid data block terminator"}
		}

		// Truncate CRLF
		resp.Data = data[:dataSize]
	}

	// Handle ME (debug) response
	// ME is for debugging, so we prioritize readability over performance here
	if resp.Status == StatusME {
		// ME response format: ME <key> <key>=<value>*\r\n
		// Store key=value pairs in Data (skip status and key)
		parts := strings.Fields(string(line))
		if len(parts) > 2 {
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
