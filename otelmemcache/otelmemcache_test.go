package otelmemcache_test

import (
	"context"
	"errors"
	"testing"

	"github.com/pior/memcache"
	"github.com/pior/memcache/otelmemcache"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/trace"
)

func newRecorder(opts ...otelmemcache.Option) (memcache.Observer, *tracetest.SpanRecorder) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	return otelmemcache.New(tp, opts...), sr
}

func attrMap(s sdktrace.ReadOnlySpan) map[attribute.Key]attribute.Value {
	m := make(map[attribute.Key]attribute.Value)
	for _, kv := range s.Attributes() {
		m[kv.Key] = kv.Value
	}
	return m
}

func TestObserver_EmitsSpan(t *testing.T) {
	obs, sr := newRecorder()

	// The core reports the technical op code ("mg"); the adapter formats it to a
	// readable span name and db.operation.
	ctx, op := obs.StartOp(context.Background(), memcache.OpInfo{
		Op: "mg", Server: "10.0.0.1:11211", Key: "user:42",
	})
	// The returned context must carry the active span so downstream work nests.
	require.True(t, trace.SpanFromContext(ctx).SpanContext().IsValid())
	op.End(memcache.OpResult{Result: memcache.ResultHit, Status: "VA"})

	spans := sr.Ended()
	require.Len(t, spans, 1)

	span := spans[0]
	require.Equal(t, "get 10.0.0.1:11211", span.Name())
	require.Equal(t, trace.SpanKindClient, span.SpanKind())
	require.Equal(t, semconv.SchemaURL, span.InstrumentationScope().SchemaURL)

	attrs := attrMap(span)
	require.Equal(t, "memcached", attrs["db.system.name"].AsString())
	require.Equal(t, "get", attrs["db.operation.name"].AsString())
	require.Equal(t, "10.0.0.1", attrs["server.address"].AsString())
	require.Equal(t, int64(11211), attrs["server.port"].AsInt64())
	require.Equal(t, "VA", attrs["db.response.status_code"].AsString())
	require.Equal(t, "hit", attrs["memcache.result"].AsString())
	_, legacySystem := attrs["db.system"]
	require.False(t, legacySystem)
	_, legacyOperation := attrs["db.operation"]
	require.False(t, legacyOperation)
	_, customRequests := attrs["memcache.requests"]
	require.False(t, customRequests)
	// By default the raw key is not recorded.
	_, hasKey := attrs["db.operation.parameter.key"]
	require.False(t, hasKey)
}

func TestObserver_ServerAttributes(t *testing.T) {
	cases := []struct {
		name        string
		server      string
		wantAddress string
		wantPort    int64
		hasPort     bool
	}{
		{name: "TCP", server: "cache.example:11211", wantAddress: "cache.example", wantPort: 11211, hasPort: true},
		{name: "IPv6", server: "[2001:db8::1]:11211", wantAddress: "2001:db8::1", wantPort: 11211, hasPort: true},
		{name: "host only", server: "cache.internal", wantAddress: "cache.internal"},
		{name: "Unix socket", server: "/var/run/memcached.sock", wantAddress: "/var/run/memcached.sock"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			obs, sr := newRecorder()
			_, op := obs.StartOp(context.Background(), memcache.OpInfo{Op: "mg", Server: tc.server})
			op.End(memcache.OpResult{})

			attrs := attrMap(sr.Ended()[0])
			require.Equal(t, tc.wantAddress, attrs["server.address"].AsString())
			port, ok := attrs["server.port"]
			require.Equal(t, tc.hasPort, ok)
			if tc.hasPort {
				require.Equal(t, tc.wantPort, port.AsInt64())
			}
		})
	}
}

func TestObserver_WithKeys(t *testing.T) {
	obs, sr := newRecorder(otelmemcache.WithKeys())

	_, op := obs.StartOp(context.Background(), memcache.OpInfo{Op: "mg", Server: "h:1", Key: "user:42"})
	op.End(memcache.OpResult{Result: memcache.ResultHit})

	require.Equal(t, "user:42", attrMap(sr.Ended()[0])["db.operation.parameter.key"].AsString())
}

func TestObserver_RecordsError(t *testing.T) {
	obs, sr := newRecorder()

	_, op := obs.StartOp(context.Background(), memcache.OpInfo{Op: "ms", Server: "h:1"})
	err := errors.New("dial failed")
	op.End(memcache.OpResult{Err: err})

	spans := sr.Ended()
	require.Len(t, spans, 1)
	require.Equal(t, codes.Error, spans[0].Status().Code)
	require.NotEmpty(t, spans[0].Events()) // RecordError adds an exception event
	require.Equal(t, semconv.ErrorType(err).Value.AsString(), attrMap(spans[0])["error.type"].AsString())
}

func TestObserver_BatchRequestCount(t *testing.T) {
	obs, sr := newRecorder()

	_, op := obs.StartOp(context.Background(), memcache.OpInfo{Op: "batch", Server: "h:1", Requests: 7})
	op.End(memcache.OpResult{})

	span := sr.Ended()[0]
	require.Equal(t, "BATCH h:1", span.Name())
	require.Equal(t, "BATCH", attrMap(span)["db.operation.name"].AsString())
	require.Equal(t, int64(7), attrMap(span)["db.operation.batch.size"].AsInt64())
}

func TestObserver_OmitsNonBatchRequestCount(t *testing.T) {
	for _, requests := range []int{0, 1} {
		obs, sr := newRecorder()
		_, op := obs.StartOp(context.Background(), memcache.OpInfo{Op: "batch", Server: "h:1", Requests: requests})
		op.End(memcache.OpResult{})

		_, ok := attrMap(sr.Ended()[0])["db.operation.batch.size"]
		require.False(t, ok)
	}
}
