package meta

import "strings"

// ResponseFlag represents a single protocol flag returned by the server.
//
// Token is returned as a byte slice to avoid allocating strings in the hot path.
// The returned slice is backed by a response-owned buffer (not the bufio.Reader
// internal buffer), so it is safe to keep beyond ReadResponse.
//
// Token should be treated as immutable.
type ResponseFlag struct {
	// Type is the single-character flag identifier
	Type FlagType

	// Token is the optional value following the flag character.
	// Nil if flag has no token.
	Token []byte
}

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
	Flags []ResponseFlag

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
	for _, f := range r.Flags {
		if f.Type == flagType {
			return true
		}
	}
	return false
}

// GetFlag returns the first flag of the given type and true if found.
// Returns zero ResponseFlag and false if not found.
func (r *Response) GetFlag(flagType FlagType) (ResponseFlag, bool) {
	for _, f := range r.Flags {
		if f.Type == flagType {
			return f, true
		}
	}
	return ResponseFlag{}, false
}

// GetFlagToken returns the token value for the first flag of the given type.
// Returns nil if flag not found or has no token.
func (r *Response) GetFlagToken(flagType FlagType) []byte {
	if flag, ok := r.GetFlag(flagType); ok {
		return flag.Token
	}
	return nil
}

// GetFlagTokenString returns the token as a string.
//
// This allocates if the token is backed by bytes (typical for ReadResponse).
func (r *Response) GetFlagTokenString(flagType FlagType) string {
	return string(r.GetFlagToken(flagType))
}

// HasWinFlag returns true if the response contains the W (win) flag.
// Win flag indicates client has exclusive right to recache.
func (r *Response) HasWinFlag() bool {
	return r.HasFlag(FlagWin)
}

// HasStaleFlag returns true if the response contains the X (stale) flag.
// Stale flag indicates item is marked as stale.
func (r *Response) HasStaleFlag() bool {
	return r.HasFlag(FlagStale)
}

// HasAlreadyWonFlag returns true if the response contains the Z (already won) flag.
// Already won flag indicates another client has already received the W flag.
func (r *Response) HasAlreadyWonFlag() bool {
	return r.HasFlag(FlagAlreadyWon)
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
