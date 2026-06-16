// Package otelmemcache provides an OpenTelemetry adapter for the memcache
// client. It implements memcache.Observer, emitting a client span per
// operation. Wire it in via Config.Observer:
//
//	cfg := memcache.Config{Observer: otelmemcache.New(tracerProvider)}
//
// It is a separate module so the OpenTelemetry dependency tree does not leak
// into consumers of the core memcache package.
package otelmemcache

import (
	"context"

	"github.com/pior/memcache"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const scopeName = "github.com/pior/memcache/otelmemcache"

type observer struct {
	tracer     trace.Tracer
	recordKeys bool
}

// Option configures the Observer returned by New.
type Option func(*observer)

// WithKeys records each operation's key as the memcache.key span attribute.
// It is off by default because keys can be high-cardinality or carry PII; only
// enable it when your keys are safe to export to your tracing backend.
func WithKeys() Option {
	return func(o *observer) { o.recordKeys = true }
}

// New returns a memcache.Observer that emits one client span per operation
// using tp. If tp is nil, the global TracerProvider is used.
func New(tp trace.TracerProvider, opts ...Option) memcache.Observer {
	if tp == nil {
		tp = otel.GetTracerProvider()
	}
	o := &observer{tracer: tp.Tracer(scopeName)}
	for _, opt := range opts {
		opt(o)
	}
	return o
}

func (o *observer) StartOp(ctx context.Context, info memcache.OpInfo) (context.Context, memcache.ActiveOp) {
	attrs := []attribute.KeyValue{
		attribute.String("db.system", "memcached"),
		attribute.String("db.operation", info.Op),
	}
	if info.Server != "" {
		attrs = append(attrs, attribute.String("server.address", info.Server))
	}
	// Record how many requests the span covers. A batch reports its request
	// count; a single op is 1; an op without a key (stats) is 0. Keys are
	// excluded by default — they can be high-cardinality or carry PII, matching
	// the core's decision to keep keys out of errors — and opt in via WithKeys.
	requests := info.Requests
	if requests == 0 && info.Key != "" {
		requests = 1
	}
	attrs = append(attrs, attribute.Int("memcache.requests", requests))
	if o.recordKeys && info.Key != "" {
		attrs = append(attrs, attribute.String("memcache.key", info.Key))
	}

	ctx, span := o.tracer.Start(ctx, "memcache "+info.Op,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attrs...),
	)

	return ctx, &activeOp{span: span}
}

// activeOp is the in-flight span for a single memcache operation.
type activeOp struct {
	span trace.Span
}

func (a *activeOp) End(res memcache.OpResult) {
	if res.Result != memcache.ResultUnknown {
		a.span.SetAttributes(attribute.String("memcache.result", res.Result.String()))
	}
	if res.Err != nil {
		a.span.RecordError(res.Err)
		a.span.SetStatus(codes.Error, res.Err.Error())
	}
	a.span.End()
}
