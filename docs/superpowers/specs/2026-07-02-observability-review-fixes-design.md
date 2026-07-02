# Observability Review Fixes Design

## Goal

Make PR #89's observer and OpenTelemetry adapter report memcached failures accurately, emit current database semantic conventions, and receive the same automated test and lint coverage as the core module.

## Scope

The change covers three review findings:

1. Propagate response-level protocol errors to observers for single and batch execution.
2. Replace legacy OpenTelemetry attributes with the current database semantic conventions and align related span metadata.
3. Test, tidy, and lint the nested `otelmemcache` module in local DevBuddy commands and GitHub Actions.

The adapter module's temporary `github.com/pior/memcache v0.0.0` requirement and local replacement remain unchanged. The release process must replace that requirement with a published core version, or publish the exact required version.

## Observer Result Model

`OpResult` will continue to carry the domain result and error, and will add the single-operation response status code. The client derives the observed error in this order:

1. A transport, pool, context, parsing, or other Go error.
2. `meta.Response.Error` for `ERROR`, `CLIENT_ERROR`, and `SERVER_ERROR` responses.
3. No error.

For a batch, the observer receives the batch-level Go error when present, otherwise the first response-level protocol error. A response-count mismatch replaces the observed error with the mismatch error. Batch observation uses a deferred completion so `ActiveOp.End` runs exactly once across every normal return path.

Response status codes are emitted only for single operations because a batch can contain multiple status codes. Meta response statuses such as `HD`, `VA`, `EN`, `NF`, `NS`, and `EX` are preserved as low-cardinality values. Non-meta protocol errors are represented by `error.type` rather than an invented response status.

## OpenTelemetry Mapping

The adapter will use generated helpers from `go.opentelemetry.io/otel/semconv/v1.41.0`, the newest semantic-convention package shipped by OpenTelemetry Go v1.44.0.

Each span will include:

- `db.system.name=memcached`
- `db.operation.name=<operation>`
- `server.address=<host or Unix socket>` when available
- `server.port=<port>` when the configured endpoint contains a valid port
- `db.operation.batch.size=<count>` only for batches of at least two operations
- `db.response.status_code=<meta status>` for single operations with a response status
- `error.type=<concrete error type>` when the operation fails
- `memcache.result=<hit|miss|stored|not_stored>` when a domain result is known

`WithKeys` remains opt-in, but records the key through the standard `db.operation.parameter.key` attribute instead of `memcache.key`. No key or query payload is emitted by default.

Span names follow the database convention's operation-and-target form: `<operation> <server>`, for example `get cache.example:11211`. Batch operations use `BATCH`; other technical meta command codes are mapped to stable human-readable operation names. The tracer declares the semantic-convention schema URL.

The configured server is a logical endpoint, so it maps to `server.*`. The adapter will not emit `network.peer.*`, because the observer does not receive the actual connected peer address after DNS, proxies, or other intermediaries.

## Reference Emitter Findings

The design incorporates patterns found in established Go emitters:

- redisotel uses generated semantic-convention helpers, separates host and port with `net.SplitHostPort`, makes command capture configurable, and avoids treating cache misses as errors.
- otelpgx emits current database system, operation, endpoint, batch-size, and error metadata and keeps query parameters opt-in.
- official otelmongo separates database operation metadata from actual network-peer metadata, defaults command payload capture off, and records typed errors for database operation metrics.

The new adapter follows those patterns while preferring the current stable database conventions over legacy attribute compatibility. Because the adapter is unreleased, it will not dual-emit legacy attributes.

## CI and Developer Workflow

GitHub Actions will gain an `OpenTelemetry adapter` job that downloads, verifies, tidies, tests with the race detector, and lints from `otelmemcache/`.

DevBuddy commands will cover both modules:

- `bud test` tests the core module and then the adapter.
- `bud lint` lints the core module and then the adapter.
- `bud ci` tidies, lints, and race-tests both modules.
- `bud test-otelmemcache` provides a focused adapter command.

## Testing

Tests will be written before implementation and must demonstrate these behaviors:

- A single `SERVER_ERROR` or `CLIENT_ERROR` reaches `OpResult.Err`.
- A batch response-level protocol error reaches the batch `OpResult.Err`.
- Normal single responses carry their meta response status.
- OpenTelemetry spans use current attribute names and omit legacy names.
- TCP, IPv6, host-only, and Unix-socket endpoints produce valid server attributes.
- Batch size is emitted only when at least two operations are represented.
- Errors record an exception event, error status, and `error.type`.
- Key capture is absent by default and uses `db.operation.parameter.key` when enabled.
- Tracer scope carries the semantic-convention schema URL.

Final verification runs root and adapter tests, race tests through `bud ci`, both linters, tidy checks, and focused client benchmarks to confirm the default no-op observer path remains allocation-neutral.
