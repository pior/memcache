package protocol

import (
	"context"
	"errors"
)

// Command represents a memcache meta protocol command
type Command struct {
	Type     CmdType       // Command type: "mg", "ms", "md", etc.
	Key      string        // The key to operate on
	Value    []byte        // Value for set operations
	Flags    Flags         // Meta protocol flags
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

// WithValue sets the value for the command
func (c *Command) WithValue(value []byte) *Command {
	c.Value = value
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
