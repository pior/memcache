package memcache

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

// A zero Status must not read as a successful operation: a forgotten
// assignment would otherwise report StatusApplied.
func TestStatus_ZeroValueIsNotApplied(t *testing.T) {
	var s Status
	assert.False(t, s.OK())
	assert.Equal(t, "Status(0)", s.String())
	assert.False(t, Item{}.Status.OK())
	assert.False(t, StoreResult{}.Status.OK())
	assert.False(t, Counter{}.Status.OK())
}

// OK is the one "did it take effect" question in the API, and only
// StatusApplied answers yes: a key that exists but failed a precondition does
// not.
func TestStatus_OK(t *testing.T) {
	tests := []struct {
		status Status
		want   string
		ok     bool
	}{
		{status: StatusApplied, want: "Applied", ok: true},
		{status: StatusNotFound, want: "NotFound"},
		{status: StatusExists, want: "Exists"},
		{status: StatusCASMismatch, want: "CASMismatch"},
		{status: Status(99), want: "Status(99)"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.status.String())
			assert.Equal(t, tt.ok, tt.status.OK())
		})
	}
}

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
