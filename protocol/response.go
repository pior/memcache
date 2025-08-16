package protocol

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log/slog"
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

	if resp.Status == StatusME {
		resp.Value = []byte(line)
		return resp, nil
	}

	if resp.Status == StatusVA {
		if len(parts) < 2 {
			slog.Error("memcache: invalid VA response: not enough parts", "line", line)
			return nil, ErrInvalidResponse
		}

		resp.Flags = readFlags(parts[2:])

		valueSize, err := strconv.Atoi(parts[1])
		if err != nil {
			slog.Error("memcache: invalid VA response: size is not a number", "part", parts[1], "error", err)
			return nil, ErrInvalidResponse
		}

		resp.Value = make([]byte, valueSize)

		if _, err := io.ReadFull(reader, resp.Value); err != nil {
			slog.Error("memcache: failed to read value for VA response", "line", line, "error", err)
			return nil, err
		}

		reader.ReadString('\n') // Read trailing \r\n
	} else {
		resp.Flags = readFlags(parts[1:])
	}

	if value, found := resp.Flags.Get(FlagOpaque); found {
		resp.Opaque = value
	}

	// Set error based on status using constants
	switch resp.Status {
	case StatusHD, StatusVA, StatusMN, StatusME:
		// Success, no error
		// HD = Hit/stored, VA = Value follows, MN = Meta no-op, ME = Meta debug
	case StatusEN, StatusNF:
		resp.Error = ErrCacheMiss
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

func readFlags(parts []string) Flags {
	flags := Flags{}
	for _, part := range parts {
		if len(part) > 1 {
			flags.Set(FlagType(part[0]), part[1:])
		} else {
			flags.Set(FlagType(part), "")
		}
	}
	return flags
}
