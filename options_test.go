package memcache

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Passing more than one options value is a programming error and must be
// loud rather than silently dropping the extra values.
func TestCommands_MoreThanOneOptionsPanics(t *testing.T) {
	client := NewClient(StaticServers("localhost:11211"), Config{})
	t.Cleanup(client.Close)
	c := NewCommands(client)
	assert.PanicsWithValue(t, "memcache: at most one memcache.StoreOptions may be passed, got 2", func() {
		_, _ = c.Set(context.Background(), "k", []byte("v"), StoreOptions{}, StoreOptions{})
	})
}
