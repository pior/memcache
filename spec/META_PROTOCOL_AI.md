# Memcached Meta Protocol - AI Reference

**Version:** 1.6+ | **Format:** `CMD key [size] [flags]\r\n[data]\r\n` | **Response:** `CODE [flags]\r\n[data]\r\n`

## Commands

| Cmd | Purpose | Syntax | Example |
|-----|---------|--------|---------|
| `mg` | Get | `mg <key> [flags]` | `mg k v c\r\n` |
| `ms` | Set | `ms <key> <size> [flags]\r\n<data>\r\n` | `ms k 5 T60\r\nhello\r\n` |
| `md` | Delete | `md <key> [flags]` | `md k I T30\r\n` |
| `ma` | Arithmetic | `ma <key> [flags]` | `ma k v D5\r\n` |
| `me` | Debug | `me <key>` | `me k\r\n` |
| `mn` | No-op | `mn` | `mn\r\n` |

## Response Codes

| Code | When | Flags | Data |
|------|------|-------|------|
| `HD` | Success, no value | Yes | No |
| `VA` | Success, has value | Yes | Yes |
| `EN` | Not found (miss) | Yes | No |
| `NF` | Not found (ops) | Yes | No |
| `NS` | Not stored | Yes | No |
| `EX` | CAS mismatch | Yes | No |
| `MN` | No-op response | No | No |
| `ME` | Debug info | No | Yes |

**Errors:** `ERROR\r\n`, `CLIENT_ERROR <msg>\r\n`, `SERVER_ERROR <msg>\r\n` (close connection on CLIENT_ERROR)

## Flags - Request

### mg Flags

| Flag | Token | Effect | Response |
|------|-------|--------|----------|
| `v` | - | Return value | Value in VA response |
| `k` | - | Return key | `k<key>` in response |
| `c` | - | Return CAS | `c<uint64>` in response |
| `f` | - | Return client flags | `f<uint32>` in response |
| `s` | - | Return size | `s<bytes>` in response |
| `t` | - | Return TTL | `t<seconds>` or `t-1` (no TTL) |
| `h` | - | Hit before status | `h0` (miss) or `h1` (hit) before VA/EN |
| `l` | - | Last access time | `l<seconds>` since last access |
| `u` | - | No LRU bump | Prevents access time update |
| `O` | `<token>` | Opaque echo | `O<token>` in response (≤32 bytes) |
| `q` | - | Quiet mode | Suppress EN response (success only) |
| `b` | - | Base64 key | Key is base64 encoded |
| `R` | `<seconds>` | Recache if TTL < N | Returns `W` if TTL below threshold |
| `N` | `<seconds>` | Auto-vivify on miss | Creates item with TTL, returns `W` |
| `T` | `<seconds>` | Update TTL | Sets new TTL on access |

### ms Flags (Storage Modes)

| Mode | Flag | Behavior |
|------|------|----------|
| Set | `MS` or none | Store unconditionally (default) |
| Add | `ME` | Store only if key doesn't exist |
| Replace | `MR` | Store only if key exists |
| Append | `MA` | Append data to existing value |
| Prepend | `MP` | Prepend data to existing value |

### ms Flags (Other)

| Flag | Token | Effect |
|------|-------|--------|
| `C` | `<cas>` | Compare CAS (returns EX if mismatch) |
| `F` | `<uint32>` | Set client flags |
| `T` | `<seconds>` | Set TTL (0 = no expiry) |
| `O` | `<token>` | Opaque echo (≤32 bytes) |
| `q` | - | Quiet (suppress HD response) |
| `b` | - | Base64 key |
| `I` | - | Invalidate (mark stale) |

### md Flags

| Flag | Token | Effect | Notes |
|------|-------|--------|-------|
| `I` | - | Invalidate | Mark stale, don't delete |
| `T` | `<seconds>` | TTL for stale | With `I`, keeps item N seconds |
| `C` | `<cas>` | CAS check | Returns EX if mismatch |
| `O` | `<token>` | Opaque echo | ≤32 bytes |
| `q` | - | Quiet | Suppress HD/NF |
| `b` | - | Base64 key | - |
| `x` | - | Remove value | Keep metadata, reset client flags to 0 |

### ma Flags

| Flag | Token | Effect |
|------|-------|--------|
| `v` | - | Return new value |
| `D` | `<delta>` | Delta amount (default: 1) |
| `MI` | - | Mode: increment (default) |
| `MD` | - | Mode: decrement |
| `J` | `<initial>` | Initial value if missing |
| `N` | `<initial>` | Auto-create with value if missing |
| `T` | `<seconds>` | Set TTL |
| `C` | `<cas>` | CAS check |
| `O` | `<token>` | Opaque echo |
| `q` | - | Quiet |
| `b` | - | Base64 key |

## Flags - Response

| Flag | Token | Source | Meaning |
|------|-------|--------|---------|
| `k` | `<key>` | `k` flag | Echoed key |
| `c` | `<cas>` | `c` flag | CAS value (uint64, never 0) |
| `f` | `<flags>` | `f` flag | Client flags (uint32) |
| `s` | `<size>` | `s` flag | Value size in bytes |
| `t` | `<ttl>` | `t` flag | TTL in seconds (-1 = none) |
| `h` | `0/1` | `h` flag | Hit before: 0=miss, 1=hit |
| `l` | `<secs>` | `l` flag | Seconds since last access |
| `O` | `<token>` | `O` flag | Echoed opaque token |
| `W` | - | Auto | Win flag (exclusive recache rights) |
| `Z` | - | Auto | Recache winner (saw W before) |
| `X` | - | Auto | Stale item flag |

## Win Flag Lifecycle

**States:** Fresh → (Invalidate) → Stale+W → Stale+Z → Fresh

1. `md k I T30` → Item marked stale
2. `mg k v` → Returns `VA ... X W` (stale + win)
3. `mg k v` → Returns `VA ... X Z` (stale + winner)
4. `mg k v` → Returns `VA ... X Z` (winner persists)
5. `ms k ...` → Clears X/Z/W, item fresh again

**Note:** W→Z transition happens on second access. Z persists until item updated.

## Edge Cases

### Arithmetic
- **Overflow:** uint64 max + 1 → wraps to 0
- **Underflow:** 0 - 1 → stays at 0
- **Non-numeric:** Returns `CLIENT_ERROR`

### CAS
- **CAS=0:** Invalid, returns `EX` or `CLIENT_ERROR`
- **CAS mismatch:** Returns `EX`

### Keys
- **Length:** 1-250 bytes (251+ → `CLIENT_ERROR`)
- **Chars:** No whitespace/control unless `b` flag
- **Base64:** Use `b` flag for binary keys

### Flags
- **Duplicate:** `mg k v v` → Accepts (uses last/all)
- **Conflict:** `ma k MI MD` → `CLIENT_ERROR`
- **Invalid:** `mg k @bad` → `CLIENT_ERROR`
- **Opaque limit:** >32 bytes → `CLIENT_ERROR`

### Special Behaviors
- **`x` flag:** Removes value, resets client flags to 0 (not preserve like `I`)
- **`u` flag:** Prevents LRU bump and access time update
- **Multiple spaces:** Tolerated in commands
- **Mixed protocol:** Text and meta commands fully compatible

## Protocol Limits

| Item | Limit | Error |
|------|-------|-------|
| Key length | 1-250 bytes | `CLIENT_ERROR` |
| Opaque token | ≤32 bytes | `CLIENT_ERROR` |
| Value size | 1MB (default, configurable) | - |
| CAS value | >0 (uint64) | `EX` or `CLIENT_ERROR` |

## Pipelining

Use `q` flag + `mn` to pipeline:
```
mg k1 v q\r\n
mg k2 v q\r\n
mg k3 v\r\n
mn\r\n
```
Only last (non-quiet) and errors return responses. `MN\r\n` marks end.

## Stale-While-Revalidate Pattern

```
md k I T30\r\n          # Invalidate, keep 30s
HD\r\n
mg k v\r\n              # Get stale
VA 5 X W\r\n            # Stale + win → recache
hello\r\n
mg k v\r\n              # Get again
VA 5 X Z\r\n            # Stale + winner
hello\r\n
ms k 5 T60\r\n          # Update
fresh\r\n
HD\r\n
mg k v\r\n              # Get fresh
VA 5\r\n                # No X/Z/W
fresh\r\n
```

## Stampede Protection

```
mg k v N30\r\n          # Auto-vivify with 30s TTL on miss
EN W\r\n                # Miss + win → exclusive create
```

## Early Recache

```
ms k 5 T60\r\nhello\r\n
HD\r\n
# ... wait 40 seconds ...
mg k v R30\r\n          # Recache if TTL < 30s
VA 5 t20 W\r\n          # TTL is 20s, win granted
hello\r\n
```

## Error Handling

- **CLIENT_ERROR:** Close connection immediately
- **SERVER_ERROR:** Transient, may retry
- **ERROR:** Unknown command or generic error
- **Duplicate flags:** Usually tolerated (last wins or combined)
- **Conflicting flags:** `CLIENT_ERROR cannot coexist`

## Implementation Notes

1. **Flag order:** Response flags match request flag order
2. **CAS never 0:** 0 is invalid, server never generates
3. **TTL -1:** Means no expiration
4. **`x` vs `I`:** `x` resets client flags to 0, `I` preserves them
5. **`u` flag:** Prevents both LRU bump and access time update
6. **Quiet mode:** Suppresses EN/NF/HD but not errors
7. **Mixed protocol:** Text commands (set/get) and meta commands fully interoperable

## Quick Flag Combos

| Combo | Purpose |
|-------|---------|
| `mg k v c` | Get value + CAS for update |
| `ms k 5 C<cas> T60` | CAS update with TTL |
| `md k I T30` | Soft delete (stale 30s) |
| `ma k v D5` | Increment by 5, return value |
| `mg k v N30` | Auto-create on miss (stampede protection) |
| `mg k v R30` | Early recache if TTL < 30s |
| `mg k h l u` | Check hit/age without LRU bump |
| `md k x` | Remove value, reset flags |
