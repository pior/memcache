package meta

// CmdType represents a meta protocol command (2 characters).
type CmdType string

// FlagType represents a single-character flag identifier.
type FlagType byte

// StatusType represents a response status code (2 characters).
type StatusType string

// Protocol delimiters
const (
	// CRLF is the line terminator for the memcached protocol
	CRLF = "\r\n"

	// Space separates command tokens
	Space = " "
)

// Command codes (2 characters)
//
// Each command has specific valid flags and behaviors. See individual command
// documentation for Request construction patterns.
const (
	// CmdGet retrieves item data and metadata from cache.
	//
	// Wire format: mg <key> <flags>*\r\n
	//
	// Valid flags:
	//   - FlagReturnValue (v): Return value data (changes response from HD to VA)
	//   - FlagReturnCAS (c): Return CAS token
	//   - FlagReturnTTL (t): Return remaining TTL in seconds (-1 for infinite)
	//   - FlagReturnClientFlags (f): Return client flags (uint32)
	//   - FlagReturnSize (s): Return value size in bytes
	//   - FlagReturnHit (h): Return hit status (0 or 1)
	//   - FlagReturnLastAccess (l): Return seconds since last access
	//   - FlagReturnKey (k): Return key in response
	//   - FlagQuiet (q): Suppress miss response (EN) - useful for pipelining
	//   - FlagOpaque (O): Set opaque token for request matching
	//   - FlagBase64Key (b): Key is base64-encoded
	//   - FlagNoLRUBump (u): Don't update LRU or access time
	//   - FlagRecache (R): Recache threshold in seconds - returns W if TTL below
	//   - FlagVivify (N): Auto-create on miss with given TTL, returns W
	//
	// Response statuses:
	//   - VA <size>: Hit with value (when v flag used)
	//   - HD: Hit without value (no v flag)
	//   - EN: Miss
	//
	// Typical patterns:
	//
	//   Basic get with value:
	//     &Request{Command: CmdGet, Key: "mykey", Flags: []Flag{{Type: FlagReturnValue}}}
	//
	//   Get with metadata:
	//     &Request{Command: CmdGet, Key: "mykey", Flags: []Flag{
	//         {Type: FlagReturnValue},
	//         {Type: FlagReturnCAS},
	//         {Type: FlagReturnTTL},
	//     }}
	//
	//   Quiet get for pipelining (suppresses EN on miss):
	//     &Request{Command: CmdGet, Key: "mykey", Flags: []Flag{
	//         {Type: FlagReturnValue},
	//         {Type: FlagQuiet},
	//     }}
	//
	//   Stale-while-revalidate pattern:
	//     &Request{Command: CmdGet, Key: "mykey", Flags: []Flag{
	//         {Type: FlagReturnValue},
	//         {Type: FlagRecache, Token: "30"},  // Win if TTL < 30s
	//     }}
	CmdGet CmdType = "mg"

	// CmdSet stores data in cache with various modes.
	//
	// Wire format: ms <key> <size> <flags>*\r\n<data>\r\n
	//
	// Valid flags:
	//   - FlagTTL (T): Set TTL in seconds (0 = infinite)
	//   - FlagClientFlags (F): Set client flags (uint32)
	//   - FlagCAS (C): Compare-and-swap - only store if CAS matches
	//   - FlagMode (M): Storage mode (default: ModeSet)
	//   - FlagReturnCAS (c): Return new CAS value
	//   - FlagReturnKey (k): Return key in response
	//   - FlagQuiet (q): Suppress success response (HD)
	//   - FlagOpaque (O): Set opaque token for request matching
	//   - FlagBase64Key (b): Key is base64-encoded
	//   - FlagInvalidate (I): Mark as stale instead of storing
	//
	// Storage modes (with FlagMode):
	//   - ModeSet (S): Store unconditionally (default)
	//   - ModeAdd (E): Store only if key doesn't exist (returns NS if exists)
	//   - ModeReplace (R): Store only if key exists (returns NS if missing)
	//   - ModeAppend (A): Append to existing value (returns NF if missing)
	//   - ModePrepend (P): Prepend to existing value (returns NF if missing)
	//
	// Response statuses:
	//   - HD: Stored successfully
	//   - NS: Not stored (add/replace mode conditions not met)
	//   - NF: Not found (append/prepend on missing key)
	//   - EX: CAS mismatch
	//
	// Typical patterns:
	//
	//   Basic set with TTL:
	//     &Request{Command: CmdSet, Key: "mykey", Data: []byte("value"), Flags: []Flag{
	//         {Type: FlagTTL, Token: "3600"},  // 1 hour TTL
	//     }}
	//
	//   Add (only if not exists):
	//     &Request{Command: CmdSet, Key: "mykey", Data: []byte("value"), Flags: []Flag{
	//         {Type: FlagMode, Token: ModeAdd},
	//         {Type: FlagTTL, Token: "3600"},
	//     }}
	//
	//   CAS update:
	//     &Request{Command: CmdSet, Key: "mykey", Data: []byte("new"), Flags: []Flag{
	//         {Type: FlagCAS, Token: casValue},
	//         {Type: FlagTTL, Token: "3600"},
	//     }}
	//
	//   Set with client flags:
	//     &Request{Command: CmdSet, Key: "mykey", Data: []byte("value"), Flags: []Flag{
	//         {Type: FlagTTL, Token: "3600"},
	//         {Type: FlagClientFlags, Token: "123"},
	//     }}
	CmdSet CmdType = "ms"

	// CmdDelete deletes or invalidates items.
	//
	// Wire format: md <key> <flags>*\r\n
	//
	// Valid flags:
	//   - FlagCAS (C): Only delete if CAS matches
	//   - FlagTTL (T): Set TTL for invalidation (with I flag)
	//   - FlagInvalidate (I): Mark stale instead of deleting
	//   - FlagReturnKey (k): Return key in response
	//   - FlagQuiet (q): Suppress success response
	//   - FlagOpaque (O): Set opaque token for request matching
	//   - FlagBase64Key (b): Key is base64-encoded
	//
	// Response statuses:
	//   - HD: Deleted successfully
	//   - NF: Key not found
	//   - EX: CAS mismatch
	//
	// Typical patterns:
	//
	//   Basic delete:
	//     &Request{Command: CmdDelete, Key: "mykey"}
	//
	//   CAS delete:
	//     &Request{Command: CmdDelete, Key: "mykey", Flags: []Flag{
	//         {Type: FlagCAS, Token: casValue},
	//     }}
	//
	//   Invalidate (mark stale for stale-while-revalidate):
	//     &Request{Command: CmdDelete, Key: "mykey", Flags: []Flag{
	//         {Type: FlagInvalidate},
	//         {Type: FlagTTL, Token: "30"},  // Keep stale for 30s
	//     }}
	CmdDelete CmdType = "md"

	// CmdArithmetic performs atomic increment/decrement operations.
	//
	// Wire format: ma <key> <flags>*\r\n
	//
	// Valid flags:
	//   - FlagReturnValue (v): Return new value
	//   - FlagDelta (D): Delta amount (default: 1)
	//   - FlagMode (M): ModeIncrement (I/+) or ModeDecrement (D/-)
	//   - FlagInitialValue (J): Initial value if auto-creating
	//   - FlagVivify (N): Auto-create with given TTL if missing
	//   - FlagTTL (T): Set/update TTL
	//   - FlagReturnCAS (c): Return CAS value
	//   - FlagReturnTTL (t): Return remaining TTL
	//   - FlagReturnKey (k): Return key in response
	//   - FlagQuiet (q): Suppress response
	//   - FlagOpaque (O): Set opaque token for request matching
	//   - FlagBase64Key (b): Key is base64-encoded
	//
	// Arithmetic modes (with FlagMode):
	//   - ModeIncrement (I or +): Increment (default)
	//   - ModeDecrement (D or -): Decrement (stops at 0, no underflow)
	//
	// Response statuses:
	//   - VA <size>: Success with value (when v flag used)
	//   - HD: Success without value
	//   - NF: Key not found (and no auto-create)
	//
	// Typical patterns:
	//
	//   Increment by 1, return value:
	//     &Request{Command: CmdArithmetic, Key: "counter", Flags: []Flag{
	//         {Type: FlagReturnValue},
	//     }}
	//
	//   Increment by 5:
	//     &Request{Command: CmdArithmetic, Key: "counter", Flags: []Flag{
	//         {Type: FlagReturnValue},
	//         {Type: FlagDelta, Token: "5"},
	//     }}
	//
	//   Decrement:
	//     &Request{Command: CmdArithmetic, Key: "counter", Flags: []Flag{
	//         {Type: FlagReturnValue},
	//         {Type: FlagMode, Token: ModeDecrement},
	//     }}
	//
	//   Auto-create counter with initial value:
	//     &Request{Command: CmdArithmetic, Key: "counter", Flags: []Flag{
	//         {Type: FlagReturnValue},
	//         {Type: FlagVivify, Token: "3600"},      // Create with 1h TTL
	//         {Type: FlagInitialValue, Token: "100"}, // Start at 100
	//     }}
	CmdArithmetic CmdType = "ma"

	// CmdDebug returns human-readable internal metadata.
	//
	// Wire format: me <key> <flags>*\r\n
	//
	// Valid flags:
	//   - FlagBase64Key (b): Key is base64-encoded
	//
	// Response status:
	//   - ME: Debug info follows
	//
	// Typical pattern:
	//     &Request{Command: CmdDebug, Key: "mykey"}
	CmdDebug CmdType = "me"

	// CmdNoOp returns a static response, useful for pipelining.
	//
	// Wire format: mn\r\n
	//
	// This command takes no key or flags. Used to detect end of pipelined
	// responses when using quiet mode (q flag).
	//
	// Response status:
	//   - MN: No-op response
	//
	// Typical pattern:
	//     &Request{Command: CmdNoOp}
	CmdNoOp CmdType = "mn"

	// CmdStats returns server statistics (standard text protocol).
	//
	// Wire format: stats [args]\r\n
	//
	// This is not part of the meta protocol but part of the standard text protocol.
	// Response consists of multiple "STAT <name> <value>\r\n" lines followed by "END\r\n".
	//
	// Common arguments:
	//   - (none): General statistics
	//   - "items": Per-slab statistics
	//   - "slabs": Slab allocator statistics
	//   - "sizes": Item size statistics
	//   - "settings": Server settings
	//
	// Typical pattern:
	//     &Request{Command: CmdStats}
	CmdStats CmdType = "stats"
)

// Response status codes (2 characters)
const (
	// StatusHD indicates success with no value data returned (Header/Stored)
	StatusHD StatusType = "HD"

	// StatusVA indicates success with value data following (Value)
	StatusVA StatusType = "VA"

	// StatusEN indicates key not found - miss (End/Not Found)
	StatusEN StatusType = "EN"

	// StatusNF indicates key not found for operations requiring existing key (Not Found)
	StatusNF StatusType = "NF"

	// StatusNS indicates item was not stored (Not Stored)
	// Not an error - e.g., add on existing key, replace on missing key
	StatusNS StatusType = "NS"

	// StatusEX indicates CAS mismatch - item was modified (Exists)
	StatusEX StatusType = "EX"

	// StatusMN is the response to mn command (Meta No-op)
	StatusMN StatusType = "MN"

	// StatusME is the debug information response (Meta Debug)
	StatusME StatusType = "ME"
)

// Non-meta error responses (legacy protocol compatibility)
const (
	// ErrorGeneric is returned for unknown command or generic errors
	ErrorGeneric = "ERROR"

	// ErrorClientPrefix indicates client sent invalid data
	// CRITICAL: Connection should be closed after this error as protocol state may be corrupted
	ErrorClientPrefix = "CLIENT_ERROR"

	// ErrorServerPrefix indicates a server-side error
	// Connection may be retried but request must be tracked
	ErrorServerPrefix = "SERVER_ERROR"
)

// Stats command response prefixes (standard text protocol)
const (
	// StatPrefix is the prefix for each statistics line
	// Format: STAT <name> <value>\r\n
	StatPrefix = "STAT"

	// EndMarker indicates the end of a stats response
	EndMarker = "END"
)

// Request flags (single character, optionally followed by token)

// Universal flags (all commands)
const (
	// FlagBase64Key indicates key is base64-encoded
	FlagBase64Key FlagType = 'b'

	// FlagReturnKey returns the key in response
	FlagReturnKey FlagType = 'k'

	// FlagOpaque is followed by token (max 32 bytes) for request matching
	// Format: O<token>
	FlagOpaque FlagType = 'O'

	// FlagQuiet suppresses nominal responses (HD, EN, NF)
	// Errors are still returned
	FlagQuiet FlagType = 'q'
)

// Metadata retrieval flags (mg, ma)
const (
	// FlagReturnCAS returns the CAS value in response
	FlagReturnCAS FlagType = 'c'

	// FlagReturnClientFlags returns the client flags (uint32) in response
	FlagReturnClientFlags FlagType = 'f'

	// FlagReturnSize returns the value size in bytes
	FlagReturnSize FlagType = 's'

	// FlagReturnTTL returns the TTL remaining in seconds (-1 for infinite)
	FlagReturnTTL FlagType = 't'

	// FlagReturnValue returns the item value in data block
	// Response changes from HD to VA <size>
	FlagReturnValue FlagType = 'v'

	// FlagReturnHit returns whether item has been hit before (0 or 1)
	FlagReturnHit FlagType = 'h'

	// FlagReturnLastAccess returns time since last access in seconds
	FlagReturnLastAccess FlagType = 'l'
)

// Modification flags
const (
	// FlagCAS is followed by uint64 token for compare-and-swap
	// Format: C<cas_value>
	// Returns EX on mismatch
	FlagCAS FlagType = 'C'

	// FlagExplicitCAS is followed by uint64 token to set explicit CAS value
	// Format: E<cas_value>
	FlagExplicitCAS FlagType = 'E'

	// FlagTTL is followed by int32 token for TTL in seconds
	// Format: T<seconds>
	// 0 or omitted = infinite TTL
	FlagTTL FlagType = 'T'

	// FlagClientFlags is followed by uint32 token for client flags
	// Format: F<flags>
	FlagClientFlags FlagType = 'F'
)

// Meta Get specific flags
const (
	// FlagNoLRUBump prevents LRU bump and access time update
	FlagNoLRUBump FlagType = 'u'

	// FlagRecache is followed by seconds token
	// Format: R<seconds>
	// Returns W flag if TTL is below threshold
	FlagRecache FlagType = 'R'

	// FlagVivify is followed by seconds token for TTL
	// Format: N<seconds>
	// Creates stub item on miss, returns W flag
	FlagVivify FlagType = 'N'
)

// Meta Set specific flags
const (
	// FlagMode is followed by mode character
	// Format: M<mode>
	// See Mode* constants below
	FlagMode FlagType = 'M'

	// FlagInvalidate marks item as stale instead of storing/deleting
	FlagInvalidate FlagType = 'I'
)

// Storage modes (used with FlagMode in ms command)
const (
	// ModeSet stores unconditionally (default)
	ModeSet = "S"

	// ModeAdd stores only if key doesn't exist (returns NS if exists)
	ModeAdd = "E"

	// ModeReplace stores only if key exists (returns NS if missing)
	ModeReplace = "R"

	// ModeAppend appends data to existing value (returns NF if missing)
	ModeAppend = "A"

	// ModePrepend prepends data to existing value (returns NF if missing)
	ModePrepend = "P"
)

// Meta Arithmetic specific flags
const (
	// FlagDelta is followed by uint64 token for increment/decrement amount
	// Format: D<delta>
	// Default: 1
	FlagDelta FlagType = 'D'

	// FlagInitialValue is followed by uint64 token for initial value
	// Format: J<initial>
	// Used with auto-create on miss (default: 0)
	FlagInitialValue FlagType = 'J'
)

// Arithmetic modes (used with FlagMode in ma command)
const (
	// ModeIncrement increments the value (default)
	ModeIncrement = "I"

	// ModeIncrementAlt is alternative syntax for increment
	ModeIncrementAlt = "+"

	// ModeDecrement decrements the value (stops at 0, no underflow)
	ModeDecrement = "D"

	// ModeDecrementAlt is alternative syntax for decrement
	ModeDecrementAlt = "-"
)

// Meta Delete specific flags
const (
	// FlagRemoveValue removes value but keeps metadata, resets client flags to 0
	// Format: x
	FlagRemoveValue FlagType = 'x'
)

// Response-only flags (auto-generated by server)
const (
	// FlagWin indicates client has exclusive right to recache (Win flag)
	FlagWin FlagType = 'W'

	// FlagStale indicates item is marked as stale
	FlagStale FlagType = 'X'

	// FlagAlreadyWon indicates another client has already received W flag
	FlagAlreadyWon FlagType = 'Z'
)

// Protocol limits
const (
	// MaxKeyLength is the maximum key length in bytes
	// Keys exceeding this return CLIENT_ERROR
	MaxKeyLength = 250

	// MinKeyLength is the minimum key length in bytes
	MinKeyLength = 1

	// MaxOpaqueLength is the maximum opaque token length in bytes
	// Tokens exceeding this return CLIENT_ERROR
	MaxOpaqueLength = 32

	// MaxValueSize is the default maximum value size (configurable on server)
	MaxValueSize = 1024 * 1024 // 1 MB
)
