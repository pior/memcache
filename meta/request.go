package meta

import (
	"strconv"
	"time"
)

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

func (f *Flags) AddDurationSeconds(flagType FlagType, d time.Duration) {
	f.AddInt64(flagType, int64(d/time.Second))
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
// Usage:
//
//	// Get request
//	var flags Flags
//	flags.Add(FlagReturnValue)
//	req := NewRequest(CmdGet, "mykey", nil, flags)
//
//	// Set request
//	flags.Reset()
//	flags.AddInt(FlagTTL, 60)
//	req = NewRequest(CmdSet, "mykey", []byte("value"), flags)
//
//	// Delete request (no flags)
//	req = NewRequest(CmdDelete, "mykey", nil, Flags{})
//
//	// NoOp request
//	req = NewRequest(CmdNoOp, "", nil, Flags{})
func NewRequest(cmd CmdType, key string, data []byte, flags Flags) *Request {
	return &Request{
		Command: cmd,
		Key:     key,
		Data:    data,
		Flags:   flags,
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

func (r *Request) AddFlag(flagType FlagType) {
	r.Flags.Add(flagType)
}

func (r *Request) AddFlagTokenBytes(flagType FlagType, token []byte) {
	r.Flags.AddTokenBytes(flagType, token)
}

func (r *Request) AddFlagTokenString(flagType FlagType, token string) {
	r.Flags.AddTokenString(flagType, token)
}

func (r *Request) AddFlagInt(flagType FlagType, value int) {
	r.Flags.AddInt(flagType, value)
}
