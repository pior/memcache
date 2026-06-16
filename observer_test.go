package memcache

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/pior/memcache/internal/testutils"
	"github.com/pior/memcache/meta"
	"github.com/stretchr/testify/require"
)

func TestOpName(t *testing.T) {
	for cmd, want := range map[meta.CmdType]string{
		meta.CmdGet:        "get",
		meta.CmdSet:        "set",
		meta.CmdDelete:     "delete",
		meta.CmdArithmetic: "increment",
		meta.CmdStats:      "stats",
	} {
		require.Equal(t, want, opName(cmd))
	}
}

func TestResultOf(t *testing.T) {
	resp := func(s meta.StatusType) *meta.Response { return &meta.Response{Status: s} }

	require.Equal(t, ResultHit, resultOf(meta.CmdGet, resp(meta.StatusVA), nil))
	require.Equal(t, ResultHit, resultOf(meta.CmdGet, resp(meta.StatusHD), nil))
	require.Equal(t, ResultMiss, resultOf(meta.CmdGet, resp(meta.StatusEN), nil))
	require.Equal(t, ResultHit, resultOf(meta.CmdDelete, resp(meta.StatusHD), nil))
	require.Equal(t, ResultMiss, resultOf(meta.CmdDelete, resp(meta.StatusNF), nil))
	require.Equal(t, ResultStored, resultOf(meta.CmdSet, resp(meta.StatusHD), nil))
	require.Equal(t, ResultNotStored, resultOf(meta.CmdSet, resp(meta.StatusNS), nil))
	require.Equal(t, ResultStored, resultOf(meta.CmdArithmetic, resp(meta.StatusVA), nil))

	// Errors and nil responses are always Unknown, regardless of command.
	require.Equal(t, ResultUnknown, resultOf(meta.CmdGet, resp(meta.StatusVA), errors.New("boom")))
	require.Equal(t, ResultUnknown, resultOf(meta.CmdGet, nil, nil))
}

type recordingObserver struct {
	mu      sync.Mutex
	infos   []OpInfo
	results []OpResult
}

func (o *recordingObserver) StartOp(ctx context.Context, info OpInfo) (context.Context, ActiveOp) {
	o.mu.Lock()
	o.infos = append(o.infos, info)
	o.mu.Unlock()
	return ctx, &recordingOp{obs: o}
}

type recordingOp struct{ obs *recordingObserver }

func (op *recordingOp) End(r OpResult) {
	op.obs.mu.Lock()
	op.obs.results = append(op.obs.results, r)
	op.obs.mu.Unlock()
}

func TestClient_Observer_SingleOp(t *testing.T) {
	cases := []struct {
		name       string
		response   string
		op         func(c *Client) error
		wantOp     string
		wantResult Result
	}{
		{
			name:     "get hit",
			response: "VA 5\r\nhello\r\n",
			op:       func(c *Client) error { _, err := c.Get(context.Background(), "testkey"); return err },
			wantOp:   "get", wantResult: ResultHit,
		},
		{
			name:     "get miss",
			response: "EN\r\n",
			op:       func(c *Client) error { _, err := c.Get(context.Background(), "testkey"); return err },
			wantOp:   "get", wantResult: ResultMiss,
		},
		{
			name:     "set stored",
			response: "HD\r\n",
			op:       func(c *Client) error { return c.Set(context.Background(), Item{Key: "testkey", Value: []byte("v")}) },
			wantOp:   "set", wantResult: ResultStored,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			obs := &recordingObserver{}
			client := NewClient(StaticServers("localhost:11211"), Config{
				Dialer:   &mockDialer{conn: testutils.NewConnectionMock(tc.response)},
				Observer: obs,
			})
			t.Cleanup(client.Close)

			require.NoError(t, tc.op(client))

			require.Len(t, obs.infos, 1)
			require.Equal(t, tc.wantOp, obs.infos[0].Op)
			require.Equal(t, "localhost:11211", obs.infos[0].Server)
			require.Equal(t, "testkey", obs.infos[0].Key)

			require.Len(t, obs.results, 1, "completion must be called exactly once")
			require.Equal(t, tc.wantResult, obs.results[0].Result)
			require.NoError(t, obs.results[0].Err)
		})
	}
}

func TestClient_Observer_CompletesOnError(t *testing.T) {
	obs := &recordingObserver{}
	client := NewClient(StaticServers("localhost:11211"), Config{
		Dialer:   &mockDialer{error: errors.New("dial failed")},
		Observer: obs,
	})
	t.Cleanup(client.Close)

	_, err := client.Get(context.Background(), "k")
	require.Error(t, err)

	require.Len(t, obs.results, 1, "completion must fire even when the op fails")
	require.Error(t, obs.results[0].Err)
	require.Equal(t, ResultUnknown, obs.results[0].Result)
}
