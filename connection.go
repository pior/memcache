package memcache

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
)

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
func (mc *Conn) sendCommand(cmd string, key string, datalen int, flags []string, data []byte) error {
	line := cmd + " " + key
	// Always include datalen for commands that might have a data block (like ms)
	// For commands like mg, md, ma, mn, datalen is conceptually 0 and not sent by callers if no data block.
	// The protocol for 'ms' requires <datalen>.
	if cmd == "ms" || (datalen > 0 || (cmd == "ms" && datalen == 0)) { // Ensure datalen is added for 'ms' even if 0, or if datalen > 0 for any cmd.
		line += fmt.Sprintf(" %d", datalen)
	}
	if len(flags) > 0 {
		line += " " + strings.Join(flags, " ")
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
	parts := strings.Split(line, " ")
	if len(parts) == 0 {
		err = io.ErrUnexpectedEOF
		return
	}
	code = parts[0]
	args = parts[1:]
	if code == "VA" && len(args) > 0 {
		sz, err2 := strconv.Atoi(args[0])
		if err2 != nil {
			err = err2
			data = nil // Ensure data is nil if Atoi fails
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

// MetaGet issues an mg (meta get) command and returns the response.
func (mc *Conn) MetaGet(key string, flags ...MetaFlag) (code string, args []string, data []byte, err error) {
	return mc.metaGet(key, flags)
}

func (mc *Conn) metaGet(key string, flags []MetaFlag) (code string, args []string, data []byte, err error) {
	if len(flags) == 0 {
		err = mc.sendCommand("mg", key, 0, nil, nil)
	} else {
		strFlags := make([]string, len(flags))
		for i, f := range flags {
			strFlags[i] = string(f)
		}
		err = mc.sendCommand("mg", key, 0, strFlags, nil)
	}
	if err != nil {
		return
	}
	return mc.readResponse()
}

func (mc *Conn) MetaSet(key string, value []byte, flags ...MetaFlag) (code string, args []string, err error) {
	return mc.metaSet(key, value, flags)
}

func (mc *Conn) metaSet(key string, value []byte, flags []MetaFlag) (code string, args []string, err error) {
	var err2 error
	if len(flags) == 0 {
		err2 = mc.sendCommand("ms", key, len(value), nil, value)
	} else {
		strFlags := make([]string, len(flags))
		for i, f := range flags {
			strFlags[i] = string(f)
		}
		err2 = mc.sendCommand("ms", key, len(value), strFlags, value)
	}
	if err2 != nil {
		err = err2
		return
	}
	code, args, _, err = mc.readResponse()
	return
}

func (mc *Conn) MetaDelete(key string, flags ...MetaFlag) (code string, args []string, err error) {
	return mc.metaDelete(key, flags)
}

func (mc *Conn) metaDelete(key string, flags []MetaFlag) (code string, args []string, err error) {
	var err2 error
	if len(flags) == 0 {
		err2 = mc.sendCommand("md", key, 0, nil, nil)
	} else {
		strFlags := make([]string, len(flags))
		for i, f := range flags {
			strFlags[i] = string(f)
		}
		err2 = mc.sendCommand("md", key, 0, strFlags, nil)
	}
	if err2 != nil {
		err = err2
		return
	}
	code, args, _, err = mc.readResponse()
	return
}

func (mc *Conn) MetaArithmetic(key string, flags ...MetaFlag) (code string, args []string, data []byte, err error) {
	return mc.metaArithmetic(key, flags)
}

func (mc *Conn) metaArithmetic(key string, flags []MetaFlag) (code string, args []string, data []byte, err error) {
	var err2 error
	if len(flags) == 0 {
		err2 = mc.sendCommand("ma", key, 0, nil, nil)
	} else {
		strFlags := make([]string, len(flags))
		for i, f := range flags {
			strFlags[i] = string(f)
		}
		err2 = mc.sendCommand("ma", key, 0, strFlags, nil)
	}
	if err2 != nil {
		err = err2
		return
	}
	return mc.readResponse()
}

func (mc *Conn) MetaNoop() (code string, args []string, err error) {
	err = mc.sendCommand("mn", "", 0, nil, nil)
	if err != nil {
		return
	}
	code, args, _, err = mc.readResponse()
	return
}
