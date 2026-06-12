package memcache

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/pior/memcache/internal/testutils"
	"github.com/pior/memcache/meta"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTTLSeconds(t *testing.T) {
	tests := []struct {
		name string
		ttl  time.Duration
		want string
	}{
		{name: "zero means no expiration", ttl: 0, want: "0"},
		{name: "negative means no expiration", ttl: -time.Hour, want: "0"},
		{name: "sub-second rounds up to 1s", ttl: 500 * time.Millisecond, want: "1"},
		{name: "1.5s rounds up to 2s", ttl: 1500 * time.Millisecond, want: "2"},
		{name: "exact seconds unchanged", ttl: time.Hour, want: "3600"},
		{name: "30 days is still relative", ttl: 30 * 24 * time.Hour, want: strconv.Itoa(30 * 24 * 3600)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := strconv.Itoa(ttlSeconds(tt.ttl))
			assert.Equal(t, tt.want, got)
		})
	}

	t.Run("beyond 30 days becomes an absolute unix timestamp", func(t *testing.T) {
		ttl := 31 * 24 * time.Hour
		got := ttlSeconds(ttl)
		want := time.Now().Add(ttl).Unix()
		// Allow a small window for clock movement between the two time.Now calls.
		assert.InDelta(t, want, got, 2)
	})
}

func TestClient_ExecuteBatch_RejectsQuietFlag(t *testing.T) {
	mockConn := testutils.NewConnectionMock()
	client := newTestClient(t, mockConn)

	reqs := []*meta.Request{
		meta.NewRequest(meta.CmdGet, "key1", nil).AddReturnValue().AddQuiet(),
	}
	resps, err := client.ExecuteBatch(context.Background(), reqs)

	require.ErrorContains(t, err, "quiet flag is not supported")
	assert.Nil(t, resps)
	assert.Empty(t, mockConn.GetWrittenRequest(), "nothing must be written for a rejected batch")
}

func TestClient_OperationsAfterClose(t *testing.T) {
	mockConn := testutils.NewConnectionMock()
	client := newTestClient(t, mockConn)

	client.Close()
	client.Close() // must not panic

	_, err := client.Get(context.Background(), "key")
	require.ErrorContains(t, err, "client is closed")
}
