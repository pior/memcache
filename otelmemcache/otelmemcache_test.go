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

	ctx, op := obs.StartOp(context.Background(), memcache.OpInfo{
		Op: "get", Server: "10.0.0.1:11211", Key: "user:42",
	})
	// The returned context must carry the active span so downstream work nests.
	require.True(t, trace.SpanFromContext(ctx).SpanContext().IsValid())
	op.End(memcache.OpResult{Result: memcache.ResultHit})

	spans := sr.Ended()
	require.Len(t, spans, 1)

	span := spans[0]
	require.Equal(t, "memcache get", span.Name())
	require.Equal(t, trace.SpanKindClient, span.SpanKind())

	attrs := attrMap(span)
	require.Equal(t, "memcached", attrs["db.system"].AsString())
	require.Equal(t, "get", attrs["db.operation"].AsString())
	require.Equal(t, "10.0.0.1:11211", attrs["server.address"].AsString())
	require.Equal(t, "hit", attrs["memcache.result"].AsString())
	require.Equal(t, int64(1), attrs["memcache.requests"].AsInt64())
	// By default the raw key is not recorded; only a count.
	_, hasKey := attrs["memcache.key"]
	require.False(t, hasKey)
}

func TestObserver_RequestsAlwaysSet(t *testing.T) {
	obs, sr := newRecorder()

	// A stats op carries no key and no request count; memcache.requests is still
	// recorded, as 0, so the attribute is always present for aggregation.
	_, op := obs.StartOp(context.Background(), memcache.OpInfo{Op: "stats", Server: "h:1"})
	op.End(memcache.OpResult{})

	attrs := attrMap(sr.Ended()[0])
	val, ok := attrs["memcache.requests"]
	require.True(t, ok)
	require.Equal(t, int64(0), val.AsInt64())
}

func TestObserver_WithKeys(t *testing.T) {
	obs, sr := newRecorder(otelmemcache.WithKeys())

	_, op := obs.StartOp(context.Background(), memcache.OpInfo{Op: "get", Server: "h:1", Key: "user:42"})
	op.End(memcache.OpResult{Result: memcache.ResultHit})

	require.Equal(t, "user:42", attrMap(sr.Ended()[0])["memcache.key"].AsString())
}

func TestObserver_RecordsError(t *testing.T) {
	obs, sr := newRecorder()

	_, op := obs.StartOp(context.Background(), memcache.OpInfo{Op: "set", Server: "h:1"})
	op.End(memcache.OpResult{Err: errors.New("dial failed")})

	spans := sr.Ended()
	require.Len(t, spans, 1)
	require.Equal(t, codes.Error, spans[0].Status().Code)
	require.NotEmpty(t, spans[0].Events()) // RecordError adds an exception event
}

func TestObserver_BatchRequestCount(t *testing.T) {
	obs, sr := newRecorder()

	_, op := obs.StartOp(context.Background(), memcache.OpInfo{Op: "batch", Server: "h:1", Requests: 7})
	op.End(memcache.OpResult{})

	require.Equal(t, int64(7), attrMap(sr.Ended()[0])["memcache.requests"].AsInt64())
}
