package meta

import (
	"bufio"
	"bytes"
	"io"
	"strconv"
	"strings"
)

// MaxDataSize is the maximum value size accepted in a VA response (1 GiB).
// Memcached's maximum configurable item size is 1 GiB; a size beyond this
// indicates a corrupted or malicious response and is rejected before
// allocating memory for it.
const MaxDataSize = 1 << 30

// ReadResponse reads and parses a single response from r into resp.
// Response format: <status> [<flags>*]\r\n[<data>\r\n]
//
// The caller provides the Response; it will be reset before parsing.
// This allows callers to reuse Response objects (e.g., via sync.Pool).
//
// Protocol errors (CLIENT_ERROR, SERVER_ERROR, ERROR) from the server are
// stored in resp.Error (not returned as Go error). The caller should check
// resp.HasError() and use ShouldCloseConnection() to determine connection handling.
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
func ReadResponse(r *bufio.Reader, resp *Response) error {
	// Reset response for reuse
	*resp = Response{}

	// Read response line
	line, err := r.ReadString('\n')
	if err != nil {
		return err
	}

	// Trim CRLF
	line = strings.TrimSuffix(line, CRLF)
	line = strings.TrimSuffix(line, "\n") // Handle LF-only (lenient)

	// Check for protocol errors first
	if msg, ok := strings.CutPrefix(line, ErrorClientPrefix+" "); ok {
		// CLIENT_ERROR - connection should be closed
		resp.Error = &ClientError{Message: msg}
		return nil
	}

	if msg, ok := strings.CutPrefix(line, ErrorServerPrefix+" "); ok {
		// SERVER_ERROR - server-side error
		resp.Error = &ServerError{Message: msg}
		return nil
	}

	if line == ErrorGeneric {
		// ERROR - generic error or unknown command
		resp.Error = &GenericError{Message: "ERROR"}
		return nil
	}

	// Parse the response line in place: <status> [<size>] [<flags>*].
	// Field-by-field scanning avoids a per-response strings.Fields allocation.
	pos := 0
	status, ok := nextField(line, &pos)
	if !ok {
		return &ParseError{Message: "empty response line"}
	}

	resp.Status = StatusType(status)

	switch resp.Status {
	case StatusHD, StatusVA, StatusEN, StatusNF, StatusNS, StatusEX, StatusMN, StatusME:
	default:
		// An unknown status means the stream is desynchronized (or the server
		// speaks a protocol we don't understand): fail so the connection gets closed.
		return &ParseError{Message: "unknown response status: " + status}
	}

	// MN response has no additional data
	if resp.Status == StatusMN {
		return nil
	}

	// ME response format: ME <key> <key>=<value>*\r\n
	// The tokens after the key are debug key=value pairs, not flags: store the
	// raw remainder in Data (the key is known by the caller) and skip flag parsing.
	if resp.Status == StatusME {
		nextField(line, &pos) // skip the key
		if rest := strings.TrimLeft(line[pos:], " "); rest != "" {
			resp.Data = []byte(rest)
		}
		return nil
	}

	// VA response has size as second field
	var dataSize int
	if resp.Status == StatusVA {
		sizeField, ok := nextField(line, &pos)
		if !ok {
			return &ParseError{Message: "VA response missing size"}
		}

		dataSize, err = strconv.Atoi(sizeField)
		if err != nil {
			return &ParseError{Message: "invalid size in VA response", Err: err}
		}
		if dataSize < 0 {
			return &ParseError{Message: "negative size in VA response"}
		}
		if dataSize > MaxDataSize {
			return &ParseError{Message: "size in VA response exceeds maximum: " + sizeField}
		}
	}

	// Parse flags. Size the buffer once from the remaining line so the repeated
	// AddTokenString appends don't grow it incrementally.
	if pos < len(line) {
		resp.Flags = make(Flags, 0, len(line)-pos)
	}
	for {
		flagField, ok := nextField(line, &pos)
		if !ok {
			break
		}

		flagType := FlagType(flagField[0])
		if len(flagField) > 1 {
			resp.Flags.AddTokenString(flagType, flagField[1:])
		} else {
			resp.Flags.Add(flagType)
		}
	}

	// Read data block for VA responses
	if resp.Status == StatusVA {
		// Read data + CRLF together in single read
		data := make([]byte, dataSize+2)
		_, err = io.ReadFull(r, data)
		if err != nil {
			return &ParseError{Message: "failed to read data block", Err: err}
		}

		// Verify CRLF suffix
		if !bytes.HasSuffix(data, []byte(CRLF)) {
			return &ParseError{Message: "invalid data block terminator"}
		}

		// Truncate CRLF
		resp.Data = data[:dataSize]
	}

	return nil
}

// nextField returns the next space-separated field in line starting at *pos and
// advances *pos past it. Leading spaces are skipped and empty fields are never
// returned; ok is false once only spaces (or nothing) remain.
func nextField(line string, pos *int) (field string, ok bool) {
	i := *pos
	for i < len(line) && line[i] == ' ' {
		i++
	}
	start := i
	for i < len(line) && line[i] != ' ' {
		i++
	}
	*pos = i
	return line[start:i], start < i
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
