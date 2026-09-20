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
	"fmt"
	"net"
	"strconv"

	"github.com/pior/memcache"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/trace"
)

const scopeName = "github.com/pior/memcache/otelmemcache"

// operationName is the core's operation name, which is already the readable
// one ("get", "touch", "add", ...) and is used verbatim for the span name and
// db.operation.name. The one exception is a batch, which OpenTelemetry's
// database conventions name "BATCH".
func operationName(op string) string {
	if op == memcache.OpBatch {
		return "BATCH"
	}
	return op
}

type observer struct {
	tracer     trace.Tracer
	recordKeys bool
}

// Options configures the Observer returned by New. The zero value emits one
// span per operation without the key, which is the usual choice.
type Options struct {
	// RecordKeys records each operation's key as the db.operation.parameter.key
	// span attribute. It is off by default because keys can be
	// high-cardinality or carry PII; only enable it when your keys are safe to
	// export to your tracing backend.
	RecordKeys bool
}

// New returns a memcache.Observer that emits one client span per operation
// using tp. If tp is nil, the global TracerProvider is used.
//
// opts accepts at most one Options value; the variadic form keeps the common
// call site free of an empty struct, and passing more is a programming error.
func New(tp trace.TracerProvider, opts ...Options) memcache.Observer {
	if tp == nil {
		tp = otel.GetTracerProvider()
	}

	var options Options
	switch len(opts) {
	case 0:
	case 1:
		options = opts[0]
	default:
		panic(fmt.Sprintf("otelmemcache: at most one Options may be passed, got %d", len(opts)))
	}

	return &observer{
		tracer:     tp.Tracer(scopeName, trace.WithSchemaURL(semconv.SchemaURL)),
		recordKeys: options.RecordKeys,
	}
}

func (o *observer) StartOp(ctx context.Context, info memcache.OpInfo) (context.Context, memcache.ActiveOp) {
	name := operationName(info.Op)
	attrs := []attribute.KeyValue{
		semconv.DBSystemNameMemcached,
		semconv.DBOperationName(name),
	}
	attrs = append(attrs, serverAttributes(info.Address)...)
	if info.Op == memcache.OpBatch && info.Requests >= 2 {
		attrs = append(attrs, semconv.DBOperationBatchSize(info.Requests))
	}
	// Keys are excluded by default because they can be high-cardinality or carry
	// PII. Options.RecordKeys opts in to the standard database operation parameter.
	if o.recordKeys && info.Key != "" {
		attrs = append(attrs, semconv.DBOperationParameter("key", info.Key))
	}

	spanName := name
	if info.Address != "" {
		spanName += " " + info.Address
	}
	ctx, span := o.tracer.Start(ctx, spanName,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attrs...),
	)

	return ctx, &activeOp{span: span}
}

func serverAttributes(server string) []attribute.KeyValue {
	if server == "" {
		return nil
	}

	host, portString, err := net.SplitHostPort(server)
	if err != nil {
		return []attribute.KeyValue{semconv.ServerAddress(server)}
	}

	attrs := []attribute.KeyValue{semconv.ServerAddress(host)}
	if port, err := strconv.Atoi(portString); err == nil {
		attrs = append(attrs, semconv.ServerPort(port))
	}
	return attrs
}

// activeOp is the in-flight span for a single memcache operation.
type activeOp struct {
	span trace.Span
}

func (a *activeOp) End(res memcache.OpResult) {
	// The interpreted outcome and the wire code both go on the span: the
	// status tells an add's NS (Exists) from a replace's (NotFound), the code
	// is what the server actually replied.
	if res.Status != 0 {
		a.span.SetAttributes(attribute.String("memcache.status", res.Status.String()))
	}
	if res.Code != "" {
		a.span.SetAttributes(semconv.DBResponseStatusCode(res.Code))
	}
	if res.Err != nil {
		a.span.SetAttributes(semconv.ErrorType(res.Err))
		a.span.RecordError(res.Err)
		a.span.SetStatus(codes.Error, res.Err.Error())
	}
	a.span.End()
}
