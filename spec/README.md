# Memcached Meta Protocol Specification

This directory contains comprehensive documentation and experimental validation of the Memcached Meta Protocol.

## Contents

### Documentation

- **[META_PROTOCOL_SPEC.md](./META_PROTOCOL_SPEC.md)** - Complete specification of the Meta Protocol (human-readable)
  - Command reference for all meta commands (mg, ms, md, ma, me, mn)
  - All flags with detailed behaviors (`h`, `l`, `u`, `x`, etc.)
  - Response codes and semantics
  - Edge cases and error handling
  - Protocol error conditions with exact error messages
  - Mixed text/meta protocol compatibility
  - Arithmetic edge cases (overflow/underflow)
  - Win flag lifecycle and state machine
  - LRU and access tracking behaviors
  - Pipelining guide
  - Protocol limits and constraints
  - Implementation notes

- **[META_PROTOCOL_AI.md](./META_PROTOCOL_AI.md)** - Token-optimized specification for AI agents
  - Same comprehensive information as main spec
  - Compact table-based format (63% fewer tokens)
  - Optimized for LLM context windows
  - All commands, flags, edge cases, and behaviors included

- **[QUICK_REFERENCE.md](./QUICK_REFERENCE.md)** - Quick lookup guide for common operations

### Experiments

The `experiments/` directory contains Go programs that validate protocol behavior against a live memcached 1.6 server:

1. **01_metaget_basic.go** - Meta Get (mg) command with all flags
2. **02_metaset_modes.go** - Meta Set (ms) modes (set/add/replace/append/prepend)
3. **03_metadelete.go** - Meta Delete (md) with invalidation and stale-while-revalidate
4. **04_metaarithmetic.go** - Meta Arithmetic (ma) increment/decrement operations
5. **05_edge_cases.go** - Protocol boundaries, long keys, empty values, base64 encoding
6. **06_protocol_edge_cases.go** - Error conditions, protocol limits, mixed protocol, advanced flags (h/l/u/x)

### Running Experiments

To run the experiments, you need a memcached 1.6+ server running on localhost:11211.

Using Docker:
```bash
docker run -d --name memcached-test -p 11211:11211 memcached:1.6
```

Then run any experiment:
```bash
go run experiments/01_metaget_basic.go
```

## Quick Command Reference

| Command | Purpose | Example |
|---------|---------|---------|
| `mg` | Get item | `mg key v c t` |
| `ms` | Set item | `ms key 5 T60\r\nvalue\r\n` |
| `md` | Delete item | `md key` |
| `ma` | Arithmetic | `ma counter v D5` |
| `me` | Debug info | `me key` |
| `mn` | No-op | `mn` |

## Response Codes

| Code | Meaning |
|------|---------|
| `HD` | Success (header only) |
| `VA` | Success with value |
| `EN` | Not found (miss) |
| `NF` | Not found (for operations) |
| `NS` | Not stored |
| `EX` | CAS mismatch (exists) |
| `MN` | No-op response |

## Key Features

### Stampede Protection
```
mg key v N30  # Vivify on miss with 30s TTL
→ VA 0 W      # Win flag grants exclusive recache rights
```

### Stale-While-Revalidate
```
md key I T30  # Mark stale, keep for 30s
mg key v      # Returns with X (stale) and W (win) flags
```

### Early Recache
```
mg key v R30  # If TTL < 30s, get win flag
→ VA 5 t25 W  # TTL is 25s, recache granted
```

### Pipelining with Quiet Mode
```
mg key1 v q
mg key2 v q
mg key3 v q
mn           # Marks end of pipeline
```

## Implementation Checklist

When implementing a Meta Protocol client:

**Core Protocol:**
- [ ] Parse 2-character response codes (HD, VA, EN, NF, EX, NS, MN, ME)
- [ ] Handle flag ordering in responses (matches request order)
- [ ] Validate key constraints (1-250 bytes, no whitespace unless base64)
- [ ] Validate opaque tokens (≤32 bytes)
- [ ] Support base64 keys (`b` flag)

**Error Handling:**
- [ ] Close connection on CLIENT_ERROR
- [ ] Handle all error responses correctly
- [ ] Detect duplicate/conflicting flags

**Commands:**
- [ ] Meta Get (mg) with all flags
- [ ] Meta Set (ms) with all storage modes (set/add/replace/append/prepend)
- [ ] Meta Delete (md) with invalidation
- [ ] Meta Arithmetic (ma) with overflow/underflow handling
- [ ] Meta Debug (me)
- [ ] Meta No-Op (mn)

**Advanced Features:**
- [ ] Quiet mode (`q` flag)
- [ ] Opaque tokens (`O` flag)
- [ ] Stale-while-revalidate (`W`, `X`, `Z` flags)
- [ ] CAS operations (`C`, `E`, `c` flags)
- [ ] TTL operations (`T`, `t` flags)
- [ ] LRU management (`h`, `l`, `u` flags)
- [ ] Win flag lifecycle handling
- [ ] Pipelining with quiet mode

**Edge Cases:**
- [ ] Arithmetic overflow (wraps to 0)
- [ ] Arithmetic underflow (stops at 0)
- [ ] Zero-length values
- [ ] CAS value 0 (invalid)
- [ ] Mixed text/meta protocol
- [ ] Multiple spaces in commands (tolerated)

## Testing Against Live Server

The experiments in this directory serve as both documentation and test cases. They verify:

- Basic command functionality (mg, ms, md, ma, me, mn)
- All flag combinations and behaviors
- Error conditions with exact error messages
- Edge cases (empty values, long keys, base64 encoding, etc.)
- CAS semantics including edge cases
- Stale-while-revalidate complete lifecycle
- Win flag state machine (W → Z → cleared)
- Arithmetic overflow/underflow protection
- Pipelining with quiet mode
- Advanced flags (h, l, u, x)
- Mixed text/meta protocol compatibility
- Protocol limits and constraints

All experiments have been validated against memcached 1.6.39.

## References

- [Official Protocol Documentation](https://github.com/memcached/memcached/blob/master/doc/protocol.txt)
- [Meta Protocol Wiki](https://github.com/memcached/memcached/wiki/MetaCommands)
- [Memcached Docs](https://docs.memcached.org/protocols/meta/)
