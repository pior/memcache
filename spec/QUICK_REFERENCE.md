# Meta Protocol Quick Reference

## Commands

### Meta Get (mg)
```
mg <key> [flags]\r\n
```

**Common patterns:**
```bash
mg key v          # Get value
mg key v k c t    # Get value, key, CAS, TTL
mg key v N60      # Vivify on miss (stampede protection)
mg key v R30      # Early recache if TTL < 30s
mg key v q        # Quiet mode (no EN on miss)
mg key v O123     # With opaque token
```

### Meta Set (ms)
```
ms <key> <size> [flags]\r\n
<data>\r\n
```

**Common patterns:**
```bash
ms key 5 T60\r\nhello\r\n          # Set with 60s TTL
ms key 5 F30\r\nhello\r\n          # Set with client flags=30
ms key 5 ME\r\nhello\r\n           # Add (only if not exists)
ms key 5 MR\r\nhello\r\n           # Replace (only if exists)
ms key 5 MA\r\nworld\r\n           # Append
ms key 5 MP\r\nhello\r\n           # Prepend
ms key 5 C123 c\r\nhello\r\n      # CAS update, return new CAS
ms key 5 q\r\nhello\r\n            # Quiet mode
```

### Meta Delete (md)
```
md <key> [flags]\r\n
```

**Common patterns:**
```bash
md key               # Delete
md key I T30         # Invalidate (mark stale) for 30s
md key C123          # Delete with CAS check
md key q             # Quiet mode
```

### Meta Arithmetic (ma)
```
ma <key> [flags]\r\n
```

**Common patterns:**
```bash
ma counter v               # Increment by 1, return value
ma counter v D5            # Increment by 5
ma counter v MD D3         # Decrement by 3
ma counter v N60           # Auto-create on miss, TTL=60s
ma counter v N60 J100      # Auto-create with initial=100
ma counter v C123          # With CAS check
```

### Meta Debug (me)
```
me <key> [b]\r\n
→ ME <key> <metadata>\r\n
```

### Meta No-Op (mn)
```
mn\r\n
→ MN\r\n
```

## Flags

### Universal Flags
- `b` - Base64-encoded key
- `k` - Return key in response
- `O<token>` - Opaque token (max 32 bytes)
- `q` - Quiet mode (suppress nominal responses)

### Metadata Flags
- `c` - Return/set CAS value
- `f` - Return/set client flags
- `s` - Return size
- `t` - Return TTL
- `v` - Return value
- `h` - Return hit-before status (0/1)
- `l` - Return last-access time (seconds)

### Modification Flags
- `T<n>` - Set/update TTL (seconds)
- `C<n>` - Compare CAS
- `E<n>` - Explicit CAS value
- `F<n>` - Client flags value

### Mode Flags (ms)
- `MS` - Set (default)
- `ME` - Add (error if exists)
- `MR` - Replace (error if missing)
- `MA` - Append
- `MP` - Prepend
- `I` - Invalidate mode (with CAS)

### Mode Flags (ma)
- `MI` or `M+` - Increment (default)
- `MD` or `M-` - Decrement
- `D<n>` - Delta value
- `N<n>` - Auto-create with TTL
- `J<n>` - Initial value for auto-create

### Recache Flags (mg)
- `N<n>` - Vivify on miss with TTL
- `R<n>` - Recache if TTL < n
- `u` - Don't bump LRU

### Response Flags
- `W` - Win (exclusive recache rights)
- `X` - Stale (item marked as stale)
- `Z` - Already won (another client has W)

## Response Codes

- `HD` - Success (header only)
- `VA <size>` - Success with value
- `EN` - Miss (not found)
- `NF` - Not found (for operations)
- `NS` - Not stored
- `EX` - CAS mismatch
- `MN` - No-op response
- `ME` - Debug response

## Error Responses

- `ERROR` - Unknown command
- `CLIENT_ERROR <msg>` - Invalid request (close connection)
- `SERVER_ERROR <msg>` - Server issue

## Common Patterns

### Get with All Metadata
```
mg key k c f t s v\r\n
→ VA 5 kkey c123 f30 t3600 s5\r\n
  value\r\n
```

### Stampede Protection
```
mg key v N30\r\n
→ VA 0 W\r\n          # Miss, created stub, win flag
  \r\n
```

### Stale-While-Revalidate
```
# Mark stale
md key I T30\r\n
→ HD\r\n

# First client gets win
mg key v c\r\n
→ VA 5 c100 W X\r\n
  value\r\n

# Others get already-won
mg key v c\r\n
→ VA 5 c100 Z X\r\n
  value\r\n
```

### Early Recache
```
mg key v R30\r\n
→ VA 5 t25 W\r\n      # TTL=25s < 30s, win flag
  value\r\n
```

### Pipelining with Quiet Mode
```
mg key1 v q\r\n
mg key2 v q\r\n
mg key3 v q\r\n
mn\r\n
```

Only hits and MN respond:
```
VA 5\r\n
val1\r\n
MN\r\n
```

### CAS Update
```
# Get current CAS
mg key c\r\n
→ HD c100\r\n

# Update only if CAS matches
ms key 5 C100 c\r\n
value\r\n
→ HD c101\r\n

# Update with old CAS fails
ms key 5 C100\r\n
value\r\n
→ EX\r\n
```

### Increment Counter
```
# Create counter
ms counter 1\r\n
0\r\n
→ HD\r\n

# Increment
ma counter v D5\r\n
→ VA 1\r\n
  5\r\n

# Auto-create on miss
ma newcounter v N60 J100\r\n
→ VA 3\r\n
  100\r\n
```

### Append/Prepend
```
ms key 5\r\n
hello\r\n
→ HD\r\n

ms key 6 MA\r\n
 world\r\n
→ HD\r\n

mg key v\r\n
→ VA 11\r\n
  hello world\r\n
```

## Constraints

- **Key**: 1-250 bytes, no whitespace (unless base64)
- **Value**: 0 to 1MB (default limit)
- **TTL**: -1 (infinite), 0 (infinite), or positive seconds
- **CAS**: 64-bit unsigned integer
- **Client Flags**: 32-bit unsigned integer
- **Delta**: 64-bit unsigned integer
- **Opaque**: Up to 32 bytes
- **Arithmetic**: Values are uint64, decrement stops at 0

## Tips

1. Use `q` flag for bulk operations to reduce traffic
2. Use opaque tokens (`O`) instead of `k` for long keys
3. End pipelines with `mn` for deterministic completion
4. Close connection after `CLIENT_ERROR`
5. Use `N` flag for stampede protection
6. Use `R` flag for early recache
7. Use `I` flag for stale-while-revalidate
8. Always check response codes
9. Handle `W`, `X`, `Z` flags for cache patterns
10. Use base64 (`b`) for binary or UTF-8 keys
