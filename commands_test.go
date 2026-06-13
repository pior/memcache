package memcache

import (
	"context"
	"strconv"
	"testing"
	"testing/synctest"
	"time"

	"github.com/pior/memcache/internal/testutils"
	"github.com/pior/memcache/meta"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTTL_Expiration(t *testing.T) {
	// ref is only a base for constructing absolute-time TTLs; their encoding
	// depends on the embedded time, not the current clock.
	ref := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		ttl  TTL
		want string
	}{
		{name: "NoTTL means no expiration", ttl: NoTTL, want: "0"},
		{name: "zero duration means no expiration", ttl: ExpiresIn(0), want: "0"},
		{name: "negative duration means no expiration", ttl: ExpiresIn(-time.Hour), want: "0"},
		{name: "sub-second rounds up to 1s", ttl: ExpiresIn(500 * time.Millisecond), want: "1"},
		{name: "1.5s rounds up to 2s", ttl: ExpiresIn(1500 * time.Millisecond), want: "2"},
		{name: "exact seconds unchanged", ttl: ExpiresIn(time.Hour), want: "3600"},
		{name: "30 days is still relative", ttl: ExpiresIn(30 * 24 * time.Hour), want: strconv.Itoa(30 * 24 * 3600)},
		{name: "absolute time becomes a unix timestamp", ttl: ExpiresAt(ref.Add(time.Hour)), want: strconv.FormatInt(ref.Add(time.Hour).Unix(), 10)},
		{name: "absolute time in the past stays absolute (expired)", ttl: ExpiresAt(ref.Add(-time.Hour)), want: strconv.FormatInt(ref.Add(-time.Hour).Unix(), 10)},
		{name: "absolute time near the epoch is clamped to the absolute range", ttl: ExpiresAt(time.Unix(60, 0)), want: strconv.FormatInt(minAbsoluteExptime, 10)},
		{name: "zero time means no expiration", ttl: ExpiresAt(time.Time{}), want: "0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := strconv.Itoa(tt.ttl.Expiration())
			assert.Equal(t, tt.want, got)
		})
	}

	// Relative durations beyond 30 days read the clock. synctest freezes it so
	// the two time.Now calls agree, no clock injection needed.
	t.Run("beyond 30 days becomes an absolute unix timestamp", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			want := strconv.FormatInt(time.Now().Unix()+31*24*3600, 10)
			got := strconv.Itoa(ExpiresIn(31 * 24 * time.Hour).Expiration())
			assert.Equal(t, want, got)
		})
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
	require.ErrorIs(t, err, ErrClientClosed)
}
