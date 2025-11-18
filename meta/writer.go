package meta

import (
	"io"
	"strconv"
)

// WriteRequest serializes a Request to wire format and writes it to w.
// Format: <command> <key> [<size>] <flags>*\r\n[<data>\r\n]
//
// For ms command: ms <key> <size> <flags>*\r\n<data>\r\n
// For other commands: <cmd> <key> <flags>*\r\n
// For mn command: mn\r\n
//
// Returns the number of bytes written and any error encountered.
// Does not perform validation - assumes Request is well-formed.
// Caller is responsible for validating key length, opaque length, etc.
//
// Performance considerations:
//   - Minimizes allocations by writing directly to io.Writer
//   - Uses byte buffer for small allocations
//   - Avoids string concatenation
func WriteRequest(w io.Writer, req *Request) (int, error) {
	var total int

	// Write command
	n, err := io.WriteString(w, string(req.Command))
	total += n
	if err != nil {
		return total, err
	}

	// mn command has no key or flags
	if req.Command == CmdNoOp {
		n, err = io.WriteString(w, CRLF)
		total += n
		return total, err
	}

	// Write key
	n, err = io.WriteString(w, Space)
	total += n
	if err != nil {
		return total, err
	}

	n, err = io.WriteString(w, req.Key)
	total += n
	if err != nil {
		return total, err
	}

	// Write size for ms command
	if req.Command == CmdSet {
		n, err = io.WriteString(w, Space)
		total += n
		if err != nil {
			return total, err
		}

		// Convert data length to string
		size := strconv.Itoa(len(req.Data))
		n, err = io.WriteString(w, size)
		total += n
		if err != nil {
			return total, err
		}
	}

	// Write flags
	for _, flag := range req.Flags {
		n, err = io.WriteString(w, Space)
		total += n
		if err != nil {
			return total, err
		}

		// Write flag type
		buf := []byte{byte(flag.Type)}
		n, err = w.Write(buf)
		total += n
		if err != nil {
			return total, err
		}

		// Write flag token if present
		if flag.Token != "" {
			n, err = io.WriteString(w, flag.Token)
			total += n
			if err != nil {
				return total, err
			}
		}
	}

	// Write command line terminator
	n, err = io.WriteString(w, CRLF)
	total += n
	if err != nil {
		return total, err
	}

	// Write data block for ms command
	if req.Command == CmdSet && len(req.Data) > 0 {
		n, err = w.Write(req.Data)
		total += n
		if err != nil {
			return total, err
		}

		n, err = io.WriteString(w, CRLF)
		total += n
		if err != nil {
			return total, err
		}
	}

	// Write data block terminator for ms with zero-length data
	if req.Command == CmdSet && len(req.Data) == 0 {
		n, err = io.WriteString(w, CRLF)
		total += n
		if err != nil {
			return total, err
		}
	}

	return total, nil
}
