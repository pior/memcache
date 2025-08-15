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
	var buf bytes.Buffer

	buf.WriteString(cmd.Type)

	if cmd.Type != CmdMetaNoOp {
		buf.WriteByte(' ')
		buf.WriteString(cmd.Key)
	}

	if cmd.Type == CmdMetaSet {
		buf.WriteByte(' ')
		buf.WriteString(strconv.Itoa(len(cmd.Value)))
	}

	// write flags after sorting them
	flags := cmd.Flags
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

	if cmd.Opaque != "" {
		buf.WriteString(" O")
		buf.WriteString(cmd.Opaque)
	}

	if cmd.Type == CmdMetaSet {
		buf.WriteString("\r\n")
		buf.Write(cmd.Value)
	}

	buf.WriteString("\r\n")
	return buf.Bytes()
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
