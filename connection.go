package memcache

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
)

// ResponseFlags holds common flags parsed from a meta response.
type ResponseFlags struct {
	Opaque      string // o<token>
	CAS         uint64 // c<cas_id>
	ClientFlags uint32 // f<value>
	TTL         int    // t<seconds> (-1 if not present/applicable)
	Key         string // k<key> (if echoed back)
}

// GetResponse represents a response from a MetaGet operation.
type GetResponse struct {
	Code string // e.g., "VA", "EN", "ME"
	Data []byte // The value for "VA"
	Size int    // From "VA <size> ..."
	ResponseFlags
}

// MutateResponse represents a response from MetaSet, MetaDelete, MetaNoop.
type MutateResponse struct {
	Code string // e.g., "HD", "NS", "EX", "NF", "ME"
	ResponseFlags
}

// ArithmeticResponse represents a response from a MetaArithmetic operation.
// If the operation results in a new value (e.g., "VA"), Data will contain it.
type ArithmeticResponse struct {
	Code  string // e.g., "VA", "NF", "HD", "ME"
	Data  []byte // The new value (numeric, as bytes) for "VA"
	Size  int    // From "VA <size> ..."
	Value uint64 // Parsed numeric value if Data is present and numeric for "VA"
	ResponseFlags
}

// Conn wraps a net.Conn for the memcached meta protocol.
type Conn struct {
	c net.Conn
	r *bufio.Reader
	w *bufio.Writer
}

// NewConn creates a new Conn from a net.Conn.
func NewConn(c net.Conn) *Conn {
	return &Conn{
		c: c,
		r: bufio.NewReader(c),
		w: bufio.NewWriter(c),
	}
}

// Close closes the connection.
func (mc *Conn) Close() error {
	return mc.c.Close()
}

// sendCommand writes a meta protocol command line and optional data block.
func (mc *Conn) sendCommand(cmd string, key string, datalen int, flags []MetaFlag, data []byte) error {
	line := cmd + " " + key
	// Always include datalen for commands that might have a data block (like ms)
	// For commands like mg, md, ma, mn, datalen is conceptually 0 and not sent by callers if no data block.
	// The protocol for 'ms' requires <datalen>.
	if cmd == "ms" || (datalen > 0 || (cmd == "ms" && datalen == 0)) { // Ensure datalen is added for 'ms' even if 0, or if datalen > 0 for any cmd.
		line += fmt.Sprintf(" %d", datalen)
	}
	if len(flags) > 0 {
		for _, flag := range flags {
			line += " " + string(flag)
		}
	}
	line += "\r\n"
	if _, err := mc.w.WriteString(line); err != nil {
		return err
	}
	// Send data block if data is not nil. For ms, datalen is key.
	// For ms with datalen 0, data might be empty or nil, but \r\n is still needed.
	if cmd == "ms" || (datalen > 0 && len(data) == datalen) { // Adjusted condition
		if len(data) > 0 {
			if _, err := mc.w.Write(data); err != nil {
				return err
			}
		}
		if _, err := mc.w.WriteString("\r\n"); err != nil {
			return err
		}
	}
	return mc.w.Flush()
}

// readResponse reads a single response line and optional data block.
func (mc *Conn) readResponse() (code string, args []string, data []byte, err error) {
	line, err := mc.r.ReadString('\n')
	if err != nil {
		return
	}
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		err = io.ErrUnexpectedEOF
		return
	}
	parts := strings.Split(line, " ")
	if len(parts) == 0 {
		err = io.ErrUnexpectedEOF
		return
	}
	code = parts[0]
	if code == "" {
		err = io.ErrUnexpectedEOF
		return
	}
	args = parts[1:]
	if code == "VA" && len(args) > 0 {
		sz, err2 := strconv.Atoi(args[0])
		if err2 != nil {
			err = err2
			data = nil // Ensure data is nil if Atoi fails
			return
		}
		if sz < 0 {
			err = fmt.Errorf("VA response has negative size: %d", sz)
			return
		}
		if sz > 1024*1024*1024 { // 1GB limit to prevent memory exhaustion
			err = fmt.Errorf("VA response size too large: %d bytes (max 1GB)", sz)
			return
		}

		valBuffer := make([]byte, sz+2)            // Buffer to read data + \\r\\n
		n, readErr := io.ReadFull(mc.r, valBuffer) // n is bytes read into valBuffer

		if readErr != nil {
			err = readErr // Propagate the error
			// If bytes were read before the error, return those (up to sz).
			// n is the number of bytes read into valBuffer.
			if n > sz { // If more than sz bytes were read (i.e., into the \\r\\n part)
				data = valBuffer[:sz]
			} else { // Otherwise, n <= sz (partial data, full data, or no data if n=0)
				data = valBuffer[:n] // If n=0, this results in an empty slice.
			}
			return
		}
		// Success: sz+2 bytes were read
		data = valBuffer[:sz] // Strip \\r\\n
	}
	return
}

// parseResponseFlagsAndVASize parses common flags from raw argument strings.
// It expects rawArgs to be the arguments *after* the response code.
// For "VA" responses, rawArgs[0] is the size, and actual flags start from rawArgs[1].
func parseResponseFlagsAndVASize(rawArgs []string, isVA bool) (parsedFlags ResponseFlags, vaSize int, err error) {
	parsedFlags.TTL = -1 // Default TTL if not specified

	argsForFlags := rawArgs
	if isVA {
		if len(rawArgs) == 0 {
			return parsedFlags, 0, fmt.Errorf("VA response expects at least a size argument, got none")
		}
		vaSize, err = strconv.Atoi(rawArgs[0])
		if err != nil {
			return parsedFlags, 0, fmt.Errorf("failed to parse VA size '%s': %w", rawArgs[0], err)
		}
		if vaSize < 0 {
			return parsedFlags, 0, fmt.Errorf("VA size cannot be negative: %d", vaSize)
		}
		if vaSize > 1024*1024*1024 { // 1GB limit to prevent memory exhaustion
			return parsedFlags, 0, fmt.Errorf("VA size too large: %d bytes (max 1GB)", vaSize)
		}
		if len(rawArgs) > 1 {
			argsForFlags = rawArgs[1:]
		} else {
			argsForFlags = []string{} // No flags after size
		}
	}

	for _, arg := range argsForFlags {
		if len(arg) < 2 { // Minimum one char type + one char value
			continue // Or log a warning about malformed arg
		}
		flagType := arg[0]
		flagValue := arg[1:]

		switch flagType {
		case 'c', 'C': // CAS ID
			parsedFlags.CAS, err = strconv.ParseUint(flagValue, 10, 64)
			if err != nil {
				return parsedFlags, vaSize, fmt.Errorf("failed to parse CAS value '%s' for flag 'c': %w", flagValue, err)
			}
		case 'f', 'F': // Client Flags
			var fVal uint64
			fVal, err = strconv.ParseUint(flagValue, 10, 32)
			if err != nil {
				return parsedFlags, vaSize, fmt.Errorf("failed to parse client flags '%s' for flag 'f': %w", flagValue, err)
			}
			parsedFlags.ClientFlags = uint32(fVal)
		case 't', 'T': // TTL
			parsedFlags.TTL, err = strconv.Atoi(flagValue)
			if err != nil {
				return parsedFlags, vaSize, fmt.Errorf("failed to parse TTL value '%s' for flag 't': %w", flagValue, err)
			}
		case 'o', 'O': // Opaque token
			parsedFlags.Opaque = flagValue
		case 'k', 'K': // Key
			parsedFlags.Key = flagValue
		default:
			// Unknown flag, could log or ignore.
		}
	}
	return parsedFlags, vaSize, nil
}

// MetaGet issues an mg (meta get) command and returns the response.
func (mc *Conn) MetaGet(key string, flags ...MetaFlag) (resp GetResponse, err error) {
	err = mc.sendCommand("mg", key, 0, flags, nil)
	if err != nil {
		return
	}

	rawCode, rawArgs, rawData, errRead := mc.readResponse()
	if errRead != nil {
		resp.Code = rawCode // Populate code even on error if available
		resp.Data = rawData // Include any partial data that was read
		err = errRead
		return
	}

	resp.Code = rawCode
	resp.Data = rawData

	isVA := (rawCode == "VA")
	parsedFlags, vaSizeFromArgs, parseErr := parseResponseFlagsAndVASize(rawArgs, isVA)
	if parseErr != nil {
		err = fmt.Errorf("MetaGet: %w (raw response: code=%s, args=%#v)", parseErr, rawCode, rawArgs)
		return
	}

	resp.ResponseFlags = parsedFlags
	if isVA {
		resp.Size = vaSizeFromArgs
		if len(resp.Data) != resp.Size {
			err = fmt.Errorf("MetaGet: VA size from args (%d) mismatch with data length (%d) (raw response: code=%s, args=%#v)", resp.Size, len(resp.Data), rawCode, rawArgs)
			return
		}
	}
	return
}

func (mc *Conn) MetaSet(key string, value []byte, flags ...MetaFlag) (resp MutateResponse, err error) {
	err = mc.sendCommand("ms", key, len(value), flags, value)
	if err != nil {
		return
	}

	rawCode, rawArgs, _, errRead := mc.readResponse()
	if errRead != nil {
		resp.Code = rawCode
		err = errRead
		return
	}
	resp.Code = rawCode
	parsedFlags, _, parseErr := parseResponseFlagsAndVASize(rawArgs, false) // isVA is false
	if parseErr != nil {
		err = fmt.Errorf("MetaSet: %w (raw response: code=%s, args=%#v)", parseErr, rawCode, rawArgs)
		return
	}
	resp.ResponseFlags = parsedFlags
	return
}

func (mc *Conn) MetaDelete(key string, flags ...MetaFlag) (resp MutateResponse, err error) {
	err = mc.sendCommand("md", key, 0, flags, nil)
	if err != nil {
		return
	}
	rawCode, rawArgs, _, errRead := mc.readResponse()
	if errRead != nil {
		resp.Code = rawCode
		err = errRead
		return
	}
	resp.Code = rawCode
	parsedFlags, _, parseErr := parseResponseFlagsAndVASize(rawArgs, false) // isVA is false
	if parseErr != nil {
		err = fmt.Errorf("MetaDelete: %w (raw response: code=%s, args=%#v)", parseErr, rawCode, rawArgs)
		return
	}
	resp.ResponseFlags = parsedFlags
	return
}

func (mc *Conn) MetaArithmetic(key string, flags ...MetaFlag) (resp ArithmeticResponse, err error) {
	err = mc.sendCommand("ma", key, 0, flags, nil)
	if err != nil {
		return
	}

	rawCode, rawArgs, rawData, errRead := mc.readResponse()
	if errRead != nil {
		resp.Code = rawCode
		err = errRead
		return
	}

	resp.Code = rawCode
	resp.Data = rawData

	isVA := (rawCode == "VA")
	parsedFlags, vaSizeFromArgs, parseErr := parseResponseFlagsAndVASize(rawArgs, isVA)
	if parseErr != nil {
		err = fmt.Errorf("MetaArithmetic: %w (raw response: code=%s, args=%#v)", parseErr, rawCode, rawArgs)
		return
	}
	resp.ResponseFlags = parsedFlags

	if isVA {
		resp.Size = vaSizeFromArgs
		if len(resp.Data) != resp.Size {
			err = fmt.Errorf("MetaArithmetic: VA size from args (%d) mismatch with data length (%d) (raw response: code=%s, args=%#v)", resp.Size, len(resp.Data), rawCode, rawArgs)
			return
		}
		if len(resp.Data) > 0 {
			valStr := string(resp.Data)
			resp.Value, err = strconv.ParseUint(valStr, 10, 64)
			if err != nil {
				err = fmt.Errorf("MetaArithmetic: failed to parse VA data '%s' as uint64: %w (raw response: code=%s, args=%#v)", valStr, err, rawCode, rawArgs)
				return
			}
		} else if resp.Size > 0 {
			err = fmt.Errorf("MetaArithmetic: VA response indicates size %d but no data received (raw response: code=%s, args=%#v)", resp.Size, rawCode, rawArgs)
			return
		}
	}
	return
}

func (mc *Conn) MetaNoop() (resp MutateResponse, err error) {
	err = mc.sendCommand("mn", "", 0, nil, nil)
	if err != nil {
		return
	}
	rawCode, rawArgs, _, errRead := mc.readResponse()
	if errRead != nil {
		resp.Code = rawCode
		err = errRead
		return
	}
	resp.Code = rawCode
	parsedFlags, _, parseErr := parseResponseFlagsAndVASize(rawArgs, false) // isVA false
	if parseErr != nil {
		err = fmt.Errorf("MetaNoop: %w (raw response: code=%s, args=%#v)", parseErr, rawCode, rawArgs)
		return
	}
	resp.ResponseFlags = parsedFlags
	return
}
