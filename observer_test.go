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

func TestResultOf(t *testing.T) {
	require.Equal(t, ResultHit, resultOf(meta.CmdGet, meta.StatusVA, nil))
	require.Equal(t, ResultHit, resultOf(meta.CmdGet, meta.StatusHD, nil))
	require.Equal(t, ResultMiss, resultOf(meta.CmdGet, meta.StatusEN, nil))
	require.Equal(t, ResultHit, resultOf(meta.CmdDelete, meta.StatusHD, nil))
	require.Equal(t, ResultMiss, resultOf(meta.CmdDelete, meta.StatusNF, nil))
	require.Equal(t, ResultStored, resultOf(meta.CmdSet, meta.StatusHD, nil))
	require.Equal(t, ResultNotStored, resultOf(meta.CmdSet, meta.StatusNS, nil))
	require.Equal(t, ResultStored, resultOf(meta.CmdArithmetic, meta.StatusVA, nil))

	// Errors and missing responses (empty status) are always Unknown,
	// regardless of command.
	require.Equal(t, ResultUnknown, resultOf(meta.CmdGet, meta.StatusVA, errors.New("boom")))
	require.Equal(t, ResultUnknown, resultOf(meta.CmdGet, "", nil))
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
		wantStatus string
	}{
		{
			name:     "get hit",
			response: "VA 5\r\nhello\r\n",
			op:       func(c *Client) error { _, err := c.Get(context.Background(), "testkey"); return err },
			wantOp:   "mg", wantResult: ResultHit, wantStatus: "VA",
		},
		{
			name:     "get miss",
			response: "EN\r\n",
			op:       func(c *Client) error { _, err := c.Get(context.Background(), "testkey"); return err },
			wantOp:   "mg", wantResult: ResultMiss, wantStatus: "EN",
		},
		{
			name:     "set stored",
			response: "HD\r\n",
			op:       func(c *Client) error { return c.Set(context.Background(), Item{Key: "testkey", Value: []byte("v")}) },
			wantOp:   "ms", wantResult: ResultStored, wantStatus: "HD",
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
			require.Equal(t, tc.wantStatus, obs.results[0].Status)
			require.NoError(t, obs.results[0].Err)
		})
	}
}

func TestClient_Observer_ProtocolError(t *testing.T) {
	obs := &recordingObserver{}
	client := NewClient(StaticServers("localhost:11211"), Config{
		Dialer:   &mockDialer{conn: testutils.NewConnectionMock("SERVER_ERROR unavailable\r\n")},
		Observer: obs,
	})
	t.Cleanup(client.Close)

	_, err := client.Get(context.Background(), "k")
	require.Error(t, err)
	require.Len(t, obs.results, 1)
	require.Equal(t, err, obs.results[0].Err)
}

func TestClient_Observer_BatchProtocolError(t *testing.T) {
	obs := &recordingObserver{}
	client := NewClient(StaticServers("localhost:11211"), Config{
		Dialer: &mockDialer{conn: testutils.NewConnectionMock(
			"SERVER_ERROR unavailable\r\n",
			"MN\r\n",
		)},
		Observer: obs,
	})
	t.Cleanup(client.Close)

	responses, err := client.ExecuteBatch(context.Background(), []*meta.Request{
		meta.NewRequest(meta.CmdGet, "k", nil),
	})
	require.NoError(t, err)
	require.Error(t, responses[0].Error)
	require.Len(t, obs.results, 1)
	require.Equal(t, responses[0].Error, obs.results[0].Err)
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

func TestClient_Observer_FlushAll(t *testing.T) {
	obs := &recordingObserver{}
	client := NewClient(StaticServers("localhost:11211"), Config{
		Dialer:   &mockDialer{conn: testutils.NewConnectionMock("OK\r\n")},
		Observer: obs,
	})
	t.Cleanup(client.Close)

	require.NoError(t, client.FlushAll(context.Background()))

	require.Len(t, obs.infos, 1)
	require.Equal(t, OpInfo{Op: OpFlushAll, Server: "localhost:11211"}, obs.infos[0])
	require.Len(t, obs.results, 1)
	require.NoError(t, obs.results[0].Err)
}
