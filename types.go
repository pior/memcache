package memcache

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// Item represents a memcache item
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
