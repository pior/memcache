package protocol

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
)

// Command represents a memcache meta protocol command
type Command struct {
	Type     string        // Command type: "mg", "ms", "md", etc.
	Key      string        // The key to operate on
	Value    []byte        // Value for set operations
	Flags    Flags         // Meta protocol flags
	Opaque   string        // Opaque identifier for matching responses
	response *Response     // Response for this command (set after execution)
	ready    chan struct{} // Channel to signal when response is available
}

func NewCommand(typ, key string) *Command {
	return &Command{
		Type:  typ,
		Key:   key,
		ready: make(chan struct{}),
	}
}

func (c *Command) Ready() chan struct{} {
	return c.ready
}

func (c *Command) SetValue(value []byte) *Command {
	c.Value = value
	return c
}

// GetResponse returns the response for this command, blocking until it's available
func (c *Command) GetResponse(ctx context.Context) (*Response, error) {
	// Check if context is already cancelled
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Wait for the response to be ready or context to be cancelled
	select {
	case <-c.ready:
		// Response is ready
		if c.response == nil {
			return nil, errors.New("memcache: response ready channel closed but no response available")
		}
		return c.response, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// SetResponse sets the response for this command (internal use only)
func (c *Command) SetResponse(response *Response) {
	c.response = response
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
	bytes := make([]byte, 4)
	rand.Read(bytes)
	c.Opaque = hex.EncodeToString(bytes)
}
