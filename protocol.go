package memcache

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strconv"
	"strings"
)

var (
	ErrInvalidResponse = errors.New("memcache: invalid response")
	ErrMalformedKey    = errors.New("memcache: malformed key")
)

// MetaCommand represents a memcache meta protocol command
type MetaCommand struct {
	Type   string
	Key    string
	Flags  map[string]string
	Value  []byte
	Opaque string
}

// MetaResponse represents a memcache meta protocol response
type MetaResponse struct {
	Status string
	Flags  map[string]string
	Value  []byte
	Opaque string
}

// FormatGetCommand formats a meta get (mg) command
func FormatGetCommand(key string, flags []string, opaque string) []byte {
	if !isValidKey(key) {
		return nil
	}

	var buf bytes.Buffer
	buf.WriteString("mg ")
	buf.WriteString(key)

	for _, flag := range flags {
		buf.WriteByte(' ')
		buf.WriteString(flag)
	}

	if opaque != "" {
		buf.WriteString(" O")
		buf.WriteString(opaque)
	}

	buf.WriteString("\r\n")
	return buf.Bytes()
}

// FormatSetCommand formats a meta set (ms) command
func FormatSetCommand(key string, value []byte, ttl int, flags map[string]string, opaque string) []byte {
	if !isValidKey(key) {
		return nil
	}

	var buf bytes.Buffer
	buf.WriteString("ms ")
	buf.WriteString(key)
	buf.WriteByte(' ')
	buf.WriteString(strconv.Itoa(len(value)))

	if ttl > 0 {
		buf.WriteString(" T")
		buf.WriteString(strconv.Itoa(ttl))
	}

	for flag, val := range flags {
		buf.WriteByte(' ')
		buf.WriteString(flag)
		if val != "" {
			buf.WriteString(val)
		}
	}

	if opaque != "" {
		buf.WriteString(" O")
		buf.WriteString(opaque)
	}

	buf.WriteString("\r\n")
	buf.Write(value)
	buf.WriteString("\r\n")
	return buf.Bytes()
}

// FormatDeleteCommand formats a meta delete (md) command
func FormatDeleteCommand(key string, opaque string) []byte {
	if !isValidKey(key) {
		return nil
	}

	var buf bytes.Buffer
	buf.WriteString("md ")
	buf.WriteString(key)

	if opaque != "" {
		buf.WriteString(" O")
		buf.WriteString(opaque)
	}

	buf.WriteString("\r\n")
	return buf.Bytes()
}

// ParseResponse parses a meta protocol response
func ParseResponse(reader *bufio.Reader) (*MetaResponse, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}

	// Remove \r\n
	line = strings.TrimRight(line, "\r\n")

	if line == "" {
		return nil, ErrInvalidResponse
	}

	parts := strings.Split(line, " ")
	if len(parts) < 1 {
		return nil, ErrInvalidResponse
	}

	resp := &MetaResponse{
		Status: parts[0],
		Flags:  make(map[string]string),
	}

	// Parse flags
	for i := 1; i < len(parts); i++ {
		part := parts[i]
		if len(part) == 0 {
			continue
		}

		switch part[0] {
		case 'O':
			resp.Opaque = part[1:]
		case 's':
			if len(part) > 1 {
				if size, err := strconv.Atoi(part[1:]); err == nil && size > 0 {
					value := make([]byte, size)
					if _, err := io.ReadFull(reader, value); err != nil {
						return nil, err
					}
					resp.Value = value
					// Read trailing \r\n
					reader.ReadString('\n')
				}
			}
		default:
			if strings.Contains(part, "=") {
				kv := strings.SplitN(part, "=", 2)
				resp.Flags[kv[0]] = kv[1]
			} else {
				resp.Flags[part] = ""
			}
		}
	}

	return resp, nil
}

// isValidKey checks if a key is valid according to memcache protocol
func isValidKey(key string) bool {
	if len(key) == 0 || len(key) > 250 {
		return false
	}

	for _, b := range []byte(key) {
		if b <= 32 || b == 127 {
			return false
		}
	}

	return true
}
