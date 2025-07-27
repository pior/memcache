package memcache

import (
	"fmt"
)

// commandToProtocol converts a Command to protocol bytes
func commandToProtocol(cmd *Command) []byte {
	switch cmd.Type {
	case "mg":
		flags := make([]string, 0, len(cmd.Flags))
		for flag, value := range cmd.Flags {
			if value == "" {
				flags = append(flags, flag)
			} else {
				flags = append(flags, flag+value)
			}
		}
		return formatGetCommand(cmd.Key, flags, cmd.Opaque)

	case "ms":
		return formatSetCommand(cmd.Key, cmd.Value, cmd.TTL, cmd.Flags, cmd.Opaque)

	case "md":
		return formatDeleteCommand(cmd.Key, cmd.Opaque)

	default:
		return nil
	}
}

// protocolToResponse converts a MetaResponse to Response
func protocolToResponse(metaResp *MetaResponse, originalKey string) *Response {
	resp := &Response{
		Status: metaResp.Status,
		Key:    originalKey,
		Value:  metaResp.Value,
		Flags:  metaResp.Flags,
		Opaque: metaResp.Opaque,
	}

	// Set error based on status
	switch metaResp.Status {
	case "EN":
		resp.Error = ErrCacheMiss
	case "HD":
		// Success, no error
	default:
		resp.Error = fmt.Errorf("memcache: unexpected status %s", metaResp.Status)
	}

	return resp
}
