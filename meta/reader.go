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
// Error handling considerations:
//   - io.EOF: Connection closed (clean shutdown or unexpected)
//   - ErrClientError: Protocol state corrupted, connection should be closed
//   - ErrServerError: Server-side error, connection can be retried
//   - Other errors: Parse errors, connection should be evaluated by caller
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

	// Parse response line manually to avoid strings.Fields allocation
	if len(line) == 0 {
		return nil, &ParseError{Message: "empty response line"}
	}

	// Pre-allocate flags with typical capacity
	flags := make([]Flag, 0, 4)

	// Parse status (first 2 characters or until space)
	spaceIdx := strings.IndexByte(line, ' ')
	var status StatusType
	var remaining string

	if spaceIdx == -1 {
		// No space - entire line is status
		status = StatusType(line)
		remaining = ""
	} else {
		status = StatusType(line[:spaceIdx])
		remaining = line[spaceIdx+1:]
	}

	resp := &Response{
		Status: status,
		Flags:  flags,
	}

	// MN response has no additional data
	if resp.Status == StatusMN {
		return resp, nil
	}

	// VA response has size as second field
	var dataSize int
	if resp.Status == StatusVA && len(remaining) > 0 {
		// Find the size field (everything before next space)
		spaceIdx = strings.IndexByte(remaining, ' ')
		var sizeStr string
		if spaceIdx == -1 {
			sizeStr = remaining
			remaining = ""
		} else {
			sizeStr = remaining[:spaceIdx]
			remaining = remaining[spaceIdx+1:]
		}

		dataSize, err = strconv.Atoi(sizeStr)
		if err != nil {
			return nil, &ParseError{Message: "invalid size in VA response: " + sizeStr}
		}
	}

	// Parse flags from remaining string
	for len(remaining) > 0 {
		// Skip leading spaces
		if remaining[0] == ' ' {
			remaining = remaining[1:]
			continue
		}

		// Find next space or end of string
		spaceIdx = strings.IndexByte(remaining, ' ')
		var flagStr string
		if spaceIdx == -1 {
			flagStr = remaining
			remaining = ""
		} else {
			flagStr = remaining[:spaceIdx]
			remaining = remaining[spaceIdx+1:]
		}

		if len(flagStr) == 0 {
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
	}

	// Read data block for VA responses
	if resp.Status == StatusVA {
		// Allocate buffer for data
		data := make([]byte, dataSize)

		// Read data block
		_, err = io.ReadFull(r, data)
		if err != nil {
			return nil, &ParseError{Message: "failed to read data block: " + err.Error()}
		}

		resp.Data = data

		// Read trailing CRLF
		crlf := make([]byte, 2)
		_, err = io.ReadFull(r, crlf)
		if err != nil {
			return nil, &ParseError{Message: "failed to read data block CRLF: " + err.Error()}
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
					return nil, &ParseError{Message: "failed to unread byte: " + err.Error()}
				}
			}
		}
	}

	// Handle ME (debug) response - read until next line
	if resp.Status == StatusME {
		// ME response format: ME <key> <key>=<value>*\r\n
		// For simplicity, we've already parsed the line
		// Data can be reconstructed from parts if needed
		// For now, we leave Data empty as ME is rarely used
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

// PeekStatus peeks at the next response status without consuming it.
// Returns the 2-character status code or error.
// Useful for determining how to handle the next response without fully parsing it.
//
// Example:
//
//	status, err := PeekStatus(r)
//	if err != nil {
//	    return err
//	}
//	if status == StatusVA {
//	    // Prepare to read value data
//	}
func PeekStatus(r *bufio.Reader) (string, error) {
	// Peek at least 2 bytes for status code
	b, err := r.Peek(2)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
