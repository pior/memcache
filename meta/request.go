package meta

import "strconv"

// Pre-formatted strings for common integer values to avoid allocations
const (
	str0   = "0"
	str1   = "1"
	strNeg1 = "-1"
	str60  = "60"
	str300 = "300"
	str3600 = "3600"
	str86400 = "86400"
)

// formatInt returns a string representation of an integer.
// Uses pre-allocated constants for common values to avoid allocations.
func formatInt(value int) string {
	switch value {
	case 0:
		return str0
	case 1:
		return str1
	case -1:
		return strNeg1
	case 60:
		return str60
	case 300:
		return str300
	case 3600:
		return str3600
	case 86400:
		return str86400
	default:
		return strconv.Itoa(value)
	}
}

// FormatInt64 returns a string representation of an int64.
// Uses pre-allocated constants for common values to avoid allocations.
func FormatInt64(value int64) string {
	switch value {
	case 0:
		return str0
	case 1:
		return str1
	case -1:
		return strNeg1
	case 60:
		return str60
	case 300:
		return str300
	case 3600:
		return str3600
	case 86400:
		return str86400
	default:
		return strconv.FormatInt(value, 10)
	}
}

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
//   - 'T60' (with token): Flag{Type: FlagTTL, Token: "60"}
//   - 'Omytoken' (opaque): Flag{Type: FlagOpaque, Token: "mytoken"}
type Flag struct {
	// Type is the single-character flag identifier
	Type FlagType

	// Token is the optional value following the flag character
	// Empty string if flag has no token
	Token string
}

func FormatFlagInt(flagType FlagType, value int) Flag {
	return Flag{
		Type:  flagType,
		Token: formatInt(value),
	}
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
//	req := NewRequest(CmdGet, "mykey", nil, Flag{Type: FlagReturnValue})
//
//	// Set request
//	req := NewRequest(CmdSet, "mykey", []byte("value"), Flag{Type: FlagTTL, Token: "60"})
//
//	// Delete request
//	req := NewRequest(CmdDelete, "mykey", nil)
//
//	// NoOp request
//	req := NewRequest(CmdNoOp, "", nil)
func NewRequest(cmd CmdType, key string, data []byte, flags ...Flag) *Request {
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
