package meta

import (
	"strconv"
	"strings"
)

// Response represents a parsed meta protocol response.
// This is a low-level container for response data without parsing logic.
// Fields map directly to protocol elements.
type Response struct {
	// Status is the 2-character response code: HD, VA, EN, NF, NS, EX, MN, ME
	Status StatusType

	// Data is the value data (only present for VA responses and ME responses)
	// For VA responses, data is the item value
	// For ME responses, data contains debug key=value pairs (parse with ParseDebugParams)
	Data []byte

	// Flags contains all flags returned in the response.
	// Order matches the response wire order.
	Flags Flags

	// Error is set for non-meta error responses: ERROR, CLIENT_ERROR, SERVER_ERROR
	// When Error is set, other fields may be empty or invalid
	Error error
}

// IsSuccess returns true if the response indicates a successful operation.
// Success statuses: HD, VA, MN, ME
func (r *Response) IsSuccess() bool {
	switch r.Status {
	case StatusHD, StatusVA, StatusMN, StatusME:
		return true
	default:
		return false
	}
}

// IsMiss returns true if the response indicates a cache miss.
// Miss statuses: EN, NF
func (r *Response) IsMiss() bool {
	return r.Status == StatusEN || r.Status == StatusNF
}

// IsNotStored returns true if the response indicates item was not stored.
// This is not an error - e.g., add on existing key, replace on missing key
func (r *Response) IsNotStored() bool {
	return r.Status == StatusNS
}

// IsCASMismatch returns true if the response indicates a CAS mismatch.
func (r *Response) IsCASMismatch() bool {
	return r.Status == StatusEX
}

// HasValue returns true if the response includes value data.
// Only VA responses have values (and some ME responses)
func (r *Response) HasValue() bool {
	return r.Status == StatusVA && r.Data != nil
}

// HasError returns true if the response contains a protocol error.
// Protocol errors: ERROR, CLIENT_ERROR, SERVER_ERROR
func (r *Response) HasError() bool {
	return r.Error != nil
}

// HasFlag checks if the response contains a flag of the given type.
func (r *Response) HasFlag(flagType FlagType) bool {
	return r.Flags.Has(flagType)
}

// GetFlagToken returns the token value for the first flag of the given type.
//
// ok is true if the flag is present.
// token is nil if the flag is present but has no token.
func (r *Response) GetFlagToken(flagType FlagType) (token []byte, ok bool) {
	return r.Flags.Get(flagType)
}

// --- Typed flag getters ---

// Boolean flags (presence check)

// Win returns true if the response contains the W (win) flag.
// Win flag indicates client has exclusive right to recache.
func (r *Response) Win() bool {
	return r.Flags.Has(FlagWin)
}

// Stale returns true if the response contains the X (stale) flag.
// Stale flag indicates item is marked as stale.
func (r *Response) Stale() bool {
	return r.Flags.Has(FlagStale)
}

// AlreadyWon returns true if the response contains the Z (already won) flag.
// Already won flag indicates another client has already received the W flag.
func (r *Response) AlreadyWon() bool {
	return r.Flags.Has(FlagAlreadyWon)
}

// Typed getters (parse flag tokens)

// CAS returns the CAS token value from the response.
func (r *Response) CAS() (uint64, bool) {
	token, ok := r.Flags.Get(FlagReturnCAS)
	if !ok {
		return 0, false
	}
	v, err := strconv.ParseUint(string(token), 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// TTL returns the remaining TTL in seconds from the response.
// Returns -1 for infinite TTL.
func (r *Response) TTL() (int, bool) {
	token, ok := r.Flags.Get(FlagReturnTTL)
	if !ok {
		return 0, false
	}
	v, err := strconv.Atoi(string(token))
	if err != nil {
		return 0, false
	}
	return v, true
}

// ClientFlags returns the client flags value from the response.
func (r *Response) ClientFlags() (uint32, bool) {
	token, ok := r.Flags.Get(FlagReturnClientFlags)
	if !ok {
		return 0, false
	}
	v, err := strconv.ParseUint(string(token), 10, 32)
	if err != nil {
		return 0, false
	}
	return uint32(v), true
}

// Size returns the value size in bytes from the response.
func (r *Response) Size() (int, bool) {
	token, ok := r.Flags.Get(FlagReturnSize)
	if !ok {
		return 0, false
	}
	v, err := strconv.Atoi(string(token))
	if err != nil {
		return 0, false
	}
	return v, true
}

// Hit returns the hit status from the response (true if item was hit before).
func (r *Response) Hit() (bool, bool) {
	token, ok := r.Flags.Get(FlagReturnHit)
	if !ok {
		return false, false
	}
	return string(token) == "1", true
}

// LastAccess returns the seconds since last access from the response.
func (r *Response) LastAccess() (int, bool) {
	token, ok := r.Flags.Get(FlagReturnLastAccess)
	if !ok {
		return 0, false
	}
	v, err := strconv.Atoi(string(token))
	if err != nil {
		return 0, false
	}
	return v, true
}

// Key returns the key from the response (when k flag was requested).
func (r *Response) Key() ([]byte, bool) {
	return r.Flags.Get(FlagReturnKey)
}

// Opaque returns the opaque token from the response.
func (r *Response) Opaque() ([]byte, bool) {
	return r.Flags.Get(FlagOpaque)
}

// ParseDebugParams parses debug key=value pairs from ME response Data.
// ME responses contain debug information in the format: key=value key2=value2 ...
//
// Returns a map of parameter names to their values.
// Silently skips any malformed entries (tokens without '=').
//
// Example:
//
//	resp := &Response{
//	    Status: StatusME,
//	    Data:   []byte("size=1024 ttl=3600 flags=0"),
//	}
//	params := ParseDebugParams(resp.Data)
//	// params["size"] == "1024"
//	// params["ttl"] == "3600"
//	// params["flags"] == "0"
func ParseDebugParams(data []byte) map[string]string {
	if len(data) == 0 {
		return make(map[string]string)
	}

	params := make(map[string]string)
	parts := strings.Fields(string(data))

	for _, part := range parts {
		key, value, found := strings.Cut(part, "=")
		if found {
			params[key] = value
		}
		// Silently skip malformed entries
	}

	return params
}
