package protocol

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
)

// Command represents a memcache meta protocol command
type Command struct {
	Type     CmdType       // Command type: "mg", "ms", "md", etc.
	Key      string        // The key to operate on
	Value    []byte        // Value for set operations
	Flags    Flags         // Meta protocol flags
	Opaque   string        // Opaque identifier for matching responses. This is a string, up to 32 bytes in length.
	Response *Response     // Response for this command (set after execution)
	ready    chan struct{} // Channel to signal when response is available
}

func NewCommand(typ CmdType, key string) *Command {
	return &Command{
		Type:  typ,
		Key:   key,
		ready: make(chan struct{}),
	}
}

// WithFlag sets a flag
func (c *Command) WithFlag(flag FlagType, value string) *Command {
	c.Flags.Set(flag, value)
	return c
}

// GetResponse returns the response for this command, blocking until it's available
func (c *Command) Wait(ctx context.Context) error {
	if c == nil {
		return nil
	}
	select {
	case <-c.ready:
		if c.Response == nil {
			return errors.New("memcache: response ready channel closed but no response available")
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// SetResponse sets the response for this command (internal use only)
func (c *Command) SetResponse(response *Response) {
	c.Response = response
	// Signal that the response is ready (close the channel)
	// Use select with default to avoid panic if already closed
	select {
	case <-c.ready:
		// Channel already closed, do nothing
	default:
		close(c.ready)
	}
}

func SetRandomOpaque(c *Command) {
	if c.Opaque != "" {
		return // Opaque already set
	}
	bytes := make([]byte, 4)
	rand.Read(bytes)
	c.Opaque = hex.EncodeToString(bytes)
}
