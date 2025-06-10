package memcache

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"strconv"
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
	// Estimate buffer size: cmd (2) + space (1) + key_len + space (1) + datalen_str (max ~3 for 1GB) + flags_len + \r\n (2)
	// For flags, each is at least 1 char + space. Avg ~3 chars. Max flags ~5. So ~15.
	// Total estimate: 2 + 1 + len(key) + 1 + 3 + 15 + 2 = len(key) + 24
	// Add data block: datalen + \r\n (2)
	// Using a bytes.Buffer to build the command line
	var buf bytes.Buffer
	buf.WriteString(cmd)
	buf.WriteByte(' ')
	buf.WriteString(key)

	if cmd == "ms" || (datalen > 0 || (cmd == "ms" && datalen == 0)) {
		buf.WriteByte(' ')
		var temp [20]byte // Max length for int64 is 19 digits + sign. datalen is int.
		buf.Write(strconv.AppendInt(temp[:0], int64(datalen), 10))
	}

	for _, flag := range flags {
		buf.WriteByte(' ')
		buf.WriteString(string(flag))
	}
	buf.WriteString("\r\n")

	if _, err := mc.w.Write(buf.Bytes()); err != nil {
		return err
	}

	if cmd == "ms" || (datalen > 0 && len(data) == datalen) {
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
// It now returns args as [][]byte to reduce allocations.
func (mc *Conn) readResponse() (code string, args [][]byte, data []byte, err error) {
	lineBytes, err := mc.r.ReadSlice('\n')
	if err != nil {
		// If ReadSlice returns an error, it might be ErrBufferFull.
		// A more robust implementation might handle this by reading into a larger buffer.
		// For now, we propagate the error.
		return
	}

	// The line includes \n, and possibly \r. Trim them.
	// Note: ReadSlice returns a slice of the reader's buffer. It's valid until the next read.
	// We must copy the parts we need if they are to be stored long-term outside this function's scope
	// or passed to something that stores them.
	trimmedLine := bytes.TrimRight(lineBytes, "\r\n")

	if len(trimmedLine) == 0 {
		err = io.ErrUnexpectedEOF // Or a more specific error for empty line
		return
	}

	parts := bytes.Split(trimmedLine, []byte(" "))
	if len(parts) == 0 {
		err = io.ErrUnexpectedEOF // Should not happen if trimmedLine is not empty
		return
	}

	code = string(parts[0]) // Convert code to string here as it's stored directly in response structs.
	if code == "" {
		err = io.ErrUnexpectedEOF
		return
	}

	args = parts[1:] // args remain [][]byte

	if code == "VA" && len(args) > 0 {
		// Parse size directly from args[0] which is []byte
		var szInt int64
		szInt, err = strconv.ParseInt(string(args[0]), 10, 0) // strconv.Atoi needs string, ParseInt too.
		if err != nil {
			// err = fmt.Errorf(\"failed to parse VA size '%s': %w\", string(args[0]), err) // Keep original error for now
			data = nil
			return
		}
		sz := int(szInt)

		if sz < 0 {
			err = fmt.Errorf("VA response has negative size: %d", sz)
			return
		}
		if sz > 1024*1024*1024 { // 1GB limit
			err = fmt.Errorf("VA response size too large: %d bytes (max 1GB)", sz)
			return
		}

		valBuffer := make([]byte, sz+2) // Buffer to read data + \\r\\n
		n, readErr := io.ReadFull(mc.r, valBuffer)
		if readErr != nil {
			err = readErr
			if n > sz {
				data = valBuffer[:sz]
			} else {
				data = valBuffer[:n]
			}
			return
		}
		data = valBuffer[:sz] // Strip \\r\\n
	}
	return
}

// parseResponseFlagsAndVASize parses common flags from raw argument byte slices.
// It expects rawArgs to be the arguments *after* the response code.
// For "VA" responses, rawArgs[0] is the size, and actual flags start from rawArgs[1].
func parseResponseFlagsAndVASize(rawArgs [][]byte, isVA bool) (parsedFlags ResponseFlags, vaSize int, err error) {
	parsedFlags.TTL = -1 // Default TTL

	argsForFlags := rawArgs
	if isVA {
		if len(rawArgs) == 0 {
			return parsedFlags, 0, fmt.Errorf("VA response expects at least a size argument, got none")
		}
		// Size is already parsed by readResponse for VA, but we need to get it from rawArgs[0] if called independently.
		// For this refactor, readResponse handles VA size parsing for data reading.
		// This function will re-parse from rawArgs[0] if isVA is true for flag parsing consistency.
		var sizeInt64 int64
		sizeInt64, err = strconv.ParseInt(string(rawArgs[0]), 10, 0) // Atoi needs string
		if err != nil {
			return parsedFlags, 0, fmt.Errorf("failed to parse VA size '%s': %w", string(rawArgs[0]), err)
		}
		vaSize = int(sizeInt64)

		if vaSize < 0 {
			return parsedFlags, 0, fmt.Errorf("VA size cannot be negative: %d", vaSize)
		}
		if vaSize > 1024*1024*1024 { // 1GB limit
			return parsedFlags, 0, fmt.Errorf("VA size too large: %d bytes (max 1GB)", vaSize)
		}
		if len(rawArgs) > 1 {
			argsForFlags = rawArgs[1:]
		} else {
			argsForFlags = [][]byte{} // No flags after size
		}
	}

	for _, argBytes := range argsForFlags {
		if len(argBytes) < 2 { // Minimum one char type + one char value
			continue
		}
		flagType := argBytes[0]
		flagValueBytes := argBytes[1:]
		// Convert flagValueBytes to string only when necessary for parsing or storing
		// For numeric types, strconv functions can often take string version of []byte.

		switch flagType {
		case 'c', 'C': // CAS ID
			parsedFlags.CAS, err = strconv.ParseUint(string(flagValueBytes), 10, 64)
			if err != nil {
				return parsedFlags, vaSize, fmt.Errorf("failed to parse CAS value '%s' for flag 'c': %w", string(flagValueBytes), err)
			}
		case 'f', 'F': // Client Flags
			var fVal uint64
			fVal, err = strconv.ParseUint(string(flagValueBytes), 10, 32)
			if err != nil {
				return parsedFlags, vaSize, fmt.Errorf("failed to parse client flags '%s' for flag 'f': %w", string(flagValueBytes), err)
			}
			parsedFlags.ClientFlags = uint32(fVal)
		case 't', 'T': // TTL
			parsedFlags.TTL, err = strconv.Atoi(string(flagValueBytes))
			if err != nil {
				return parsedFlags, vaSize, fmt.Errorf("failed to parse TTL value '%s' for flag 't': %w", string(flagValueBytes), err)
			}
		case 'o', 'O': // Opaque token
			parsedFlags.Opaque = string(flagValueBytes) // Store as string
		case 'k', 'K': // Key
			parsedFlags.Key = string(flagValueBytes) // Store as string
		default:
			// Unknown flag
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
