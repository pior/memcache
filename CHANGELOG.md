# Changelog

Notable changes per release. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

This project is pre-v1.0: a breaking change bumps the **minor** version and is
listed under "Changed" or "Removed". See [RELEASING.md](RELEASING.md).

## [Unreleased]

## [0.1.0] - 2026-09-21

First tagged release. The meta protocol implementation and the client's failure
handling have been validated under sustained load and injected faults — see
[How It's Tested](README.md#how-its-tested) for the record. The API has not been
used by enough callers to freeze, which is what keeps this below v1.

Two modules are published, in lockstep:

- `github.com/pior/memcache` — the client and the `meta` protocol package.
- `github.com/pior/memcache/otelmemcache` — an OpenTelemetry tracing adapter,
  separate so the OTel dependency tree stays out of the core package.

### Added

- A `Client` speaking the memcached meta protocol: single-key reads, writes,
  deletes, counters and touch, plus `MultiGet` / `MultiSet` / `MultiDelete`.
- `ExecuteBatch`, which pipelines an arbitrary mix of meta commands in one round
  trip and returns responses matched to requests by position, owned by the
  caller.
- Multi-server support with rendezvous hashing by default
  (`StableServerSelector`), jump hash for a static ordered list
  (`OrderedServerSelector`), or a caller-supplied `Config.ServerSelector`.
  `Servers` is an interface, so the list can come from service discovery.
- Per-server connection pools with a bounded size, connection lifetime and idle
  limits, health checks, and reaping of pools whose server has departed.
- Per-server circuit breakers (`Config.Breaker`), off by default, counting only
  transport-level failures against the server.
- A three-phase timeout model: pool checkout bounded by the caller's context,
  dial by `DialTimeout`, and I/O by the earlier of the context deadline and
  `OperationTimeout` — a cap that cannot be disabled.
- `Config.Observer`, invoked around every operation, and `PoolMetrics()` for
  pool and breaker state.
- TLS support.
- The `meta` package: the protocol codec (requests, responses, flags) as a
  building block for custom clients, alongside `Connection` and `Commands`.

[Unreleased]: https://github.com/pior/memcache/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/pior/memcache/releases/tag/v0.1.0
