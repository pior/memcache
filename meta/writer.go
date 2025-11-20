package meta

import (
	"io"
	"strconv"
	"strings"
)

// InvalidKeyError is returned when a key fails validation.
type InvalidKeyError struct {
	msg string
}

func (e *InvalidKeyError) Error() string {
	return e.msg
}

// ValidateKey checks if a key is valid for the memcache protocol.
// Keys must be 1-250 bytes and contain no whitespace (unless base64-encoded).
// Returns an error describing the validation failure.
func ValidateKey(key string, hasBase64Flag bool) error {
	keyLen := len(key)

	if keyLen < MinKeyLength {
		return &InvalidKeyError{msg: "key is empty"}
	}

	if keyLen > MaxKeyLength {
		return &InvalidKeyError{msg: "key exceeds maximum length of 250 bytes"}
	}

	// Whitespace is only allowed if key is base64-encoded
	if !hasBase64Flag && strings.ContainsAny(key, " \t\r\n") {
		return &InvalidKeyError{msg: "key contains whitespace"}
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
//   - Minimizes allocations by writing directly to io.Writer
//   - Uses byte buffer for small allocations
//   - Avoids string concatenation
func WriteRequest(w io.Writer, req *Request) error {
	// mn command has no key or flags
	if req.Command == CmdNoOp {
		// Write command
		_, err := io.WriteString(w, string(req.Command))
		if err != nil {
			return err
		}

		_, err = io.WriteString(w, CRLF)
		return err
	}

	// Validate key before writing
	hasBase64Flag := req.HasFlag(FlagBase64Key)
	if err := ValidateKey(req.Key, hasBase64Flag); err != nil {
		return err
	}

	// Write command
	_, err := io.WriteString(w, string(req.Command))
	if err != nil {
		return err
	}

	// Write key
	_, err = io.WriteString(w, Space)
	if err != nil {
		return err
	}

	_, err = io.WriteString(w, req.Key)
	if err != nil {
		return err
	}

	// Write size for ms command
	if req.Command == CmdSet {
		_, err = io.WriteString(w, Space)
		if err != nil {
			return err
		}

		// Convert data length to string
		size := strconv.Itoa(len(req.Data))
		_, err = io.WriteString(w, size)
		if err != nil {
			return err
		}
	}

	// Write flags
	for _, flag := range req.Flags {
		_, err = io.WriteString(w, Space)
		if err != nil {
			return err
		}

		// Write flag type
		buf := []byte{byte(flag.Type)}
		_, err = w.Write(buf)
		if err != nil {
			return err
		}

		// Write flag token if present
		if flag.Token != "" {
			_, err = io.WriteString(w, flag.Token)
			if err != nil {
				return err
			}
		}
	}

	// Write command line terminator
	_, err = io.WriteString(w, CRLF)
	if err != nil {
		return err
	}

	// Write data block for ms command
	if req.Command == CmdSet && len(req.Data) > 0 {
		_, err = w.Write(req.Data)
		if err != nil {
			return err
		}

		_, err = io.WriteString(w, CRLF)
		if err != nil {
			return err
		}
	}

	// Write data block terminator for ms with zero-length data
	if req.Command == CmdSet && len(req.Data) == 0 {
		_, err = io.WriteString(w, CRLF)
		if err != nil {
			return err
		}
	}

	return nil
}
