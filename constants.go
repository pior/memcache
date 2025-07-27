package memcache

// Meta protocol command types
const (
	// Meta commands (2-character codes)
	CmdMetaGet        = "mg" // Meta get command
	CmdMetaSet        = "ms" // Meta set command
	CmdMetaDelete     = "md" // Meta delete command
	CmdMetaArithmetic = "ma" // Meta arithmetic command
	CmdMetaDebug      = "me" // Meta debug command
	CmdMetaNoOp       = "mn" // Meta no-op command
)

// Meta protocol response codes (2-character codes)
const (
	// Success responses
	StatusHD = "HD" // Hit/stored - success for most operations
	StatusVA = "VA" // Value follows - success with value data
	StatusMN = "MN" // Meta no-op response
	StatusME = "ME" // Meta debug response

	// Error/miss responses
	StatusEN = "EN" // Not found/miss
	StatusNS = "NS" // Not stored
	StatusEX = "EX" // Exists (CAS mismatch)
	StatusNF = "NF" // Not found (for operations expecting item to exist)

	// Server errors
	StatusServerError = "SERVER_ERROR"
	StatusClientError = "CLIENT_ERROR"
	StatusError       = "ERROR"
)

// Meta protocol flags for requests
const (
	// Common flags
	FlagBase64     = "b" // Interpret key as base64 encoded binary value
	FlagCAS        = "c" // Return CAS value
	FlagFlags      = "f" // Return client flags
	FlagHit        = "h" // Return whether item has been hit before
	FlagKey        = "k" // Return key
	FlagLastAccess = "l" // Return time since last access in seconds
	FlagOpaque     = "O" // Opaque value with token
	FlagQuiet      = "q" // Use noreply/quiet semantics
	FlagSize       = "s" // Return item size
	FlagTTL        = "t" // Return TTL remaining in seconds
	FlagNoLRUBump  = "u" // Don't bump item in LRU
	FlagValue      = "v" // Return item value

	// Meta get specific flags
	FlagCASToken  = "C" // Compare CAS value (with token)
	FlagNewCAS    = "E" // Use token as new CAS value
	FlagVivify    = "N" // Vivify on miss with TTL token
	FlagRecache   = "R" // Win for recache if TTL below token
	FlagUpdateTTL = "T" // Update TTL with token

	// Meta set specific flags
	FlagClientFlags = "F" // Set client flags (with token)
	FlagInvalidate  = "I" // Invalidate/mark as stale
	FlagMode        = "M" // Mode switch (with token)
	FlagAutoVivify  = "N" // Auto-vivify on miss for append mode

	// Meta arithmetic specific flags
	FlagDelta        = "D" // Delta value (with token)
	FlagInitialValue = "J" // Initial value for auto-create (with token)

	// Meta delete specific flags
	FlagRemoveValue = "x" // Remove value but keep item
)

// Meta protocol response flags
const (
	// Response-only flags
	FlagWin   = "W" // Client has won recache flag
	FlagStale = "X" // Item is stale
	FlagOwned = "Z" // Item already has winning flag assigned
)

// Meta set mode tokens
const (
	ModeSet     = "S" // Set mode (default)
	ModeAdd     = "E" // Add mode (like "add" command)
	ModeReplace = "R" // Replace mode (like "replace" command)
	ModeAppend  = "A" // Append mode (like "append" command)
	ModePrepend = "P" // Prepend mode (like "prepend" command)
)

// Meta arithmetic mode tokens
const (
	ArithIncrement = "I" // Increment mode (default)
	ArithIncrAlias = "+" // Increment alias
	ArithDecrement = "D" // Decrement mode
	ArithDecrAlias = "-" // Decrement alias
)

// Protocol constants
const (
	MaxKeyLength    = 250     // Maximum key length in bytes
	MaxOpaqueLength = 32      // Maximum opaque token length
	MaxValueLength  = 1048576 // 1MB - typical memcached limit
)

// Error constants for protocol handling (additional to existing client errors)
var (
	ErrInvalidCommand = NewProtocolError("invalid command")
	ErrMalformedFlag  = NewProtocolError("malformed flag")
	ErrProtocolError  = NewProtocolError("protocol error")
)

// ProtocolError represents a protocol-level error
type ProtocolError struct {
	Message string
}

func (e *ProtocolError) Error() string {
	return "memcache protocol: " + e.Message
}

func NewProtocolError(message string) *ProtocolError {
	return &ProtocolError{Message: message}
}
