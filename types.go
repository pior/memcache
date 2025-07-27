package memcache

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// Command represents a memcache meta protocol command
type Command struct {
	Type   string            // Command type: "mg", "ms", "md", etc.
	Key    string            // The key to operate on
	Value  []byte            // Value for set operations
	Flags  map[string]string // Meta protocol flags
	TTL    int               // Time to live in seconds
	Opaque string            // Opaque value for request tracking
}

// Response represents a memcache meta protocol response
type Response struct {
	Status string            // Response status: "HD", "VA", "EN", etc.
	Key    string            // The key this response is for
	Value  []byte            // Value returned (for get operations)
	Flags  map[string]string // Meta protocol flags from response
	Opaque string            // Opaque value from request
	Error  error             // Any error that occurred
}

// NewGetCommand creates a new get command
func NewGetCommand(key string) *Command {
	return &Command{
		Type:   "mg",
		Key:    key,
		Flags:  map[string]string{"v": ""}, // Request value
		Opaque: GenerateOpaque(),
	}
}

// NewSetCommand creates a new set command
func NewSetCommand(key string, value []byte, ttl time.Duration) *Command {
	cmd := &Command{
		Type:   "ms",
		Key:    key,
		Value:  value,
		Flags:  make(map[string]string),
		Opaque: GenerateOpaque(),
	}
	if ttl > 0 {
		cmd.TTL = int(ttl.Seconds())
	}
	return cmd
}

// NewDeleteCommand creates a new delete command
func NewDeleteCommand(key string) *Command {
	return &Command{
		Type:   "md",
		Key:    key,
		Flags:  make(map[string]string),
		Opaque: GenerateOpaque(),
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

// Item represents a memcache item (convenience type)
type Item struct {
	Key        string
	Value      []byte
	Flags      map[string]string
	Expiration int // TTL in seconds
}

// NewItem creates a new item with the given key and value
func NewItem(key string, value []byte) *Item {
	return &Item{
		Key:   key,
		Value: value,
		Flags: make(map[string]string),
	}
}

// SetTTL sets the time-to-live for the item
func (i *Item) SetTTL(ttl time.Duration) {
	i.Expiration = int(ttl.Seconds())
}

// SetFlag sets a flag for the item
func (i *Item) SetFlag(flag, value string) {
	if i.Flags == nil {
		i.Flags = make(map[string]string)
	}
	i.Flags[flag] = value
}

// GetFlag gets a flag value for the item
func (i *Item) GetFlag(flag string) (string, bool) {
	if i.Flags == nil {
		return "", false
	}
	value, exists := i.Flags[flag]
	return value, exists
}

// GenerateOpaque generates a random opaque value for request tracking
func GenerateOpaque() string {
	bytes := make([]byte, 4)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}
