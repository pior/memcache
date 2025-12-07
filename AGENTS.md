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

## Coding Standards

### Modern Go
- Use Go 1.24+ features: generics, maps/slices packages, rand/v2, json/v2
- Use new for loop forms
- Use new testing capabilities (t.Cleanup, b.Loop(), etc.)

### Style
- Prefer explicit code over comments, but add code comments when additional context is interesting
- Follow Go's standard formatting and naming conventions
- Use concise, clear variable names

## Project Structure

- `meta/` - Low-level meta protocol implementation
- `cmd/` - Command-line tools (speed tester, etc.)
- `spec/` - Protocol specifications and experiments
- `references/` - Reference implementations in other languages

## Key Files

- `README.md` - Project overview and usage examples
- `.instructions.md` - Implementation goals and requirements
- `dev.yml` - DevBuddy configuration
- `.golangci.yml` - Linting configuration

## Workflow

1. Make changes to code
2. Run `bud lint` to check code quality
3. Run relevant tests (`bud test`, `bud test-integration`)
4. Run benchmarks if performance-critical changes: `bud bench`
5. Update documentation if needed

## Dependencies

Core dependencies are minimal:
- `github.com/sony/gobreaker/v2` - Circuit breaker
- `github.com/jackc/puddle/v2` - Default pool implementation

## Server Selection Algorithms

- `JumpSelectServer` - Jump Hash algorithm (default, better distribution)
- `DefaultSelectServer` - CRC32-based hashing (~20ns faster, simpler)

## Memory Notes

Maintain private notes for yourself in a `private-agent-notes` directory, at the root of the repository.
This directory is ignored by Git.
Write two types of notes:
- "topic" notes: general information gathered about the current repo/project: `topic-<description>.md`
- "work" notes: information about a specific work/goal: `work-<work-name-or-branch-name>.md`