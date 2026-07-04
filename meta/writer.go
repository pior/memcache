package meta

import (
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
	// Drop buffers that have grown too large (> 4KB) to avoid holding excess memory
	const maxBufferSize = 4096
	if buf.Cap() > maxBufferSize {
		return
	}
	buf.Reset()
	bufferPool.Put(buf)
}

// ValidateKey checks if a key is valid for the memcache protocol.
// Keys must be 1-250 bytes and contain no whitespace. This applies to
// base64-encoded keys as well: the base64 alphabet contains no whitespace,
// and the encoded form is what travels on the wire.
// Returns an error describing the validation failure.
func ValidateKey(key string) error {
	keyLen := len(key)

	if keyLen < MinKeyLength {
		return &InvalidRequestError{Message: "key is empty"}
	}

	if keyLen > MaxKeyLength {
		return &InvalidRequestError{Message: "key exceeds maximum length of 250 bytes"}
	}

	if strings.ContainsAny(key, " \t\r\n") {
		return &InvalidRequestError{Message: "key contains whitespace"}
	}

	return nil
}

// ValidateRequest checks every user-controlled request field serialized by
// WriteRequest.
func ValidateRequest(req *Request) error {
	switch req.Command {
	case CmdNoOp:
		return nil
	case CmdStats:
		if strings.ContainsAny(req.Key, "\r\n") {
			return &InvalidRequestError{Message: "stats argument contains CR or LF"}
		}
		return nil
	}

	if err := ValidateKey(req.Key); err != nil {
		return err
	}

	for i := 0; i < len(req.Flags); {
		i = flagsSkipSpaces(req.Flags, i)
		if i >= len(req.Flags) {
			break
		}

		flagType := FlagType(req.Flags[i])
		if flagType == '\r' || flagType == '\n' {
			return &InvalidRequestError{Message: "request flags contain CR or LF"}
		}
		i++
		start := i
		for i < len(req.Flags) && req.Flags[i] != ' ' {
			if req.Flags[i] == '\r' || req.Flags[i] == '\n' {
				return &InvalidRequestError{Message: "request flags contain CR or LF"}
			}
			i++
		}
		if flagType == FlagOpaque && i-start > MaxOpaqueLength {
			return &InvalidRequestError{Message: "opaque token exceeds maximum length of 32 bytes"}
		}
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
// Validates request fields before writing to prevent protocol errors.
//
// Performance considerations:
//   - Uses pooled buffer to build request header in memory
//   - Single write call for header reduces syscalls
//   - Data block written directly (no buffering for large values)
func WriteRequest(w io.Writer, req *Request) error {
	if err := ValidateRequest(req); err != nil {
		return err
	}

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

	// stats command has optional args but no key or flags
	if req.Command == CmdStats {
		buf.WriteString(string(req.Command))
		if req.Key != "" {
			buf.WriteString(Space)
			buf.WriteString(req.Key)
		}
		buf.WriteString(CRLF)
		_, err := w.Write(buf.Bytes())
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

	// Add flags.
	// Flags already include their leading spaces.
	if len(req.Flags) > 0 {
		buf.Write(req.Flags)
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
