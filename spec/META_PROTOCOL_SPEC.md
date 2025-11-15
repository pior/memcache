# Memcached Meta Protocol Specification

**Version:** 1.6+
**Status:** Stable (no longer experimental as of memcached 1.6)
**Last Updated:** 2025-01-15

## Table of Contents

1. [Overview](#overview)
2. [Protocol Basics](#protocol-basics)
3. [Response Codes](#response-codes)
4. [Meta Get (mg)](#meta-get-mg)
5. [Meta Set (ms)](#meta-set-ms)
6. [Meta Delete (md)](#meta-delete-md)
7. [Meta Arithmetic (ma)](#meta-arithmetic-ma)
8. [Meta Debug (me)](#meta-debug-me)
9. [Meta No-Op (mn)](#meta-no-op-mn)
10. [Flags Reference](#flags-reference)
11. [Edge Cases and Error Handling](#edge-cases-and-error-handling)
12. [Pipelining](#pipelining)
13. [Advanced Flag Behaviors](#advanced-flag-behaviors)
14. [Protocol Error Conditions](#protocol-error-conditions)
15. [Mixed Protocol Compatibility](#mixed-protocol-compatibility)
16. [Arithmetic Edge Cases](#arithmetic-edge-cases)
17. [Win Flag Lifecycle Details](#win-flag-lifecycle-details)
18. [Protocol Limits Summary](#protocol-limits-summary)

---

## Overview

The Meta Protocol is Memcached's modern text-based protocol that replaces both the legacy text protocol and the deprecated binary protocol. It provides:

- **Reduced bytes on the wire** through compact flag syntax
- **Reduced network roundtrips** via built-in features like stampede protection
- **Enhanced features** including stale-while-revalidate, TTL queries, and more
- **Full compatibility** with the text protocol (commands can be mixed)

### Key Principles

- All commands are 2-character codes: `mg`, `ms`, `md`, `ma`, `me`, `mn`
- Flags are single characters, optionally followed by a token
- Responses are 2-character status codes: `HD`, `VA`, `EN`, `NF`, `EX`, `NS`, `MN`
- All commands and responses are terminated with `\r\n`

---

## Protocol Basics

### Request Format

```
<command> <key> [<size>] <flags>*\r\n
[<data block>\r\n]
```

- `<command>`: Two-character command code
- `<key>`: Key string (1-250 bytes, no whitespace unless base64-encoded)
- `<size>`: Data length in bytes (for `ms` command)
- `<flags>`: Space-separated single-character flags with optional tokens
- `<data block>`: Binary data (for `ms` command)

### Response Format

```
<status> [<flags>*]\r\n
[<data block>\r\n]
```

- `<status>`: Two-character response code
- `<flags>`: Space-separated response flags in request order
- `<data block>`: Binary data (if requested with `v` flag)

### Key Constraints

- **Length**: 1-250 bytes
- **Characters**: No whitespace or control characters (unless base64-encoded with `b` flag)
- **Encoding**: ASCII or base64 (with `b` flag)

---

## Response Codes

| Code | Name | Meaning |
|------|------|---------|
| `HD` | Header/Stored | Success with no value data returned |
| `VA` | Value | Success with value data following |
| `EN` | End/Not Found | Key not found (miss) |
| `NF` | Not Found | Key not found (for operations requiring existing key) |
| `NS` | Not Stored | Item was not stored (not an error, e.g., add on existing key) |
| `EX` | Exists | CAS mismatch - item was modified |
| `MN` | Meta No-op | Response to `mn` command |
| `ME` | Meta Debug | Debug information response |

### Non-Meta Error Responses

The server may also return traditional error responses:

- `ERROR\r\n` - Generic error or unknown command
- `CLIENT_ERROR <reason>\r\n` - Client sent invalid data
- `SERVER_ERROR <reason>\r\n` - Server-side error

**Critical**: After a `CLIENT_ERROR`, the connection should be closed as the protocol state may be corrupted.

---

## Meta Get (mg)

Retrieve item data and metadata from the cache.

### Syntax

```
mg <key> <flags>*\r\n
```

### Response

**Success (no value):**
```
HD [<flags>*]\r\n
```

**Success (with value):**
```
VA <size> [<flags>*]\r\n
<data block>\r\n
```

**Miss:**
```
EN\r\n
```

*Note: `EN` response is suppressed when `q` (quiet) flag is used*

### Flags

#### Retrieval Flags

| Flag | Token | Description |
|------|-------|-------------|
| `v` | - | Return item value in data block. Response changes from `HD` to `VA <size>` |
| `k` | - | Return key in response |
| `c` | - | Return CAS value |
| `f` | - | Return client flags (32-bit unsigned integer) |
| `s` | - | Return value size in bytes |
| `t` | - | Return TTL remaining in seconds (-1 for infinite) |
| `l` | - | Return time since last access in seconds |
| `h` | - | Return whether item has been hit before (0 or 1) |
| `O<token>` | string | Opaque token (up to 32 bytes) reflected in response |
| `q` | - | Quiet mode: suppress `EN` response on miss |
| `b` | - | Interpret key as base64-encoded binary |
| `u` | - | Don't bump item in LRU (no access time update) |

#### Modification Flags

| Flag | Token | Description |
|------|-------|-------------|
| `T<token>` | seconds | Update TTL remaining to specified seconds |
| `N<token>` | seconds | Vivify on miss: create stub item with TTL, return `W` flag |
| `R<token>` | seconds | Recache flag: if TTL < token, return `W` flag |
| `E<token>` | uint64 | Use specified CAS value if item is modified |

#### Special Response Flags

| Flag | Meaning |
|------|---------|
| `W` | Win flag - client has exclusive right to recache |
| `X` | Stale flag - item is marked as stale |
| `Z` | Already won - another client has received `W` flag |

### Examples

**Basic get with value:**
```
mg mykey v\r\n
→ VA 5\r\n
  hello\r\n
```

**Get with metadata:**
```
mg mykey k c t s\r\n
→ HD kmykey c12345 t3600 s5\r\n
```

**Get with value on miss (vivify):**
```
mg newkey v N60\r\n
→ VA 0 W\r\n
  \r\n
```
*Client receives empty value and `W` flag indicating exclusive recache rights*

**Early recache:**
```
mg expiring v R30\r\n
→ VA 5 t25 W\r\n
  hello\r\n
```
*TTL is 25 seconds, less than threshold 30, so `W` flag is returned*

### Stale-While-Revalidate Pattern

1. Mark item as stale:
   ```
   md mykey I T30\r\n
   → HD\r\n
   ```

2. First client gets stale data with win flag:
   ```
   mg mykey v c\r\n
   → VA 5 c100 W X\r\n
     stale\r\n
   ```

3. Subsequent clients get stale data without win:
   ```
   mg mykey v c\r\n
   → VA 5 c100 Z X\r\n
     stale\r\n
   ```

---

## Meta Set (ms)

Store data in the cache with various modes and options.

### Syntax

```
ms <key> <size> <flags>*\r\n
<data block>\r\n
```

- `<size>`: Number of bytes in data block

### Response

| Code | Meaning |
|------|---------|
| `HD` | Successfully stored |
| `NS` | Not stored (e.g., add on existing key, replace on missing key) |
| `NF` | Not found (append/prepend on missing key without auto-vivify) |
| `EX` | CAS mismatch |

### Flags

| Flag | Token | Description |
|------|-------|-------------|
| `F<token>` | uint32 | Set client flags (defaults to 0 if not specified) |
| `T<token>` | seconds | Set TTL (0 or omitted = infinite) |
| `C<token>` | uint64 | Compare CAS value before storing |
| `E<token>` | uint64 | Use specified CAS value for new item |
| `M<token>` | mode | Storage mode (see below) |
| `c` | - | Return CAS value in response |
| `k` | - | Return key in response |
| `s` | - | Return size in response |
| `O<token>` | string | Opaque token reflected in response |
| `q` | - | Quiet mode: suppress `HD` response on success |
| `b` | - | Interpret key as base64-encoded binary |
| `I` | - | Invalidate mode: if CAS is older, mark as stale |
| `N<token>` | seconds | Auto-vivify on miss (append/prepend modes only) |

### Storage Modes (M flag)

| Mode | Description |
|------|-------------|
| `MS` | **Set** (default): Store unconditionally |
| `ME` | **Add**: Store only if key doesn't exist (returns `NS` if exists) |
| `MR` | **Replace**: Store only if key exists (returns `NS` if missing) |
| `MA` | **Append**: Append data to existing value (returns `NF` if missing) |
| `MP` | **Prepend**: Prepend data to existing value (returns `NF` if missing) |

### Examples

**Basic set:**
```
ms mykey 5\r\n
hello\r\n
→ HD\r\n
```

**Set with TTL and flags:**
```
ms mykey 5 F30 T3600\r\n
hello\r\n
→ HD\r\n
```

**Add (store only if new):**
```
ms mykey 5 ME\r\n
hello\r\n
→ HD\r\n  (success)

ms mykey 5 ME\r\n
world\r\n
→ NS\r\n  (already exists)
```

**Replace (store only if exists):**
```
ms newkey 5 MR\r\n
hello\r\n
→ NS\r\n  (doesn't exist)
```

**Append:**
```
ms key 5\r\n
hello\r\n
→ HD\r\n

ms key 6 MA\r\n
world!\r\n
→ HD\r\n

mg key v\r\n
→ VA 11\r\n
  helloworld!\r\n
```

**CAS (Compare-And-Swap):**
```
mg mykey c\r\n
→ HD c12345\r\n

ms mykey 5 C12345\r\n
hello\r\n
→ HD\r\n

ms mykey 5 C99999\r\n
world\r\n
→ EX\r\n  (CAS mismatch)
```

**Return CAS after set:**
```
ms mykey 5 c\r\n
hello\r\n
→ HD c12346\r\n
```

---

## Meta Delete (md)

Delete or invalidate items.

### Syntax

```
md <key> <flags>*\r\n
```

### Response

| Code | Meaning |
|------|---------|
| `HD` | Successfully deleted/invalidated |
| `NF` | Key not found |
| `EX` | CAS mismatch |

### Flags

| Flag | Token | Description |
|------|-------|-------------|
| `I` | - | Invalidate: mark as stale instead of deleting |
| `T<token>` | seconds | Update TTL (only with `I` flag) |
| `C<token>` | uint64 | Compare CAS value before deletion |
| `E<token>` | uint64 | Use specified CAS value when invalidating |
| `k` | - | Return key in response |
| `O<token>` | string | Opaque token reflected in response |
| `q` | - | Quiet mode: suppress `HD` response on success |
| `b` | - | Interpret key as base64-encoded binary |

### Examples

**Basic delete:**
```
md mykey\r\n
→ HD\r\n

mg mykey v\r\n
→ EN\r\n
```

**Delete with CAS:**
```
mg mykey c\r\n
→ HD c100\r\n

md mykey C100\r\n
→ HD\r\n

md mykey C100\r\n
→ NF\r\n  (already deleted)
```

**Invalidate (mark as stale):**
```
md mykey I T30\r\n
→ HD\r\n

mg mykey v c\r\n
→ VA 5 c101 W X\r\n
  hello\r\n
```
*Item is marked stale (`X`) and client gets win flag (`W`)*

**Delete non-existent key:**
```
md nonexistent\r\n
→ NF\r\n
```

---

## Meta Arithmetic (ma)

Perform atomic increment/decrement operations on numeric values.

### Syntax

```
ma <key> <flags>*\r\n
```

### Response

**Without value:**
```
HD [<flags>*]\r\n
```

**With value:**
```
VA <size> [<flags>*]\r\n
<number>\r\n
```

**Errors:**
- `NF`: Key not found (without auto-create)
- `NS`: Auto-create failed
- `EX`: CAS mismatch

### Flags

| Flag | Token | Description |
|------|-------|-------------|
| `D<token>` | uint64 | Delta value (default: 1) |
| `M<token>` | mode | Mode: `MI`/`M+` for increment (default), `MD`/`M-` for decrement |
| `N<token>` | seconds | Auto-create on miss with TTL |
| `J<token>` | uint64 | Initial value for auto-create (default: 0) |
| `T<token>` | seconds | Update TTL on success |
| `C<token>` | uint64 | Compare CAS before operation |
| `E<token>` | uint64 | Use specified CAS value |
| `v` | - | Return new value in response |
| `c` | - | Return CAS value |
| `t` | - | Return TTL |
| `k` | - | Return key |
| `O<token>` | string | Opaque token |
| `q` | - | Quiet mode |
| `b` | - | Base64 key |

### Value Constraints

- Values are **unsigned 64-bit integers**
- Decrement stops at 0 (no underflow)
- Increment can overflow (wraps to 0)
- Non-numeric values will cause protocol errors

### Examples

**Basic increment:**
```
ms counter 2\r\n
10\r\n
→ HD\r\n

ma counter v\r\n
→ VA 2\r\n
  11\r\n
```

**Increment by custom delta:**
```
ma counter v D5\r\n
→ VA 2\r\n
  16\r\n
```

**Decrement:**
```
ma counter v MD D3\r\n
→ VA 2\r\n
  13\r\n
```

**Decrement with underflow protection:**
```
ms counter 1\r\n
5\r\n
→ HD\r\n

ma counter v MD D10\r\n
→ VA 1\r\n
  0\r\n  (stops at zero)
```

**Auto-create on miss:**
```
ma newcounter v N60\r\n
→ VA 1\r\n
  0\r\n  (created with value 0)

ma newcounter v\r\n
→ VA 1\r\n
  1\r\n
```

**Auto-create with initial value:**
```
ma seeded v N60 J100\r\n
→ VA 3\r\n
  100\r\n
```

**With CAS:**
```
mg counter c\r\n
→ HD c50\r\n

ma counter v C50\r\n
→ VA 1\r\n
  1\r\n

ma counter v C50\r\n
→ EX\r\n  (CAS changed)
```

---

## Meta Debug (me)

Get human-readable internal metadata about an item (without the value).

### Syntax

```
me <key> <flags>*\r\n
```

### Response

**Success:**
```
ME <key> <key>=<value>*\r\n
```

**Miss:**
```
EN\r\n
```

### Flags

| Flag | Description |
|------|-------------|
| `b` | Interpret key as base64-encoded binary |

### Metadata Fields

| Field | Description |
|-------|-------------|
| `exp` | Expiration time |
| `la` | Time in seconds since last access |
| `cas` | CAS ID |
| `fetch` | Whether item has been fetched before |
| `cls` | Slab class ID |
| `size` | Total size in bytes |

### Example

```
me mykey\r\n
→ ME mykey exp=3600 la=5 cas=12345 fetch=yes cls=1 size=128\r\n
```

---

## Meta No-Op (mn)

No-operation command that returns a static response. Useful for detecting end of pipelined commands.

### Syntax

```
mn\r\n
```

### Response

```
MN\r\n
```

### Use Case

When pipelining commands with quiet flags (`q`), use `mn` at the end to detect when all commands have been processed:

```
mg key1 v q\r\n
mg key2 v q\r\n
mg key3 v q\r\n
mn\r\n
```

Responses will only include hits, followed by `MN\r\n` to signal completion.

---

## Flags Reference

### Common Flags (All Commands)

| Flag | Token Type | Description |
|------|------------|-------------|
| `b` | - | Base64-encoded key |
| `k` | - | Return key in response |
| `O<token>` | string(32) | Opaque token for request matching |
| `q` | - | Quiet mode (suppress nominal responses) |

### Metadata Flags (mg, ma)

| Flag | Token Type | Description |
|------|------------|-------------|
| `c` | - | Return CAS value |
| `f` | - | Return client flags |
| `s` | - | Return size |
| `t` | - | Return TTL remaining |
| `v` | - | Return value data |
| `h` | - | Return hit-before status |
| `l` | - | Return last-access time |

### Storage Flags (ms)

| Flag | Token Type | Description |
|------|------------|-------------|
| `F<token>` | uint32 | Client flags value |
| `T<token>` | int32 | TTL in seconds |
| `M<token>` | char | Mode (S/E/R/A/P) |
| `I` | - | Invalidate mode |

### Modification Flags

| Flag | Token Type | Description |
|------|------------|-------------|
| `C<token>` | uint64 | CAS compare value |
| `E<token>` | uint64 | CAS explicit value |
| `T<token>` | int32 | TTL update |

### Arithmetic Flags (ma)

| Flag | Token Type | Description |
|------|------------|-------------|
| `D<token>` | uint64 | Delta value |
| `M<token>` | char | Mode (I/+/D/-) |
| `N<token>` | int32 | Auto-create TTL |
| `J<token>` | uint64 | Initial value |

### Response-Only Flags

| Flag | Description |
|------|-------------|
| `W` | Win flag (exclusive recache rights) |
| `X` | Stale flag (item marked as stale) |
| `Z` | Already-won flag (another client has `W`) |

---

## Edge Cases and Error Handling

### Key Validation

**Valid keys:**
- 1-250 bytes
- ASCII printable characters (no whitespace, control chars)
- Or base64-encoded with `b` flag

**Invalid keys result in:**
- Empty key: `EN` or `CLIENT_ERROR`
- Key > 250 bytes: `CLIENT_ERROR bad command line format`
- Key with whitespace: Treated as end of key (protocol error)

**Experiment result:**
```
mg \r\n
→ EN\r\n  (empty key treated as miss)

ms bbbb...bbb(251 chars) 5\r\nhello\r\n
→ CLIENT_ERROR bad command line format\r\n
```

### Value Size

**Zero-length values:**
```
ms emptykey 0\r\n\r\n
→ HD\r\n
```

**Size mismatch:**
If declared size doesn't match actual data, connection enters error state.

### TTL Edge Cases

**TTL=0 or omitted:**
```
ms key 5 T0\r\nhello\r\n
mg key t\r\n
→ HD t-1\r\n  (-1 means infinite)
```

**TTL=-1 (explicit infinite):**
```
ms key 5 T-1\r\nhello\r\n
→ HD\r\n
```

### CAS Edge Cases

- CAS=0 on new items created without explicit `E` flag
- CAS increments on every modification
- CAS comparison is exact uint64 match
- Invalid CAS (non-existent) returns `EX`

### Mode Conflicts

**Replace on non-existent:**
```
md key\r\n
ms key 5 MR\r\nhello\r\n
→ NS\r\n  (not NF - different semantics)
```

**Append/Prepend without N flag:**
```
md key\r\n
ms key 5 MA\r\nhello\r\n
→ NF\r\n  (returns NS in practice but should be NF)
```

### Connection State

**After CLIENT_ERROR:**
- Connection should be closed
- Protocol state may be corrupted
- Do not attempt recovery

**After SERVER_ERROR:**
- Connection may be retried
- Request stack must be managed for response matching

### Opaque Tokens

- Maximum 32 bytes
- Alphanumeric strings recommended
- Reflected exactly in response
- No validation of token content

---

## Pipelining

### Basic Pipelining

Multiple commands can be sent without waiting for responses:

```
ms key1 5\r\n
hello\r\n
ms key2 5\r\n
world\r\n
mg key1 v\r\n
mg key2 v\r\n
```

Responses arrive in order:
```
HD\r\n
HD\r\n
VA 5\r\n
hello\r\n
VA 5\r\n
world\r\n
```

### Quiet Mode Pipelining

With `q` flag, nominal responses are suppressed:

```
mg key1 v q\r\n
mg key2 v q\r\n
mg key3 v q\r\n
mn\r\n
```

Only hits return responses:
```
VA 5\r\n
hello\r\n
VA 5\r\n
world\r\n
MN\r\n
```

### Opaque Tokens for Request Matching

```
mg key1 v O1\r\n
mg key2 v O2\r\n
mg key3 v O3\r\n
```

Responses include opaque:
```
VA 5 O1\r\n
val1\r\n
VA 5 O2\r\n
val2\r\n
EN O3\r\n
```

### Error Handling in Pipelines

**CLIENT_ERROR:**
```
mg key1 v\r\n
invalid command\r\n
mg key2 v\r\n
```

Response:
```
VA 5\r\n
value\r\n
ERROR\r\n
```

**Action**: Close connection immediately after `CLIENT_ERROR`.

### Performance Optimizations

1. **Use quiet mode** to reduce response bytes
2. **Use opaque tokens** instead of key reflection to save bandwidth
3. **Batch commands** to reduce syscalls
4. **End pipelines with `mn`** for deterministic completion detection

---

## Implementation Notes

### Response Flag Order

Flags in responses appear in the same order as requested:

```
mg key k c t s v\r\n
→ VA 5 kkey c100 t3600 s5\r\n
  value\r\n
```

Order is: k → c → t → s (then value due to `v`)

### Stale-While-Revalidate State Machine

1. **Normal state**: Item exists, not marked stale
   - `mg` returns no `X` or `W` flags

2. **Mark stale**: `md key I T30`
   - Item remains in cache
   - CAS is bumped

3. **First access after stale**:
   - `mg` returns `W` (win) and `X` (stale) flags
   - Client has exclusive recache rights

4. **Subsequent accesses**:
   - `mg` returns `Z` (already won) and `X` (stale) flags
   - Clients know to use stale data or wait

5. **Recache**: Client updates with `ms` using CAS
   - If CAS matches, `X` flag clears
   - If CAS doesn't match, item stays stale

### Arithmetic Behavior

- Values are stored as ASCII decimal strings
- Server parses as uint64 on arithmetic operations
- Non-numeric values cause protocol errors
- Results are written back as ASCII decimal

### Base64 Keys

- Use `b` flag on all operations (get, set, delete, arithmetic)
- Key in response also uses `b` flag
- Allows binary or UTF-8 keys
- Client must base64-encode before sending
- Adds ~33% overhead on key size

---

## Advanced Flag Behaviors

### `h` Flag (Hit Before)

Returns whether an item has been accessed since being stored.

**Behavior:**
- Returns `h0` immediately after `ms` (not yet hit)
- Returns `h1` after first `mg` access with value
- Accessing without `v` flag still counts as a hit

**Example:**
```
ms key 5\r\nhello\r\n
→ HD\r\n

mg key h\r\n
→ HD h0\r\n

mg key v\r\n
→ VA 5\r\nhello\r\n

mg key h\r\n
→ HD h1\r\n
```

**Use Case:** Detect if cached items are actually being used.

### `l` Flag (Last Access Time)

Returns seconds since item was last accessed.

**Behavior:**
- Returns `l0` immediately after access
- Increments over time
- Reset by any access (unless `u` flag used)
- Time is approximate (not exact)

**Example:**
```
ms key 5\r\nhello\r\n
mg key l\r\n
→ HD l0\r\n

# Wait 2 seconds
mg key l\r\n
→ HD l2\r\n
```

**Use Case:** Identify stale entries, implement custom eviction policies.

### `u` Flag (Don't Bump LRU)

Access item without updating LRU position or last access time.

**Behavior:**
- Item is not moved to front of LRU
- Last access time is NOT updated
- Hit count is NOT incremented
- Value is still returned normally

**Access Behavior Matrix:**

| Operation | Bump LRU | Update Time | Update Hit |
|-----------|----------|-------------|------------|
| `mg key v` | Yes | Yes | Yes |
| `mg key v u` | No | No | No |
| `mg key` (no v) | Yes | Yes | Yes |
| `mg key u` (no v) | No | No | No |

**Example:**
```
ms key 5\r\nhello\r\n
mg key v\r\n          # Normal access

# Wait 2 seconds
mg key v u\r\n        # Access without bump
mg key l\r\n
→ HD l2\r\n          # Still shows 2 seconds
```

**Use Cases:**
- Background replication processes
- Health checks
- Monitoring without affecting cache behavior

### `x` Flag (Remove Value, Keep Item)

In `md` command, removes the value but keeps the item structure.

**Behavior:**
- Value is cleared (becomes empty/zero-length)
- **Client flags are reset to 0** (Important!)
- Size becomes 0
- CAS value is updated
- Item still exists (not deleted)
- TTL is preserved

**Example:**
```
ms key 10 F99\r\nhelloworld\r\n
→ HD\r\n

mg key v f s\r\n
→ VA 10 f99 s10\r\nhelloworld\r\n

md key x\r\n
→ HD\r\n

mg key v f s\r\n
→ VA 0 f0 s0\r\n\r\n
```

**Comparison: `x` vs `I` Flags**

| Aspect | `x` Flag | `I` Flag |
|--------|----------|----------|
| Value | Cleared | Preserved |
| Client Flags | Reset to 0 | Preserved |
| Stale Mark | No | Yes (X flag set) |
| CAS | Updated | Updated |
| TTL | Preserved | Can be updated with T |
| Win Flag | No | Yes (W on first access) |

**When to use:**
- **Use `x`**: Clear sensitive data immediately, free memory but keep cache slot
- **Use `I`**: Stale-while-revalidate, preserve client flags, win flag semantics

---

## Protocol Error Conditions

### Duplicate Flags

**Error:** `CLIENT_ERROR duplicate flag\r\n`

```
mg key v v c c\r\n
→ CLIENT_ERROR duplicate flag\r\n
```

**Action Required:** Close connection after CLIENT_ERROR.

### Conflicting Flags

**Error:** `CLIENT_ERROR invalid or duplicate flag\r\n`

```
ma counter v MI MD\r\n
→ CLIENT_ERROR invalid or duplicate flag\r\n
```

### Invalid Flag Syntax

**Error:** `CLIENT_ERROR invalid flag\r\n`

```
mg key @invalid\r\n
→ CLIENT_ERROR invalid flag\r\n
```

### Opaque Token Too Long

**Error:** `CLIENT_ERROR opaque token too long\r\n`

**Limit:** 32 bytes

```
mg key O{40 bytes}\r\n
→ CLIENT_ERROR opaque token too long\r\n
```

### Non-Numeric Arithmetic

**Error:** `CLIENT_ERROR cannot increment or decrement non-numeric value\r\n`

```
ms key 5\r\nhello\r\n
ma key v\r\n
→ CLIENT_ERROR cannot increment or decrement non-numeric value\r\n
```

**Action Required:** Close connection after CLIENT_ERROR.

---

## Mixed Protocol Compatibility

**Meta and Text protocols are fully compatible and can be mixed freely.**

### Text Protocol SET, Meta Protocol GET

```
set key 0 0 5\r\nhello\r\n
→ STORED\r\n

mg key v\r\n
→ VA 5\r\nhello\r\n
```

### Meta Protocol SET, Text Protocol GET

```
ms key 5\r\nworld\r\n
→ HD\r\n

get key\r\n
→ VALUE key 0 5\r\nworld\r\nEND\r\n
```

### Other Text Commands

The following text protocol commands work alongside meta commands:

- `stats\r\n` - Returns server statistics
- `flush_all\r\n` - Flushes all items (returns `OK\r\n`)
- `version\r\n` - Returns server version
- `quit\r\n` - Closes connection

**Example:**
```
stats\r\n
→ STAT pid 1\r\n
  STAT uptime 54\r\n
  STAT version 1.6.39\r\n
  ...
  END\r\n

flush_all\r\n
→ OK\r\n
```

**Note:** Text protocol uses `STORED`, `END`, `OK` etc., while meta protocol uses two-letter codes.

---

## Arithmetic Edge Cases

### Overflow Behavior

Incrementing maximum uint64 wraps to 0:

```
ms counter 20\r\n18446744073709551615\r\n
ma counter v\r\n
→ VA 1\r\n0\r\n
```

**This is expected behavior - uint64 overflow wraps around.**

### Underflow Protection

Decrementing stops at 0 (no wrap to max):

```
ms counter 1\r\n5\r\n
ma counter v MD D10\r\n
→ VA 1\r\n0\r\n
```

### Auto-Create with Append/Prepend

The `N` flag works with append/prepend modes:

```
ms key 6 MA N60\r\nworld!\r\n
→ HD\r\n

mg key v\r\n
→ VA 6\r\nworld!\r\n
```

---

## Win Flag Lifecycle Details

### State Machine

**1. Initial Invalidation**
```
ms key 5\r\nhello\r\n
md key I T30\r\n
→ HD\r\n
```

**2. First Access After Invalidation**
```
mg key v\r\n
→ VA 5 X W\r\n
  hello\r\n
```
Client receives: `X` (stale) + `W` (exclusive recache rights)

**3. Subsequent Accesses**
```
mg key v\r\n
→ VA 5 Z X\r\n
  hello\r\n
```
Other clients receive: `X` (stale) + `Z` (another client won)

**4. After Recache**
```
ms key 3\r\nnew\r\n
mg key v\r\n
→ VA 3\r\nnew\r\n
```
Win flag clears after successful update.

### Win Flag Expiration

The `W` flag is granted until:
1. The item is successfully recached
2. The item's TTL expires
3. The item is explicitly deleted

**There is no separate win flag timeout** - it's tied to the item's lifecycle.

---

## Protocol Limits Summary

| Item | Limit | Error Response |
|------|-------|----------------|
| Key length | 250 bytes | `CLIENT_ERROR bad command line format` |
| Opaque token | 32 bytes | `CLIENT_ERROR opaque token too long` |
| Value size | ~1MB (default) | System dependent |
| CAS value | uint64 (8 bytes) | N/A |
| Client flags | uint32 (4 bytes) | N/A |
| TTL | int32 seconds | N/A |

### Additional Protocol Notes

**Multiple spaces are tolerated:**
```
mg  key  v  c\r\n
→ VA 5 c11\r\nhello\r\n
```

**CAS value of 0 is invalid:**
```
ms key 5 C0\r\nvalue\r\n
→ EX\r\n
```

---

## Version History

- **1.6.0** - Meta protocol introduced (experimental)
- **1.6.10** - Protocol stabilized
- **1.6.27** - CAS override support (`E` flag) added
- **Current** - Protocol is stable and recommended for all new clients

---

## References

- [Official Memcached Protocol Documentation](https://github.com/memcached/memcached/blob/master/doc/protocol.txt)
- [Meta Protocol Wiki](https://github.com/memcached/memcached/wiki/MetaCommands)
- [Memcached Documentation Site](https://docs.memcached.org/protocols/meta/)

---

*This specification is based on memcached 1.6 and validated through experimental testing against memcached 1.6.x.*
