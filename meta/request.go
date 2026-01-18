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

	// Flags contains all flags and their tokens for the request
	// Order is preserved for proper serialization
	Flags []Flag
}

// Flag represents a single protocol flag with optional token.
// Examples:
//   - 'v' (no token): Flag{Type: FlagReturnValue}
//   - 'T60' (with token): Flag{Type: FlagTTL, Token: []byte("60")}
//   - 'Omytoken' (opaque): Flag{Type: FlagOpaque, Token: []byte("mytoken")}
type Flag struct {
	// Type is the single-character flag identifier
	Type FlagType

	// Token is the optional value following the flag character.
	// Nil if flag has no token.
	Token []byte
}

// Common TTL values cached to reduce allocations.
// Note: strconv.Itoa already caches 0-100, so we only cache larger TTL values
// that are common in memcached usage.
var cachedInts = map[int][]byte{
	300:    []byte("300"),    // 5 minutes
	600:    []byte("600"),    // 10 minutes
	1800:   []byte("1800"),   // 30 minutes
	3600:   []byte("3600"),   // 1 hour
	7200:   []byte("7200"),   // 2 hours
	86400:  []byte("86400"),  // 1 day
	604800: []byte("604800"), // 1 week
}

func FormatFlagInt(flagType FlagType, value int) Flag {
	token, cached := cachedInts[value]
	if !cached {
		token = []byte(strconv.Itoa(value))
	}
	return Flag{
		Type:  flagType,
		Token: token,
	}
}

func FormatFlagInt64(flagType FlagType, value int64) Flag {
	// Use strconv.FormatInt for correctness; allocates like before.
	return Flag{Type: flagType, Token: []byte(strconv.FormatInt(value, 10))}
}

func FormatFlagUint64(flagType FlagType, value uint64) Flag {
	return Flag{Type: flagType, Token: []byte(strconv.FormatUint(value, 10))}
}

func FormatFlagString(flagType FlagType, token string) Flag {
	return Flag{Type: flagType, Token: []byte(token)}
}

func (f Flag) TokenString() string {
	return string(f.Token)
}

// NewRequest creates a new meta protocol request.
//
// The key and data parameters are used according to the command type:
//   - CmdGet, CmdDelete, CmdArithmetic, CmdDebug: key required, data ignored
//   - CmdSet: key and data required
//   - CmdNoOp: key and data ignored
//
// flags can be nil to avoid allocation for requests without flags.
//
// Usage:
//
//	// Get request
//	req := NewRequest(CmdGet, "mykey", nil, []Flag{{Type: FlagReturnValue}})
//
//	// Set request
//	req := NewRequest(CmdSet, "mykey", []byte("value"), []Flag{FormatFlagInt(FlagTTL, 60)})
//
//	// Delete request (no flags)
//	req := NewRequest(CmdDelete, "mykey", nil, nil)
//
//	// NoOp request
//	req := NewRequest(CmdNoOp, "", nil, nil)
func NewRequest(cmd CmdType, key string, data []byte, flags []Flag) *Request {
	return &Request{
		Command: cmd,
		Key:     key,
		Data:    data,
		Flags:   flags,
	}
}

// HasFlag checks if the request contains a flag of the given type.
// Useful for validation or introspection.
func (r *Request) HasFlag(flagType FlagType) bool {
	for _, f := range r.Flags {
		if f.Type == flagType {
			return true
		}
	}
	return false
}

// GetFlag returns the first flag of the given type and true if found.
// Returns zero Flag and false if not found.
func (r *Request) GetFlag(flagType FlagType) (Flag, bool) {
	for _, f := range r.Flags {
		if f.Type == flagType {
			return f, true
		}
	}
	return Flag{}, false
}

// AddFlag appends a flag to the request.
// Useful for building requests programmatically.
func (r *Request) AddFlag(flag Flag) {
	r.Flags = append(r.Flags, flag)
}
