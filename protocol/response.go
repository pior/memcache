package protocol

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

var ErrInvalidResponse = errors.New("memcache: invalid response")

type Response struct {
	Status string // Response status: "HD", "VA", "EN", etc.
	Value  []byte // Value returned (for get operations)
	Flags  Flags  // Meta protocol flags from response
	Opaque string // Opaque identifier for matching commands. This is a string, up to 32 bytes in length.

	Error error // Any error that occurred
}

func ReadResponse(reader *bufio.Reader) (*Response, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}

	line = strings.TrimRight(line, "\r\n")

	if line == "" {
		return nil, ErrInvalidResponse
	}

	parts := strings.Split(line, " ")
	if len(parts) < 1 {
		return nil, ErrInvalidResponse
	}

	resp := &Response{
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

	// Set error based on status using constants
	switch resp.Status {
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
		resp.Error = fmt.Errorf("memcache: unexpected status %s", resp.Status)
	}

	return resp, nil
}
