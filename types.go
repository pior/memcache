package memcache

import (
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
		Type:  "mg",
		Key:   key,
		Flags: map[string]string{"v": ""}, // Request value
	}
}

// NewSetCommand creates a new set command
func NewSetCommand(key string, value []byte, ttl time.Duration) *Command {
	cmd := &Command{
		Type:  "ms",
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
		Type:  "md",
		Key:   key,
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
