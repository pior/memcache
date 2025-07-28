package memcache

import (
	"context"
	"errors"
	"strconv"
	"time"
)

// Flag represents a meta protocol flag
type Flag struct {
	Type  string // Flag type: "v", "D", "M", etc.
	Value string // Flag value (empty for flags without values)
}

// Flags represents a collection of meta protocol flags
type Flags []Flag

// Set sets a flag value, updating existing flag or adding new one
func (f *Flags) Set(flagType, value string) {
	// Check if flag already exists and update it
	for i := range *f {
		if (*f)[i].Type == flagType {
			(*f)[i].Value = value
			return
		}
	}
	// Flag doesn't exist, append new one
	*f = append(*f, Flag{Type: flagType, Value: value})
}

// Get gets a flag value, returning the value and whether it exists
func (f Flags) Get(flagType string) (string, bool) {
	for _, flag := range f {
		if flag.Type == flagType {
			return flag.Value, true
		}
	}
	return "", false
}

// Command represents a memcache meta protocol command
type Command struct {
	Type     string        // Command type: "mg", "ms", "md", etc.
	Key      string        // The key to operate on
	Value    []byte        // Value for set operations
	Flags    Flags         // Meta protocol flags
	TTL      int           // Time to live in seconds
	response *Response     // Response for this command (set after execution)
	ready    chan struct{} // Channel to signal when response is available
}

// NewGetCommand creates a new get command
func NewGetCommand(key string) *Command {
	return &Command{
		Type:  CmdMetaGet,
		Key:   key,
		Flags: Flags{{Type: FlagValue, Value: ""}}, // Request value
		ready: make(chan struct{}),
	}
}

// NewSetCommand creates a new set command
func NewSetCommand(key string, value []byte, ttl time.Duration) *Command {
	cmd := &Command{
		Type:  CmdMetaSet,
		Key:   key,
		Value: value,
		Flags: Flags{},
		ready: make(chan struct{}),
	}
	if ttl > 0 {
		cmd.TTL = int(ttl.Seconds())
	}
	return cmd
}

// NewDeleteCommand creates a new delete command
func NewDeleteCommand(key string) *Command {
	return &Command{
		Type:  CmdMetaDelete,
		Key:   key,
		Flags: Flags{},
		ready: make(chan struct{}),
	}
}

// NewArithmeticCommand creates a new arithmetic command (increment/decrement)
func NewArithmeticCommand(key string, delta int64) *Command {
	cmd := &Command{
		Type:  CmdMetaArithmetic,
		Key:   key,
		Flags: Flags{{Type: FlagDelta, Value: strconv.FormatInt(delta, 10)}},
		ready: make(chan struct{}),
	}
	return cmd
}

// NewIncrementCommand creates a new increment command
func NewIncrementCommand(key string, delta int64) *Command {
	cmd := NewArithmeticCommand(key, delta)
	cmd.Flags.Set(FlagMode, ArithIncrement)
	return cmd
}

// NewDecrementCommand creates a new decrement command
func NewDecrementCommand(key string, delta int64) *Command {
	cmd := NewArithmeticCommand(key, delta)
	cmd.Flags.Set(FlagMode, ArithDecrement)
	return cmd
}

// NewDebugCommand creates a new debug command
func NewDebugCommand(key string) *Command {
	return &Command{
		Type:  CmdMetaDebug,
		Key:   key,
		Flags: Flags{},
		ready: make(chan struct{}),
	}
}

// NewNoOpCommand creates a new no-op command
func NewNoOpCommand() *Command {
	return &Command{
		Type:  CmdMetaNoOp,
		Flags: Flags{},
		ready: make(chan struct{}),
	}
}

// SetFlag sets a flag for the command
func (c *Command) SetFlag(flagType, value string) {
	c.Flags.Set(flagType, value)
}

// GetFlag gets a flag value for the command
func (c *Command) GetFlag(flagType string) (string, bool) {
	return c.Flags.Get(flagType)
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

// setResponse sets the response for this command (internal use only)
func (c *Command) setResponse(response *Response) {
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

// Response represents a memcache meta protocol response
type Response struct {
	Status string // Response status: "HD", "VA", "EN", etc.
	Key    string // The key this response is for
	Value  []byte // Value returned (for get operations)
	Flags  Flags  // Meta protocol flags from response
	Error  error  // Any error that occurred
}

// SetFlag sets a flag for the response
func (r *Response) SetFlag(flagType, value string) {
	r.Flags.Set(flagType, value)
}

// GetFlag gets a flag value for the response
func (r *Response) GetFlag(flagType string) (string, bool) {
	return r.Flags.Get(flagType)
}
