package memcache

import (
	"strconv"
	"time"
)

// Command represents a memcache meta protocol command
type Command struct {
	Type  string            // Command type: "mg", "ms", "md", etc.
	Key   string            // The key to operate on
	Value []byte            // Value for set operations
	Flags map[string]string // Meta protocol flags
	TTL   int               // Time to live in seconds
}

// NewGetCommand creates a new get command
func NewGetCommand(key string) *Command {
	return &Command{
		Type:  CmdMetaGet,
		Key:   key,
		Flags: map[string]string{FlagValue: ""}, // Request value
	}
}

// NewSetCommand creates a new set command
func NewSetCommand(key string, value []byte, ttl time.Duration) *Command {
	cmd := &Command{
		Type:  CmdMetaSet,
		Key:   key,
		Value: value,
		Flags: make(map[string]string),
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
		Flags: make(map[string]string),
	}
}

// NewArithmeticCommand creates a new arithmetic command (increment/decrement)
func NewArithmeticCommand(key string, delta int64) *Command {
	cmd := &Command{
		Type:  CmdMetaArithmetic,
		Key:   key,
		Flags: map[string]string{FlagDelta: strconv.FormatInt(delta, 10)},
	}
	return cmd
}

// NewIncrementCommand creates a new increment command
func NewIncrementCommand(key string, delta int64) *Command {
	cmd := NewArithmeticCommand(key, delta)
	cmd.SetFlag(FlagMode, ArithIncrement)
	return cmd
}

// NewDecrementCommand creates a new decrement command
func NewDecrementCommand(key string, delta int64) *Command {
	cmd := NewArithmeticCommand(key, delta)
	cmd.SetFlag(FlagMode, ArithDecrement)
	return cmd
}

// NewDebugCommand creates a new debug command
func NewDebugCommand(key string) *Command {
	return &Command{
		Type:  CmdMetaDebug,
		Key:   key,
		Flags: make(map[string]string),
	}
}

// NewNoOpCommand creates a new no-op command
func NewNoOpCommand() *Command {
	return &Command{
		Type:  CmdMetaNoOp,
		Flags: make(map[string]string),
	}
}

// SetFlag sets a flag for the command
func (c *Command) SetFlag(flag, value string) {
	if c.Flags == nil {
		c.Flags = make(map[string]string)
	}
	c.Flags[flag] = value
}

// GetFlag gets a flag value for the command
func (c *Command) GetFlag(flag string) (string, bool) {
	if c.Flags == nil {
		return "", false
	}
	value, exists := c.Flags[flag]
	return value, exists
}

// Response represents a memcache meta protocol response
type Response struct {
	Status string            // Response status: "HD", "VA", "EN", etc.
	Key    string            // The key this response is for
	Value  []byte            // Value returned (for get operations)
	Flags  map[string]string // Meta protocol flags from response
	Error  error             // Any error that occurred
}

// SetFlag sets a flag for the response
func (r *Response) SetFlag(flag, value string) {
	if r.Flags == nil {
		r.Flags = make(map[string]string)
	}
	r.Flags[flag] = value
}

// GetFlag gets a flag value for the response
func (r *Response) GetFlag(flag string) (string, bool) {
	if r.Flags == nil {
		return "", false
	}
	value, exists := r.Flags[flag]
	return value, exists
}
