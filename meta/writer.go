package meta

import (
	"bufio"
	"bytes"
	"io"
	"strconv"
	"strings"
	"sync"
)

// Buffer pool for building requests
var bufferPool = sync.Pool{
	New: func() any {
		// Typical request is ~100 bytes, allocate 256 bytes
		return bytes.NewBuffer(make([]byte, 0, 256))
	},
}

func getBuffer() *bytes.Buffer {
	return bufferPool.Get().(*bytes.Buffer)
}

func putBuffer(buf *bytes.Buffer) {
	// TODO: drop if buffer is too large
	buf.Reset()
	bufferPool.Put(buf)
}

// ValidateKey checks if a key is valid for the memcache protocol.
// Keys must be 1-250 bytes and contain no whitespace (unless base64-encoded).
// Returns an error describing the validation failure.
func ValidateKey(key string, hasBase64Flag bool) error {
	keyLen := len(key)

	if keyLen < MinKeyLength {
		return &InvalidKeyError{Message: "key is empty"}
	}

	if keyLen > MaxKeyLength {
		return &InvalidKeyError{Message: "key exceeds maximum length of 250 bytes"}
	}

	// Whitespace is only allowed if key is base64-encoded
	if !hasBase64Flag && strings.ContainsAny(key, " \t\r\n") {
		return &InvalidKeyError{Message: "key contains whitespace"}
	}

	return nil
}

// WriteRequest serializes a Request to wire format and writes it to w.
// Format: <command> <key> [<size>] <flags>*\r\n[<data>\r\n]
//
// For ms command: ms <key> <size> <flags>*\r\n<data>\r\n
// For other commands: <cmd> <key> <flags>*\r\n
// For mn command: mn\r\n
//
// Returns the number of bytes written and any error encountered.
// Validates key format before writing to prevent protocol errors.
//
// Performance considerations:
//   - Uses bufio.Writer when available for buffered writes
//   - Falls back to pooled buffer for other io.Writer types
//   - Data block written directly through the writer
func WriteRequest(w io.Writer, req *Request) error {
	// Optimize for bufio.Writer (used by Connection)
	if bw, ok := w.(*bufio.Writer); ok {
		return writeRequestBuffered(bw, req)
	}

	// Fallback to bytes.Buffer approach for other writers (tests, etc.)
	return writeRequestUnbuffered(w, req)
}

// writeRequestBuffered writes using bufio.Writer for optimal performance.
func writeRequestBuffered(bw *bufio.Writer, req *Request) error {
	// mn command has no key or flags
	if req.Command == CmdNoOp {
		bw.WriteString(string(req.Command))
		bw.WriteString(CRLF)
		return bw.Flush()
	}

	// Validate key before writing
	hasBase64Flag := req.HasFlag(FlagBase64Key)
	if err := ValidateKey(req.Key, hasBase64Flag); err != nil {
		return err
	}

	// Build command line
	bw.WriteString(string(req.Command))
	bw.WriteString(Space)
	bw.WriteString(req.Key)

	// Add size for ms command
	if req.Command == CmdSet {
		bw.WriteString(Space)
		bw.WriteString(strconv.Itoa(len(req.Data)))
	}

	// Add flags
	for _, flag := range req.Flags {
		bw.WriteString(Space)
		bw.WriteByte(byte(flag.Type))
		if flag.Token != "" {
			bw.WriteString(flag.Token)
		}
	}

	// Add command line terminator
	bw.WriteString(CRLF)

	// Write data block for ms command
	if req.Command == CmdSet {
		if len(req.Data) > 0 {
			_, err := bw.Write(req.Data)
			if err != nil {
				return err
			}
		}

		// Write data terminator
		bw.WriteString(CRLF)
	}

	// Flush to ensure all data is written
	return bw.Flush()
}

// writeRequestUnbuffered writes using a pooled buffer (for tests and non-buffered writers).
func writeRequestUnbuffered(w io.Writer, req *Request) error {
	// Get buffer from pool
	buf := getBuffer()
	defer putBuffer(buf)

	// mn command has no key or flags
	if req.Command == CmdNoOp {
		buf.WriteString(string(req.Command))
		buf.WriteString(CRLF)
		_, err := w.Write(buf.Bytes())
		return err
	}

	// Validate key before writing
	hasBase64Flag := req.HasFlag(FlagBase64Key)
	if err := ValidateKey(req.Key, hasBase64Flag); err != nil {
		return err
	}

	// Build command line in buffer
	buf.WriteString(string(req.Command))
	buf.WriteString(Space)
	buf.WriteString(req.Key)

	// Add size for ms command
	if req.Command == CmdSet {
		buf.WriteString(Space)
		buf.WriteString(strconv.Itoa(len(req.Data)))
	}

	// Add flags
	for _, flag := range req.Flags {
		buf.WriteString(Space)
		buf.WriteByte(byte(flag.Type))
		if flag.Token != "" {
			buf.WriteString(flag.Token)
		}
	}

	// Add command line terminator
	buf.WriteString(CRLF)

	// Write command line
	_, err := w.Write(buf.Bytes())
	if err != nil {
		return err
	}

	// Write data block for ms command
	if req.Command == CmdSet {
		if len(req.Data) > 0 {
			_, err = w.Write(req.Data)
			if err != nil {
				return err
			}
		}

		// Write data terminator
		_, err = io.WriteString(w, CRLF)
		if err != nil {
			return err
		}
	}

	return nil
}
