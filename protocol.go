package memcache

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

var (
	ErrInvalidResponse = errors.New("memcache: invalid response")
	ErrMalformedKey    = errors.New("memcache: malformed key")
)

type metaResponse struct {
	Status string
	Flags  map[string]string
	Value  []byte
	Opaque string
}

// ParseResponse parses a meta protocol response
func ParseResponse(reader *bufio.Reader) (*metaResponse, error) {
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

	resp := &metaResponse{
		Status: parts[0],
		Flags:  make(map[string]string),
	}

	// Parse flags and handle VA response format
	valueRead := false
	for i := 1; i < len(parts); i++ {
		part := parts[i]
		if len(part) == 0 {
			continue
		}

		switch part[0] {
		case 'O':
			resp.Opaque = part[1:]
		case 's':
			// s flag is always metadata, never triggers value reading
			if len(part) > 1 {
				resp.Flags["s"] = part[1:]
			} else {
				resp.Flags["s"] = ""
			}
		default:
			// Check if it's a plain number (size for VA responses)
			if resp.Status == StatusVA && !valueRead {
				if size, err := strconv.Atoi(part); err == nil && size > 0 {
					value := make([]byte, size)
					if _, err := io.ReadFull(reader, value); err != nil {
						return nil, err
					}
					resp.Value = value
					// Read trailing \r\n
					reader.ReadString('\n')
					valueRead = true
					continue
				}
			}

			// Handle other flags
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
	if len(key) == 0 || len(key) > MaxKeyLength {
		return false
	}

	for _, b := range []byte(key) {
		if b <= 32 || b == 127 {
			return false
		}
	}

	return true
}

// commandToProtocol converts a Command to protocol bytes
func commandToProtocol(cmd *Command) []byte {
	switch cmd.Type {
	case CmdMetaGet:
		flags := make([]string, 0, len(cmd.Flags))
		for flag, value := range cmd.Flags {
			if value == "" {
				flags = append(flags, flag)
			} else {
				flags = append(flags, flag+value)
			}
		}
		return formatGetCommand(cmd.Key, flags, generateOpaque())

	case CmdMetaSet:
		return formatSetCommand(cmd.Key, cmd.Value, cmd.TTL, cmd.Flags, generateOpaque())

	case CmdMetaDelete:
		return formatDeleteCommand(cmd.Key, generateOpaque())

	case CmdMetaArithmetic:
		return formatArithmeticCommand(cmd.Key, cmd.Flags, generateOpaque())

	case CmdMetaDebug:
		return formatDebugCommand(cmd.Key, cmd.Flags, generateOpaque())

	case CmdMetaNoOp:
		return formatNoOpCommand(generateOpaque())

	default:
		return nil
	}
}

// protocolToResponse converts a MetaResponse to Response
func protocolToResponse(metaResp *metaResponse, originalKey string) *Response {
	resp := &Response{
		Status: metaResp.Status,
		Key:    originalKey,
		Value:  metaResp.Value,
		Flags:  metaResp.Flags,
	}

	// Set error based on status using constants
	switch metaResp.Status {
	case StatusEN, StatusNF:
		resp.Error = ErrCacheMiss
	case StatusHD, StatusVA, StatusMN, StatusME:
		// Success, no error
		// HD = Hit/stored, VA = Value follows, MN = Meta no-op, ME = Meta debug
	case StatusNS:
		resp.Error = fmt.Errorf("memcache: not stored")
	case StatusEX:
		resp.Error = fmt.Errorf("memcache: item exists")
	case StatusServerError:
		resp.Error = fmt.Errorf("memcache: server error")
	case StatusClientError:
		resp.Error = fmt.Errorf("memcache: client error")
	case StatusError:
		resp.Error = fmt.Errorf("memcache: error")
	default:
		resp.Error = fmt.Errorf("memcache: unexpected status %s", metaResp.Status)
	}

	return resp
}

// formatGetCommand formats a meta get (mg) command
func formatGetCommand(key string, flags []string, opaque string) []byte {
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

// formatSetCommand formats a meta set (ms) command
func formatSetCommand(key string, value []byte, ttl int, flags map[string]string, opaque string) []byte {
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

// formatDeleteCommand formats a meta delete (md) command
func formatDeleteCommand(key string, opaque string) []byte {
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

// formatArithmeticCommand formats a meta arithmetic (ma) command
func formatArithmeticCommand(key string, flags map[string]string, opaque string) []byte {
	if !isValidKey(key) {
		return nil
	}

	var buf bytes.Buffer
	buf.WriteString("ma ")
	buf.WriteString(key)

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
	return buf.Bytes()
}

// formatDebugCommand formats a meta debug (me) command
func formatDebugCommand(key string, flags map[string]string, opaque string) []byte {
	var buf bytes.Buffer
	buf.WriteString("me")

	if key != "" {
		buf.WriteByte(' ')
		buf.WriteString(key)
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
	return buf.Bytes()
}

// formatNoOpCommand formats a meta no-op (mn) command
func formatNoOpCommand(opaque string) []byte {
	var buf bytes.Buffer
	buf.WriteString("mn")

	if opaque != "" {
		buf.WriteString(" O")
		buf.WriteString(opaque)
	}

	buf.WriteString("\r\n")
	return buf.Bytes()
}

func generateOpaque() string {
	bytes := make([]byte, 4)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}
