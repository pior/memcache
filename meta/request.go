package meta

import "strconv"

// Request represents a meta protocol request.
// This is a low-level container for request data without serialization logic.
// Fields map directly to protocol elements.
//
// See CmdGet, CmdSet, CmdDelete, CmdArithmetic, CmdDebug, and CmdNoOp
// for detailed documentation on valid flags and typical usage patterns.
type Request struct {
	// Command is the 2-character command code: mg, ms, md, ma, me, mn
	Command CmdType

	// Key is the cache key (1-250 bytes, no whitespace unless base64-encoded)
	// Empty for mn command
	Key string

	// Data is the value to store (for ms command only)
	// Size is derived from len(Data), not stored separately
	Data []byte

	// Flags is the serialized flags representation.
	//
	// It contains the exact bytes that appear after the key/size on the wire,
	// including the leading spaces (e.g. " v c t" or " T60 Oopaque").
	Flags Flags
}

// Flags is a serialized representation of meta protocol flags.
//
// The zero value is ready to use.
//
// It is optimized for:
//   - building flags with minimal allocations (e.g. appending integers directly)
//   - cheap encoding in WriteRequest (single write)
//   - simple lookup via linear scan (flags are typically short)
type Flags []byte

func (f Flags) IsEmpty() bool {
	return len(f) == 0
}

func (f *Flags) Reset() {
	*f = (*f)[:0]
}

func (f Flags) Clone() Flags {
	return append(Flags(nil), f...)
}

func (f *Flags) Add(flagType FlagType) {
	*f = append(*f, ' ', byte(flagType))
}

func (f *Flags) AddTokenBytes(flagType FlagType, token []byte) {
	*f = append(*f, ' ', byte(flagType))
	*f = append(*f, token...)
}

func (f *Flags) AddTokenString(flagType FlagType, token string) {
	*f = append(*f, ' ', byte(flagType))
	*f = append(*f, token...)
}

// Common TTL values cached to reduce allocations.
// Note: strconv.Itoa already caches 0-100, so we only cache larger values that are
// common in memcached usage.
var cachedInts = map[int]string{
	300:    "300",    // 5 minutes
	600:    "600",    // 10 minutes
	1800:   "1800",   // 30 minutes
	3600:   "3600",   // 1 hour
	7200:   "7200",   // 2 hours
	86400:  "86400",  // 1 day
	604800: "604800", // 1 week
}

func (f *Flags) AddInt(flagType FlagType, value int) {
	*f = append(*f, ' ', byte(flagType))
	if cached, ok := cachedInts[value]; ok {
		*f = append(*f, cached...)
		return
	}
	*f = strconv.AppendInt(*f, int64(value), 10)
}

func (f *Flags) AddInt64(flagType FlagType, value int64) {
	*f = append(*f, ' ', byte(flagType))
	*f = strconv.AppendInt(*f, value, 10)
}

func (f *Flags) AddUint64(flagType FlagType, value uint64) {
	*f = append(*f, ' ', byte(flagType))
	*f = strconv.AppendUint(*f, value, 10)
}

func (f Flags) Has(flagType FlagType) bool {
	_, ok := f.Get(flagType)
	return ok
}

// Get returns the token value for the first flag of the given type.
//
// ok is true if the flag is present.
// token is nil if the flag is present but has no token.
func (f Flags) Get(flagType FlagType) (token []byte, ok bool) {
	for i := 0; i < len(f); {
		i = flagsSkipSpaces(f, i)
		if i >= len(f) {
			return nil, false
		}

		t := FlagType(f[i])
		i++

		start := i
		for i < len(f) && f[i] != ' ' {
			i++
		}

		if t == flagType {
			if start == i {
				return nil, true
			}
			return f[start:i], true
		}
	}
	return nil, false
}

func flagsSkipSpaces(b []byte, idx int) int {
	for idx < len(b) && b[idx] == ' ' {
		idx++
	}
	return idx
}

// NewRequest creates a new meta protocol request.
//
// The key and data parameters are used according to the command type:
//   - CmdGet, CmdDelete, CmdArithmetic, CmdDebug: key required, data ignored
//   - CmdSet: key and data required
//   - CmdNoOp: key and data ignored
//
// Use the Add* methods on Request to add flags after creation.
//
// Usage:
//
//	// Get request with flags
//	req := NewRequest(CmdGet, "mykey", nil)
//	req.AddReturnValue()
//	req.AddReturnCAS()
//
//	// Set request with TTL
//	req = NewRequest(CmdSet, "mykey", []byte("value"))
//	req.AddTTL(3600)
//
//	// Delete request (no flags)
//	req = NewRequest(CmdDelete, "mykey", nil)
//
//	// NoOp request
//	req = NewRequest(CmdNoOp, "", nil)
func NewRequest(cmd CmdType, key string, data []byte) *Request {
	return &Request{
		Command: cmd,
		Key:     key,
		Data:    data,
	}
}

// HasFlag checks if the request contains a flag of the given type.
func (r *Request) HasFlag(flagType FlagType) bool {
	return r.Flags.Has(flagType)
}

// GetFlagToken returns the token value for the first flag of the given type.
func (r *Request) GetFlagToken(flagType FlagType) (token []byte, ok bool) {
	return r.Flags.Get(flagType)
}

// --- Typed flag methods ---
//
// All Add* methods return *Request for fluent chaining:
//
//	req := NewRequest(CmdGet, "key", nil).AddReturnValue().AddReturnCAS()
//
// Flags are unconditionally added on each call, even if already present.
// The caller is responsible for avoiding duplicate flags.

// Universal flags (all commands)

// AddOpaque adds the 'O' flag with an opaque token for request/response matching.
// Supported by: mg, ms, md, ma.
// Typical use: correlate responses in pipelined requests.
// Token: arbitrary string up to 32 bytes, e.g. "req-123", "abc".
// The flag is unconditionally added, even if already present.
func (r *Request) AddOpaque(token string) *Request {
	r.Flags.AddTokenString(FlagOpaque, token)
	return r
}

// AddQuiet adds the 'q' flag to suppress nominal responses (HD, EN, NF).
// Supported by: mg, ms, md, ma.
// Typical use: pipelining multiple requests and using mn (noop) to detect end.
// Error responses are still returned even with quiet mode.
// The flag is unconditionally added, even if already present.
func (r *Request) AddQuiet() *Request { r.Flags.Add(FlagQuiet); return r }

// AddBase64Key adds the 'b' flag indicating the key is base64-encoded.
// Supported by: mg, ms, md, ma, me.
// Typical use: keys containing whitespace or binary data.
// The flag is unconditionally added, even if already present.
func (r *Request) AddBase64Key() *Request { r.Flags.Add(FlagBase64Key); return r }

// AddReturnKey adds the 'k' flag to include the key in the response.
// Supported by: mg, ms, md, ma.
// Typical use: correlate responses in pipelined requests without using opaque.
// The flag is unconditionally added, even if already present.
func (r *Request) AddReturnKey() *Request { r.Flags.Add(FlagReturnKey); return r }

// Metadata retrieval flags (mg, ma)

// AddReturnValue adds the 'v' flag to return the item value in the response.
// Supported by: mg, ma.
// Typical use: retrieve item data on get, or new counter value on arithmetic.
// Changes response from HD to VA <size> followed by data block.
// The flag is unconditionally added, even if already present.
func (r *Request) AddReturnValue() *Request { r.Flags.Add(FlagReturnValue); return r }

// AddReturnCAS adds the 'c' flag to return the CAS (compare-and-swap) token.
// Supported by: mg, ms, ma.
// Typical use: optimistic locking, read-modify-write patterns.
// Response includes 'c' followed by uint64 CAS value, e.g. "c12345".
// The flag is unconditionally added, even if already present.
func (r *Request) AddReturnCAS() *Request { r.Flags.Add(FlagReturnCAS); return r }

// AddReturnTTL adds the 't' flag to return the remaining TTL in seconds.
// Supported by: mg, ma.
// Typical use: cache warming, monitoring item expiration.
// Response includes 't' followed by seconds (-1 for infinite), e.g. "t3600", "t-1".
// The flag is unconditionally added, even if already present.
func (r *Request) AddReturnTTL() *Request { r.Flags.Add(FlagReturnTTL); return r }

// AddReturnClientFlags adds the 'f' flag to return the client flags.
// Supported by: mg, ma.
// Typical use: retrieve application-specific metadata stored with the item.
// Response includes 'f' followed by uint32 value, e.g. "f0", "f123".
// The flag is unconditionally added, even if already present.
func (r *Request) AddReturnClientFlags() *Request { r.Flags.Add(FlagReturnClientFlags); return r }

// AddReturnSize adds the 's' flag to return the value size in bytes.
// Supported by: mg, ma.
// Typical use: check item size without fetching the full value.
// Response includes 's' followed by size, e.g. "s1024".
// The flag is unconditionally added, even if already present.
func (r *Request) AddReturnSize() *Request { r.Flags.Add(FlagReturnSize); return r }

// AddReturnHit adds the 'h' flag to return whether the item was previously accessed.
// Supported by: mg, ma.
// Typical use: cache analytics, identifying cold items.
// Response includes 'h' followed by 0 (never hit) or 1 (previously hit), e.g. "h0", "h1".
// The flag is unconditionally added, even if already present.
func (r *Request) AddReturnHit() *Request { r.Flags.Add(FlagReturnHit); return r }

// AddReturnLastAccess adds the 'l' flag to return seconds since last access.
// Supported by: mg, ma.
// Typical use: identify stale or frequently accessed items.
// Response includes 'l' followed by seconds, e.g. "l30".
// The flag is unconditionally added, even if already present.
func (r *Request) AddReturnLastAccess() *Request { r.Flags.Add(FlagReturnLastAccess); return r }

// Modification flags

// AddTTL adds the 'T' flag to set the item's time-to-live in seconds.
// Supported by: ms, md, ma.
// Typical use: set expiration on store, extend TTL on arithmetic.
// Token: seconds as integer, 0 means infinite TTL. Common values: 60, 300, 3600, 86400.
// The flag is unconditionally added, even if already present.
func (r *Request) AddTTL(seconds int) *Request { r.Flags.AddInt(FlagTTL, seconds); return r }

// AddCAS adds the 'C' flag for compare-and-swap conditional updates.
// Supported by: ms, md.
// Typical use: optimistic locking, prevent lost updates in read-modify-write.
// Token: CAS value from a previous get response, e.g. 12345.
// Returns EX status if the item's CAS doesn't match.
// The flag is unconditionally added, even if already present.
func (r *Request) AddCAS(value uint64) *Request { r.Flags.AddUint64(FlagCAS, value); return r }

// AddExplicitCAS adds the 'E' flag to set an explicit CAS value on store.
// Supported by: ms.
// Typical use: advanced use cases requiring specific CAS values.
// Token: CAS value to set, e.g. 12345.
// The flag is unconditionally added, even if already present.
func (r *Request) AddExplicitCAS(value uint64) *Request {
	r.Flags.AddUint64(FlagExplicitCAS, value)
	return r
}

// AddClientFlags adds the 'F' flag to set application-specific client flags.
// Supported by: ms.
// Typical use: store metadata like serialization format, compression type.
// Token: uint32 value, e.g. 0, 1, 123.
// The flag is unconditionally added, even if already present.
func (r *Request) AddClientFlags(flags uint32) *Request {
	r.Flags.AddInt(FlagClientFlags, int(flags))
	return r
}

// Get-specific flags

// AddNoLRUBump adds the 'u' flag to prevent LRU bump and access time update.
// Supported by: mg.
// Typical use: background scans, monitoring without affecting cache eviction order.
// The flag is unconditionally added, even if already present.
func (r *Request) AddNoLRUBump() *Request { r.Flags.Add(FlagNoLRUBump); return r }

// AddRecache adds the 'R' flag for stale-while-revalidate pattern.
// Supported by: mg.
// Typical use: early refresh of items approaching expiration.
// Token: threshold in seconds. If remaining TTL < threshold, response includes 'W' (win) flag.
// Only the first client gets 'W', others get 'Z' (already won). Common values: 30, 60.
// The flag is unconditionally added, even if already present.
func (r *Request) AddRecache(seconds int) *Request { r.Flags.AddInt(FlagRecache, seconds); return r }

// AddVivify adds the 'N' flag to auto-create a stub item on cache miss.
// Supported by: mg, ma.
// Typical use: cache-aside pattern with built-in locking to prevent thundering herd.
// Token: TTL for the stub item in seconds, e.g. 30, 60.
// On miss, creates stub and returns 'W' flag; subsequent requests get stale stub.
// The flag is unconditionally added, even if already present.
func (r *Request) AddVivify(seconds int) *Request { r.Flags.AddInt(FlagVivify, seconds); return r }

// Set-specific flags

// AddMode adds the 'M' flag with a custom storage mode.
// Supported by: ms, ma.
// Typical use: control store behavior (add, replace, append, prepend).
// Token: mode character. Use AddModeSet, AddModeAdd, etc. for type safety.
// The flag is unconditionally added, even if already present.
func (r *Request) AddMode(mode string) *Request { r.Flags.AddTokenString(FlagMode, mode); return r }

// AddModeSet adds 'MS' flag for unconditional store (default mode).
// Supported by: ms.
// Typical use: standard cache set operation.
// The flag is unconditionally added, even if already present.
func (r *Request) AddModeSet() *Request { r.Flags.AddTokenString(FlagMode, ModeSet); return r }

// AddModeAdd adds 'ME' flag to store only if key doesn't exist.
// Supported by: ms.
// Typical use: create-if-not-exists, distributed locking.
// Returns NS if key already exists.
// The flag is unconditionally added, even if already present.
func (r *Request) AddModeAdd() *Request { r.Flags.AddTokenString(FlagMode, ModeAdd); return r }

// AddModeReplace adds 'MR' flag to store only if key exists.
// Supported by: ms.
// Typical use: update existing items only.
// Returns NS if key doesn't exist.
// The flag is unconditionally added, even if already present.
func (r *Request) AddModeReplace() *Request { r.Flags.AddTokenString(FlagMode, ModeReplace); return r }

// AddModeAppend adds 'MA' flag to append data to existing value.
// Supported by: ms.
// Typical use: building lists, log aggregation.
// Returns NF if key doesn't exist.
// The flag is unconditionally added, even if already present.
func (r *Request) AddModeAppend() *Request { r.Flags.AddTokenString(FlagMode, ModeAppend); return r }

// AddModePrepend adds 'MP' flag to prepend data to existing value.
// Supported by: ms.
// Typical use: building lists with newest items first.
// Returns NF if key doesn't exist.
// The flag is unconditionally added, even if already present.
func (r *Request) AddModePrepend() *Request { r.Flags.AddTokenString(FlagMode, ModePrepend); return r }

// AddInvalidate adds the 'I' flag to mark item as stale instead of storing/deleting.
// Supported by: ms, md.
// Typical use: stale-while-revalidate pattern, graceful cache invalidation.
// Item remains readable but responses include 'X' (stale) flag.
// The flag is unconditionally added, even if already present.
func (r *Request) AddInvalidate() *Request { r.Flags.Add(FlagInvalidate); return r }

// Arithmetic-specific flags

// AddDelta adds the 'D' flag to set increment/decrement amount.
// Supported by: ma.
// Typical use: increment/decrement counters by values other than 1.
// Token: delta amount as uint64, e.g. 1, 5, 100. Default is 1 if not specified.
// The flag is unconditionally added, even if already present.
func (r *Request) AddDelta(amount uint64) *Request { r.Flags.AddUint64(FlagDelta, amount); return r }

// AddInitialValue adds the 'J' flag to set initial value for auto-created counters.
// Supported by: ma (with vivify flag).
// Typical use: initialize counters to non-zero values on first access.
// Token: initial value as uint64, e.g. 0, 100, 1000. Default is 0.
// The flag is unconditionally added, even if already present.
func (r *Request) AddInitialValue(value uint64) *Request {
	r.Flags.AddUint64(FlagInitialValue, value)
	return r
}

// AddModeIncrement adds 'MI' flag for increment mode (default).
// Supported by: ma.
// Typical use: incrementing counters.
// The flag is unconditionally added, even if already present.
func (r *Request) AddModeIncrement() *Request {
	r.Flags.AddTokenString(FlagMode, ModeIncrement)
	return r
}

// AddModeDecrement adds 'MD' flag for decrement mode.
// Supported by: ma.
// Typical use: decrementing counters. Stops at 0, no underflow.
// The flag is unconditionally added, even if already present.
func (r *Request) AddModeDecrement() *Request {
	r.Flags.AddTokenString(FlagMode, ModeDecrement)
	return r
}

// Delete-specific flags

// AddRemoveValue adds the 'x' flag to remove value but keep metadata.
// Supported by: md.
// Typical use: clear value while preserving item existence.
// Resets client flags to 0.
// The flag is unconditionally added, even if already present.
func (r *Request) AddRemoveValue() *Request { r.Flags.Add(FlagRemoveValue); return r }
