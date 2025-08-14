package protocol

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

var (
	ErrInvalidResponse = errors.New("memcache: invalid response")
)

type MetaResponse struct {
	Status string
	Flags  Flags
	Value  []byte
	Opaque string
}

func ReadResponse(reader *bufio.Reader) (*MetaResponse, error) {
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
		Flags:  Flags{},
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
				resp.Flags.Set("s", part[1:])
			} else {
				resp.Flags.Set("s", "")
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
				resp.Flags.Set(kv[0], kv[1])
			} else {
				resp.Flags.Set(part, "")
			}
		}
	}

	return resp, nil
}

func CommandToProtocol(cmd *Command) []byte {
	switch cmd.Type {
	case CmdMetaGet:
		flags := make([]string, 0, len(cmd.Flags))
		for _, flag := range cmd.Flags {
			if flag.Value == "" {
				flags = append(flags, flag.Type)
			} else {
				flags = append(flags, flag.Type+flag.Value)
			}
		}
		return formatGetCommand(cmd.Key, flags, cmd.Opaque)

	case CmdMetaSet:
		return formatSetCommand(cmd.Key, cmd.Value, cmd.TTL, cmd.Flags, cmd.Opaque)

	case CmdMetaDelete:
		return formatDeleteCommand(cmd.Key, cmd.Opaque)

	case CmdMetaArithmetic:
		return formatArithmeticCommand(cmd.Key, cmd.Flags, cmd.Opaque)

	case CmdMetaDebug:
		return formatDebugCommand(cmd.Key, cmd.Flags, cmd.Opaque)

	case CmdMetaNoOp:
		return formatNoOpCommand(cmd.Opaque)

	default:
		return nil
	}
}

func ProtocolToResponse(metaResp *MetaResponse, originalKey string) *Response {
	resp := &Response{
		Status: metaResp.Status,
		Key:    originalKey,
		Value:  metaResp.Value,
		Flags:  metaResp.Flags, // Direct assignment since both are Flags type now
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
	if !IsValidKey(key) {
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
func formatSetCommand(key string, value []byte, ttl int, flags []Flag, opaque string) []byte {
	if !IsValidKey(key) {
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

	// Sort flags for consistent output
	sort.Slice(flags, func(i, j int) bool {
		return flags[i].Type < flags[j].Type
	})

	for _, flag := range flags {
		buf.WriteByte(' ')
		buf.WriteString(flag.Type)
		if flag.Value != "" {
			buf.WriteString(flag.Value)
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
	if !IsValidKey(key) {
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
func formatArithmeticCommand(key string, flags []Flag, opaque string) []byte {
	if !IsValidKey(key) {
		return nil
	}

	var buf bytes.Buffer
	buf.WriteString("ma ")
	buf.WriteString(key)

	// Sort flags for consistent output
	sort.Slice(flags, func(i, j int) bool {
		return flags[i].Type < flags[j].Type
	})

	for _, flag := range flags {
		buf.WriteByte(' ')
		buf.WriteString(flag.Type)
		if flag.Value != "" {
			buf.WriteString(flag.Value)
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
func formatDebugCommand(key string, flags []Flag, opaque string) []byte {
	var buf bytes.Buffer
	buf.WriteString("me")

	if key != "" {
		buf.WriteByte(' ')
		buf.WriteString(key)
	}

	// Sort flags for consistent output
	sort.Slice(flags, func(i, j int) bool {
		return flags[i].Type < flags[j].Type
	})

	for _, flag := range flags {
		buf.WriteByte(' ')
		buf.WriteString(flag.Type)
		if flag.Value != "" {
			buf.WriteString(flag.Value)
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
