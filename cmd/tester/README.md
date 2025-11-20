# Memcache Load Tester

A comprehensive load testing tool for memcache servers that validates client behavior under intensive concurrent activity.

## Building

```bash
go build
```

## Usage

```bash
./tester [options]
```

### Options

- `-addr string` - Memcache server address (default: "127.0.0.1:11211")
- `-concurrency int` - Number of concurrent workers (default: 10)
- `-cycles int` - Number of cycles to run, 0 = infinite (default: 0)
- `-duration duration` - Duration per check (default: 5s)

### Examples

**Quick test with 5 workers for 2 seconds per check:**
```bash
./tester -cycles 1 -duration 2s -concurrency 5
```

**Continuous load test with 50 workers:**
```bash
./tester -concurrency 50 -duration 10s
```

**Target specific server:**
```bash
./tester -addr 192.168.1.100:11211 -concurrency 20
```

## Test Checks

The tester runs the following checks sequentially in each cycle:

1. **Set/Get** - Basic set and get operations, validates value integrity
2. **Add** - Tests add semantics (fails if key exists)
3. **Set/Delete/Get** - Tests delete operation and miss behavior
4. **Increment** - Tests counter increment with auto-vivify
5. **Decrement** - Tests counter decrement (negative deltas)
6. **Increment with TTL** - Tests counter with expiration
7. **Mixed Operations** - Random mix of all operations
8. **Large Values** - Tests with 50-100KB values
9. **Binary Data** - Tests binary data integrity
10. **TTL Behavior** - Tests expiration settings

## Output

The tester displays real-time statistics during each check:
- **ops** - Total operations completed
- **ops/sec** - Current operation rate
- **Success** - Successful operations
- **Miss** - Cache misses (expected for some checks)
- **Fail** - Failed assertions (unexpected behavior)
- **Errors** - Connection or protocol errors

### Example Output

```
=== Cycle 1 ===

[Set/Get]
Running: 12752 ops (8924 ops/sec) | Success: 12752 | Miss: 0 | Fail: 0 | Errors: 0
Completed: 17099 ops in 2s (8549 ops/sec) | Success: 17099 | Miss: 0 | Fail: 0 | Errors: 0

[Increment]
Running: 8231 ops (5460 ops/sec) | Success: 8231 | Miss: 0 | Fail: 0 | Errors: 0
Completed: 11045 ops in 2s (5522 ops/sec) | Success: 11045 | Miss: 0 | Fail: 0 | Errors: 0
```

## Assertions

Each check validates responses and prints detailed messages for unexpected behavior:
- Value mismatches
- Missing keys after set
- Incorrect counter values
- Binary data corruption
- Size mismatches

## Stopping

Press `Ctrl+C` to gracefully stop the tester. It will complete the current check and exit.
