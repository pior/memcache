package protocol

type CmdType string

// Meta protocol command types
const (
	// Meta commands (2-character codes)
	CmdGet        CmdType = "mg" // Meta get command
	CmdSet        CmdType = "ms" // Meta set command
	CmdDelete     CmdType = "md" // Meta delete command
	CmdArithmetic CmdType = "ma" // Meta arithmetic command
	CmdDebug      CmdType = "me" // Meta debug command
	CmdNoOp       CmdType = "mn" // Meta no-op command
)

type StatusType string

// Meta protocol response codes (2-character codes)
const (
	// Success responses
	StatusHD StatusType = "HD" // Hit/stored - success for most operations
	StatusVA StatusType = "VA" // Value follows - success with value data
	StatusMN StatusType = "MN" // Meta no-op response
	StatusME StatusType = "ME" // Meta debug response

	// Error/miss responses
	StatusEN StatusType = "EN" // Not found/miss
	StatusNS StatusType = "NS" // Not stored
	StatusEX StatusType = "EX" // Exists (CAS mismatch)
	StatusNF StatusType = "NF" // Not found (for operations expecting item to exist)

	// Server errors
	StatusServerError StatusType = "SERVER_ERROR"
	StatusClientError StatusType = "CLIENT_ERROR"
	StatusError       StatusType = "ERROR"
)

type FlagType string

// Meta protocol flags for requests
const (
	// Common flags
	FlagBase64     FlagType = "b" // Interpret key as base64 encoded binary value
	FlagCAS        FlagType = "c" // Return CAS value
	FlagFlags      FlagType = "f" // Return client flags
	FlagHit        FlagType = "h" // Return whether item has been hit before
	FlagKey        FlagType = "k" // Return key
	FlagLastAccess FlagType = "l" // Return time since last access in seconds
	FlagOpaque     FlagType = "O" // Opaque value with token
	FlagQuiet      FlagType = "q" // Use noreply/quiet semantics
	FlagSize       FlagType = "s" // Return item size
	FlagTTL        FlagType = "t" // Return TTL remaining in seconds
	FlagNoLRUBump  FlagType = "u" // Don't bump item in LRU
	FlagValue      FlagType = "v" // Return item value

	// Meta get specific flags
	FlagCASToken  FlagType = "C" // Compare CAS value (with token)
	FlagNewCAS    FlagType = "E" // Use token as new CAS value
	FlagVivify    FlagType = "N" // Vivify on miss with TTL token
	FlagRecache   FlagType = "R" // Win for recache if TTL below token
	FlagUpdateTTL FlagType = "T" // Update TTL with token

	// Meta set specific flags
	FlagClientFlags FlagType = "F" // Set client flags (with token)
	FlagInvalidate  FlagType = "I" // Invalidate/mark as stale
	FlagMode        FlagType = "M" // Mode switch (with token)
	FlagAutoVivify  FlagType = "N" // Auto-vivify on miss for append mode
	FlagSetTTL      FlagType = "T" // Set the key expiration (with token)

	// Meta arithmetic specific flags
	FlagDelta        FlagType = "D" // Delta value (with token)
	FlagInitialValue FlagType = "J" // Initial value for auto-create (with token)

	// Meta delete specific flags
	FlagRemoveValue FlagType = "x" // Remove value but keep item
)

// Meta protocol response flags
const (
	// Response-only flags
	FlagWin   FlagType = "W" // Client has won recache flag
	FlagStale FlagType = "X" // Item is stale
	FlagOwned FlagType = "Z" // Item already has winning flag assigned
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
