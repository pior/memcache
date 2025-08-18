## Summary of Connection Concurrency Fix

### Problem Identified
The original implementation had a critical concurrency issue:

1. **Multiple concurrent `ExecuteBatch` calls** could send commands to the same connection
2. **Batch-based response reading** assumed responses would arrive grouped by batch
3. **Race condition** where responses from different batches could be interleaved
4. **Incorrect response matching** leading to commands receiving wrong responses

### Example of the Issue
```
Thread 1: ExecuteBatch([cmd1, cmd2]) -> spawns goroutine to read 2 responses
Thread 2: ExecuteBatch([cmd3, cmd4]) -> spawns goroutine to read 2 responses

Network responses arrive in order: [resp_cmd3, resp_cmd1, resp_cmd2, resp_cmd4]

Thread 1's reader:
  - Reads resp_cmd3 -> assigns to cmd1 (WRONG!)
  - Reads resp_cmd1 -> assigns to cmd2 (WRONG!)

Thread 2's reader:
  - Reads resp_cmd2 -> assigns to cmd3 (WRONG!)
  - Reads resp_cmd4 -> assigns to cmd4 (WRONG!)
```

### Solution Implemented
Replaced batch-based reading with **global response matching**:

1. **Single reader goroutine** for the entire connection lifetime
2. **Global pending commands map** (`map[string]*protocol.Command`) indexed by opaque
3. **Register commands** in `ExecuteBatch` before sending
4. **Match responses** by opaque regardless of which batch they belong to
5. **Automatic cleanup** on connection close or errors

### Key Changes

#### Before (Problematic):
```go
// ExecuteBatch spawned a new goroutine per batch
go c.readResponsesAsync(commands)

// Each goroutine tried to read exactly len(commands) responses
func (c *Connection) readResponsesAsync(commands []*protocol.Command) {
    for range commands { // Assumes responses arrive in batch order
        resp := protocol.ReadResponse(c.reader)
        // Match within this batch only...
    }
}
```

#### After (Fixed):
```go
// ExecuteBatch registers commands globally
for _, cmd := range commands {
    c.pendingCommands[cmd.Opaque] = cmd
}

// Single reader loop matches any response to any pending command
func (c *Connection) readerLoop() {
    for {
        resp := protocol.ReadResponse(c.reader)
        if cmd, exists := c.pendingCommands[resp.Opaque]; exists {
            cmd.SetResponse(resp)
            delete(c.pendingCommands, resp.Opaque)
        }
    }
}
```

### Benefits
- ✅ **Thread-safe**: Concurrent `ExecuteBatch` calls work correctly
- ✅ **Correct response matching**: Each command gets its proper response
- ✅ **Resource efficient**: Single reader goroutine instead of one per batch
- ✅ **Proper cleanup**: All pending commands get error responses on connection close
- ✅ **No race conditions**: Global mutex protects shared state

### Tests Verify Fix
- `TestPipeliningOpaqueMatching` - ✅ PASS
- `TestPipeliningMultipleCommandsRandomOrder` - ✅ PASS
- `TestConnectionDeadlineHandling` - ✅ PASS
- All connection tests continue to pass

The fix ensures that concurrent memcache operations work correctly without response mismatching.
