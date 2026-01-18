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
//   - Avoids per-response strings.Fields allocations
//   - Reads VA data block in a single read operation
func ReadResponse(r *bufio.Reader) (*Response, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = trimTrailingNewline(line)

	// Check for protocol errors first.
	if msg, ok := strings.CutPrefix(line, ErrorClientPrefix+" "); ok {
		return &Response{Error: &ClientError{Message: msg}}, nil
	}
	if msg, ok := strings.CutPrefix(line, ErrorServerPrefix+" "); ok {
		return &Response{Error: &ServerError{Message: msg}}, nil
	}
	if line == ErrorGeneric {
		return &Response{Error: &GenericError{Message: "ERROR"}}, nil
	}

	statusField, idx, ok := nextField(line, 0)
	if !ok {
		return nil, &ParseError{Message: "empty response line"}
	}

	resp := &Response{Status: StatusType(statusField)}
	if resp.Status == StatusMN {
		return resp, nil
	}

	var dataSize int
	if resp.Status == StatusVA {
		sizeField, nextIdx, ok := nextField(line, idx)
		if !ok {
			return nil, &ParseError{Message: "VA response missing size"}
		}

		dataSize, err = strconv.Atoi(sizeField)
		if err != nil {
			return nil, &ParseError{Message: "invalid size in VA response", Err: err}
		}
		if dataSize < 0 {
			return nil, &ParseError{Message: "negative size in VA response"}
		}
		idx = nextIdx
	}

	for {
		flagField, nextIdx, ok := nextField(line, idx)
		if !ok {
			break
		}
		idx = nextIdx

		if flagField == "" {
			continue
		}

		flag := Flag{Type: FlagType(flagField[0])}
		if len(flagField) > 1 {
			flag.Token = flagField[1:]
		}
		resp.Flags = append(resp.Flags, flag)
	}

	if resp.Status == StatusVA {
		data := make([]byte, dataSize+2)
		_, err = io.ReadFull(r, data)
		if err != nil {
			return nil, &ParseError{Message: "failed to read data block", Err: err}
		}
		if !bytes.HasSuffix(data, []byte(CRLF)) {
			return nil, &ParseError{Message: "invalid data block terminator"}
		}
		resp.Data = data[:dataSize]
	}

	if resp.Status == StatusME {
		// ME response format: ME <key> <key>=<value>*\r\n
		// Store the raw key=value pairs in Data (skip the first two fields).
		_, idx, ok = nextField(line, 0) // "ME"
		if ok {
			_, idx, ok = nextField(line, idx) // key
			if ok {
				rest := strings.TrimSpace(line[idx:])
				if rest != "" {
					resp.Data = []byte(rest)
				}
			}
		}
	}

	return resp, nil
}

func trimTrailingNewline(s string) string {
	if len(s) == 0 {
		return s
	}
	if s[len(s)-1] == '\n' {
		s = s[:len(s)-1]
	}
	if len(s) > 0 && s[len(s)-1] == '\r' {
		s = s[:len(s)-1]
	}
	return s
}

func nextField(s string, idx int) (field string, nextIdx int, ok bool) {
	idx = skipSpaces(s, idx)
	if idx >= len(s) {
		return "", idx, false
	}

	end := idx
	for end < len(s) && !isSpace(s[end]) {
		end++
	}
	return s[idx:end], end, true
}

func skipSpaces(s string, idx int) int {
	for idx < len(s) && isSpace(s[idx]) {
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

		line = trimTrailingNewline(line)

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
