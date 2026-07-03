package meta

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strconv"
	"strings"
)

// MaxDataSize is the maximum value size accepted in a VA response (1 GiB).
// Memcached's maximum configurable item size is 1 GiB; a size beyond this
// indicates a corrupted or malicious response and is rejected before
// allocating memory for it.
const MaxDataSize = 1 << 30

// MaxLineSize is the buffer size this library uses for connection readers,
// and therefore the maximum length of a single response line (CRLF included)
// it accepts. Unlike the VA data block, a response line carries no declared
// length, so the reader's buffer size is what bounds it: ReadResponse and
// ReadStatsResponse reject a line that does not fit within the reader's buffer
// with a ParseError wrapping bufio.ErrBufferFull. Real meta status/stat lines
// are tiny, so this is generous.
const MaxLineSize = 4096

// readLine reads a single response line up to and including the '\n' delimiter,
// copies it out of the bufio buffer, and trims the trailing CRLF (a bare LF is
// tolerated for leniency).
//
// Unlike bufio.Reader.ReadString, the line length is bounded by the reader's
// buffer size (MaxLineSize for readers created by this library): a server that
// streams bytes without ever sending '\n' triggers bufio.ErrBufferFull, which
// is reported as a ParseError instead of an unbounded allocation. This protects
// the client from memory amplification by a hostile or buggy server, since the
// response line — unlike the VA data block — carries no declared length to
// bound it.
//
// The returned string is a fresh copy, so callers may retain substrings of it
// (status, flags) after subsequent reads on the same bufio.Reader.
func readLine(r *bufio.Reader) (string, error) {
	slice, err := r.ReadSlice('\n')
	if err != nil {
		if errors.Is(err, bufio.ErrBufferFull) {
			return "", &ParseError{Message: "response line exceeds maximum length", Err: err}
		}
		return "", err
	}

	line := string(slice)
	line = strings.TrimSuffix(line, CRLF)
	line = strings.TrimSuffix(line, "\n") // Handle LF-only (lenient)
	return line, nil
}

// maxRetainedBufferSize bounds the capacity ReadResponse carries from one
// response to the next. It does not limit accepted response sizes: larger data
// and flags are read normally, then their buffers are released when the
// Response is reused.
const maxRetainedBufferSize = 1 << 20

// ReadResponse reads and parses a single response from r into resp.
// Response format: <status> [<flags>*]\r\n[<data>\r\n]
//
// The caller provides resp and owns its storage. Before parsing, ReadResponse
// clears the previous logical response while retaining Data and Flags backing
// arrays whose capacity is at most 1 MiB. Parsing reuses those arrays when they
// are large enough and grows them when necessary. Larger responses are accepted
// normally, but their backing arrays are released the next time resp is reused.
// Callers can release retained storage earlier by setting Data or Flags to nil.
//
// On success, resp's fields remain valid until the same Response is passed to
// ReadResponse again or its slices are modified by the caller. Reading into a
// different Response with independent storage does not invalidate them. Copying
// a Response only copies its slice headers, so clone Data and Flags when an
// independent copy is needed.
// If ReadResponse returns an error, resp may contain a partial response and its
// contents must not be used.
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
// The response line must fit within r's buffer: a longer line is rejected as a
// ParseError wrapping bufio.ErrBufferFull, so r should be sized to at least
// MaxLineSize (see MaxLineSize for why the line read is bounded).
//
// Performance considerations:
//   - Uses bufio.Reader for efficient line reading
//   - Reuses caller-owned data and flag buffers when capacity permits
//   - Minimizes allocations for flag parsing
//   - Reads data block in single read operation when possible
func ReadResponse(r *bufio.Reader, resp *Response) error {
	// Clear the previous logical response while retaining reasonably sized
	// caller-owned buffers. Keeping zero-length slices on responses that do not
	// use them allows a single Response to carry its capacity across a mixed
	// stream of hits, misses, and status responses.
	data := resp.Data[:0]
	if cap(data) > maxRetainedBufferSize {
		data = nil
	}
	flags := resp.Flags[:0]
	if cap(flags) > maxRetainedBufferSize {
		flags = nil
	}
	*resp = Response{Data: data, Flags: flags}

	// Read response line
	line, err := readLine(r)
	if err != nil {
		return err
	}

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
	sc := lineScanner{line: line}
	status, ok := sc.next()
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
		sc.next() // skip the key
		if rest := sc.rest(); rest != "" {
			resp.Data = append(resp.Data, rest...)
		}
		return nil
	}

	// VA response has size as second field
	var dataSize int
	if resp.Status == StatusVA {
		sizeField, ok := sc.next()
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
	if n := sc.remaining(); n > 0 {
		if cap(resp.Flags) < n {
			resp.Flags = make(Flags, 0, n)
		}
	}
	for {
		flagField, ok := sc.next()
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
		dataLen := dataSize + len(CRLF)
		if cap(resp.Data) < dataLen {
			resp.Data = make([]byte, dataLen)
		} else {
			resp.Data = resp.Data[:dataLen]
		}
		_, err = io.ReadFull(r, resp.Data)
		if err != nil {
			return &ParseError{Message: "failed to read data block", Err: err}
		}

		// Verify CRLF suffix
		if !bytes.HasSuffix(resp.Data, []byte(CRLF)) {
			return &ParseError{Message: "invalid data block terminator"}
		}

		// Truncate CRLF
		resp.Data = resp.Data[:dataSize]
	}

	return nil
}

// lineScanner walks a response line field by field, in place. It avoids the
// per-response []string that strings.Fields would allocate.
type lineScanner struct {
	line string
	pos  int
}

// next returns the next space-separated field and advances past it. Leading
// spaces are skipped and empty fields are never returned; ok is false once only
// spaces (or nothing) remain.
func (s *lineScanner) next() (field string, ok bool) {
	i := s.pos
	for i < len(s.line) && s.line[i] == ' ' {
		i++
	}
	start := i
	for i < len(s.line) && s.line[i] != ' ' {
		i++
	}
	s.pos = i
	return s.line[start:i], start < i
}

// rest returns the unscanned remainder of the line with leading spaces trimmed.
func (s *lineScanner) rest() string {
	return strings.TrimLeft(s.line[s.pos:], " ")
}

// remaining reports the number of unscanned bytes, used to size buffers before
// consuming the rest of the line.
func (s *lineScanner) remaining() int {
	return len(s.line) - s.pos
}

// ReadStatsResponse reads a stats response from the server.
// Stats responses consist of multiple "STAT <name> <value>\r\n" lines
// followed by "END\r\n".
//
// Returns a map of stat names to values and any error encountered.
//
// Each line must fit within r's buffer: a longer line is rejected as a
// ParseError wrapping bufio.ErrBufferFull, so r should be sized to at least
// MaxLineSize (see MaxLineSize for why line reads are bounded).
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
		line, err := readLine(r)
		if err != nil {
			return stats, err
		}

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
