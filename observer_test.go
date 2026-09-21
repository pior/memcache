package memcache

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/pior/memcache/internal/testutils"
	"github.com/pior/memcache/meta"
	"github.com/stretchr/testify/require"
)

// opName must name the operation, not the command carrying it: mg is a get or
// a touch, ms is one of five stores, ma is an increment or a decrement.
func TestOpName(t *testing.T) {
	tests := []struct {
		name string
		req  *meta.Request
		want string
	}{
		{name: "get", req: meta.NewRequest(meta.CmdGet, "k", nil).AddReturnValue(), want: OpGet},
		{name: "touch is an mg without a value", req: meta.NewRequest(meta.CmdGet, "k", nil).AddTTL(60), want: OpTouch},
		{name: "set", req: meta.NewRequest(meta.CmdSet, "k", nil), want: OpSet},
		{name: "explicit set mode", req: meta.NewRequest(meta.CmdSet, "k", nil).AddModeSet(), want: OpSet},
		{name: "add", req: meta.NewRequest(meta.CmdSet, "k", nil).AddModeAdd(), want: OpAdd},
		{name: "replace", req: meta.NewRequest(meta.CmdSet, "k", nil).AddModeReplace(), want: OpReplace},
		{name: "append", req: meta.NewRequest(meta.CmdSet, "k", nil).AddModeAppend(), want: OpAppend},
		{name: "prepend", req: meta.NewRequest(meta.CmdSet, "k", nil).AddModePrepend(), want: OpPrepend},
		{name: "delete", req: meta.NewRequest(meta.CmdDelete, "k", nil), want: OpDelete},
		{name: "increment", req: meta.NewRequest(meta.CmdArithmetic, "k", nil), want: OpIncrement},
		{name: "explicit increment mode", req: meta.NewRequest(meta.CmdArithmetic, "k", nil).AddModeIncrement(), want: OpIncrement},
		{name: "decrement", req: meta.NewRequest(meta.CmdArithmetic, "k", nil).AddModeDecrement(), want: OpDecrement},
		{name: "decrement, alternate spelling", req: meta.NewRequest(meta.CmdArithmetic, "k", nil).AddMode(meta.ModeDecrementAlt), want: OpDecrement},
		{name: "debug", req: meta.NewRequest(meta.CmdDebug, "k", nil), want: OpDebug},
		{name: "flush_all", req: meta.NewRequest(meta.CmdFlushAll, "", nil), want: OpFlushAll},
		{name: "stats", req: meta.NewRequest(meta.CmdStats, "", nil), want: OpStats},
		{name: "a command the package does not model keeps its code", req: meta.NewRequest(meta.CmdNoOp, "", nil), want: "mn"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, opName(tt.req))
		})
	}
}

// A reply the operation does not model, an error, or no response at all leaves
// the observer without an outcome to report.
func TestStatusOf_NoOutcome(t *testing.T) {
	require.Equal(t, "Status(0)", statusOf(OpGet, meta.StatusVA, errors.New("boom")).String())
	require.Equal(t, "Status(0)", statusOf(OpGet, "", nil).String())
	require.Equal(t, "Status(0)", statusOf(OpSet, meta.StatusNS, nil).String(), "a plain set has no NS outcome")
	require.Equal(t, "Status(0)", statusOf(OpIncrement, meta.StatusNS, nil).String())
	require.Equal(t, "Status(0)", statusOf(OpGet, meta.StatusMN, nil).String())
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

// Every operation reports its own name and the outcome the caller sees. A
// command code could not name it (mg is both get and touch, ms is all five
// stores) and a raw status code could not carry it (the same NS is an add's
// StatusExists and a replace's StatusNotFound).
func TestClient_Observer_SingleOp(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name     string
		response string
		op       func(c *Client) (Status, error)
		wantOp   string
		wantCode string
		want     string
	}{
		{
			name: "get hit", response: "VA 5\r\nhello\r\n",
			op:     func(c *Client) (Status, error) { i, err := c.Get(ctx, "testkey"); return i.Status, err },
			wantOp: OpGet, wantCode: "VA", want: "Applied",
		},
		{
			name: "get miss", response: "EN\r\n",
			op:     func(c *Client) (Status, error) { i, err := c.Get(ctx, "testkey"); return i.Status, err },
			wantOp: OpGet, wantCode: "EN", want: "NotFound",
		},
		{
			name: "touch hit", response: "HD\r\n",
			op:     func(c *Client) (Status, error) { return c.Touch(ctx, "testkey", ExpiresIn(time.Minute)) },
			wantOp: OpTouch, wantCode: "HD", want: "Applied",
		},
		{
			name: "touch miss", response: "EN\r\n",
			op:     func(c *Client) (Status, error) { return c.Touch(ctx, "testkey", ExpiresIn(time.Minute)) },
			wantOp: OpTouch, wantCode: "EN", want: "NotFound",
		},
		{
			name: "set stored", response: "HD\r\n",
			op:     func(c *Client) (Status, error) { r, err := c.Set(ctx, "testkey", []byte("v")); return r.Status, err },
			wantOp: OpSet, wantCode: "HD", want: "Applied",
		},
		{
			name: "set cas mismatch", response: "EX\r\n",
			op: func(c *Client) (Status, error) {
				r, err := c.Set(ctx, "testkey", []byte("v"), StoreOptions{CAS: 7})
				return r.Status, err
			},
			wantOp: OpSet, wantCode: "EX", want: "CASMismatch",
		},
		{
			name: "add on an existing key", response: "NS\r\n",
			op:     func(c *Client) (Status, error) { r, err := c.Add(ctx, "testkey", []byte("v")); return r.Status, err },
			wantOp: OpAdd, wantCode: "NS", want: "Exists",
		},
		{
			name: "replace on a missing key", response: "NS\r\n",
			op: func(c *Client) (Status, error) {
				r, err := c.Replace(ctx, "testkey", []byte("v"))
				return r.Status, err
			},
			wantOp: OpReplace, wantCode: "NS", want: "NotFound",
		},
		{
			name: "append on a missing key", response: "NS\r\n",
			op:     func(c *Client) (Status, error) { r, err := c.Append(ctx, "testkey", []byte("v")); return r.Status, err },
			wantOp: OpAppend, wantCode: "NS", want: "NotFound",
		},
		{
			name: "prepend on a missing key", response: "NS\r\n",
			op: func(c *Client) (Status, error) {
				r, err := c.Prepend(ctx, "testkey", []byte("v"))
				return r.Status, err
			},
			wantOp: OpPrepend, wantCode: "NS", want: "NotFound",
		},
		{
			name: "delete", response: "HD\r\n",
			op:     func(c *Client) (Status, error) { return c.Delete(ctx, "testkey") },
			wantOp: OpDelete, wantCode: "HD", want: "Applied",
		},
		{
			name: "delete a missing key", response: "NF\r\n",
			op:     func(c *Client) (Status, error) { return c.Delete(ctx, "testkey") },
			wantOp: OpDelete, wantCode: "NF", want: "NotFound",
		},
		{
			name: "increment", response: "VA 1\r\n5\r\n",
			op:     func(c *Client) (Status, error) { n, err := c.Increment(ctx, "testkey", 1); return n.Status, err },
			wantOp: OpIncrement, wantCode: "VA", want: "Applied",
		},
		{
			name: "increment a missing key", response: "NF\r\n",
			op:     func(c *Client) (Status, error) { n, err := c.Increment(ctx, "testkey", 1); return n.Status, err },
			wantOp: OpIncrement, wantCode: "NF", want: "NotFound",
		},
		{
			name: "decrement", response: "VA 1\r\n4\r\n",
			op:     func(c *Client) (Status, error) { n, err := c.Decrement(ctx, "testkey", 1); return n.Status, err },
			wantOp: OpDecrement, wantCode: "VA", want: "Applied",
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

			callerStatus, err := tc.op(client)
			require.NoError(t, err)

			require.Len(t, obs.infos, 1)
			require.Equal(t, tc.wantOp, obs.infos[0].Op)
			require.Equal(t, "localhost:11211", obs.infos[0].Address)
			require.Equal(t, "testkey", obs.infos[0].Key)

			require.Len(t, obs.results, 1, "completion must be called exactly once")
			require.Equal(t, tc.want, obs.results[0].Status.String())
			require.Equal(t, tc.want, callerStatus.String(), "the observer must report what the caller sees")
			require.Equal(t, tc.wantCode, obs.results[0].Code)
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
	require.Equal(t, "Status(0)", obs.results[0].Status.String(), "a failed operation has no outcome")
	require.Empty(t, obs.results[0].Code)
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
	require.Equal(t, OpInfo{Op: OpFlushAll, Address: "localhost:11211"}, obs.infos[0])
	require.Len(t, obs.results, 1)
	require.NoError(t, obs.results[0].Err)
}
