# Agents Guide

This guide helps AI agents work effectively with the memcache project.

## Development Environment

Use DevBuddy (`bud`) for development tasks. Key commands:

```bash
bud up          # Set up development environment
bud test        # Run all tests
bud lint        # Run golangci-lint
bud bench       # Run client benchmarks
bud test-integration  # Run integration tests (requires memcached)
```

## Code Quality

### Linting
Always run `bud lint` or `golangci-lint run` after adding/changing code.

### Testing
- Run tests with `bud test` or `go test -v ./...`
- Use race detector only when needed: `go test -race ./...`
- Integration tests require memcached: `bud test-integration`
- Write maintainable tests using subtests and reusable fixtures
- Prefer comparing values as string, to make errors more readable

### Benchmarking
Use benchstat for comparing benchmarks. Run with:
```bash
go test -bench=. -benchmem -benchtime=2s -count=20 ./...
```

**Tips:**
- Use a sink variable to prevent compiler optimization of unused results
- Run specific benchmarks with `-bench='BenchmarkName'` to save time
- Ensure results are statistically significant before drawing conclusions

## Coding Standards

### Modern Go
- Use Go 1.24+ features: generics, maps/slices packages, rand/v2, json/v2
- Use new for loop forms
- Use new testing capabilities (t.Cleanup, b.Loop(), etc.)

### Style
- Prefer explicit code over comments, but add code comments when additional context is interesting
- Follow Go's standard formatting and naming conventions
- Use concise, clear variable names

### Performance Patterns
When optimizing hot paths:
- Use `bufio.ReadSlice` for zero-allocation line reading (fall back to `ReadBytes` for long lines)
- Use `bytes.TrimSuffix` instead of `strings.TrimSuffix` when working with `[]byte`
- Use incremental byte-slice parsing instead of `strings.Fields` to avoid slice allocation
- Pre-allocate byte slices for repeated comparisons (e.g., `var crlfBytes = []byte("\r\n")`)
- Slice iteration is faster than map lookup for small collections (<20 items) due to cache locality
- No `strconv.Atoi` for `[]byte` in stdlib - accept small allocation or use manual parsing

### Design Guidelines
- Check `references/implementations/` when making design decisions to see how other clients handle edge cases
- Prioritize readability over performance for debug and non-hot code paths
- Trust the server - don't add client-side limits that the server doesn't enforce

## Project Structure

- `meta/` - Low-level meta protocol implementation
- `cmd/` - Command-line tools (speed tester, etc.)
- `spec/` - Protocol specifications and experiments
- `references/` - Reference implementations in other languages

## Workflow

1. Make changes to code
2. Run `bud lint` to check code quality
3. Run relevant tests (`bud test`, `bud test-integration`)
4. Run benchmarks if performance-critical changes: `bud bench`
5. Update examples and documentation if relevant

## Dependencies

It's ok to add a new dependency, but ask first.

## Memory Notes

If you need to write notes, or anything that should not be commited,
use the `private-agent-notes` directory at the root of the repository.
This directory is ignored by Git.

## Project

### Goal

The goal of this project is to implement a memcache client in Go.

Features:
- The client must be thread-safe.
- Only support the meta protocol, not the text protocol.
- Support for connection pooling, with a preference for the connection that has the least number of requests in flight.
- Support adding an optional circuit breaker on connections.
- Support using multiple servers, with consistent hashing.
- High performance: avoid allocations, benchmark everything.

Nice to have:
- Support for commands pipelining.

Structure:
- High-level client in the top-level package (`github.com/pior/memcache`)
  - Convenient, and opinionated API
  - High performance, but some trade-offs are acceptable for increased convenience
- Low-level client, also in the top-level package
- Complete "meta" protocol implementation in `github.com/pior/memcache/meta`
  - With some bits from the text protocol that are not available in the meta protocol, like error responses

### Reference material

- The Meta protocol is documention is available in references/
- Other popular implementations are available in references/implementations/
- A pre-generated specification is available in spec/

### Expectations

- README.md should be concise, but insightful, describing the project, scope, choices and limitations, with a usage example.
- Extensive unit-tests, with a focus on maintainable test code
- Benchmarks on the client and the meta protocol package
- Fuzz tests on the client and the meta protocol package
- Integration tests using a real memcache server
