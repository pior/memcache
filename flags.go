package memcache

import "strconv"

// MetaFlag represents a meta protocol flag and its optional argument.
type MetaFlag string

// Meta protocol flag helpers (all documented flags)
// See: https://docs.memcached.org/protocols/meta/

// b: interpret key as base64 encoded binary value
func FlagBinary() MetaFlag { return MetaFlag("b") }

// c: return item cas token
func FlagReturnCAS() MetaFlag { return MetaFlag("c") }

// f: return client flags token
func FlagReturnClientFlags() MetaFlag { return MetaFlag("f") }

// h: return whether item has been hit before as a 0 or 1
func FlagReturnHit() MetaFlag { return MetaFlag("h") }

// k: return key as a token
func FlagReturnKey() MetaFlag { return MetaFlag("k") }

// l: return time since item was last accessed in seconds
func FlagReturnLastAccess() MetaFlag { return MetaFlag("l") }

// O(token): opaque value, consumes a token and copies back with response
func FlagOpaque(token string) MetaFlag { return MetaFlag("O" + token) }

// q: use noreply semantics for return codes
func FlagNoReply() MetaFlag { return MetaFlag("q") }

// s: return item size token
func FlagReturnSize() MetaFlag { return MetaFlag("s") }

// t: return item TTL remaining in seconds (-1 for unlimited)
func FlagReturnTTL() MetaFlag { return MetaFlag("t") }

// u: don't bump the item in the LRU
func FlagNoLRUBump() MetaFlag { return MetaFlag("u") }

// v: return item value in <data block>
func FlagReturnValue() MetaFlag { return MetaFlag("v") }

// E(token): use token as new CAS value if item is modified
func FlagSetCAS(token string) MetaFlag { return MetaFlag("E" + token) }

// N(token): vivify on miss, takes TTL as a argument
func FlagVivify(ttl int) MetaFlag { return MetaFlag("N" + strconv.Itoa(ttl)) }

// R(token): if remaining TTL is less than token, win for recache
func FlagRecacheIfBelow(ttl int) MetaFlag { return MetaFlag("R" + strconv.Itoa(ttl)) }

// T(token): update remaining TTL (or set TTL for set/delete/arithmetic)
func FlagSetTTL(ttl int) MetaFlag { return MetaFlag("T" + strconv.Itoa(ttl)) }

// W: client has "won" the recache flag (response only)
func FlagReturnWon() MetaFlag { return MetaFlag("W") }

// X: item is stale (response only)
func FlagReturnStale() MetaFlag { return MetaFlag("X") }

// Z: item has already sent a winning flag (response only)
func FlagReturnAlreadyWon() MetaFlag { return MetaFlag("Z") }

// C(token): compare CAS value when storing item
func FlagCompareCAS(token string) MetaFlag { return MetaFlag("C" + token) }

// F(token): set client flags to token (32 bit unsigned numeric)
func FlagSetClientFlags(flags uint32) MetaFlag {
	return MetaFlag("F" + strconv.FormatUint(uint64(flags), 10))
}

// I: invalidate. set-to-invalid if supplied CAS is older than item's CAS
func FlagInvalidate() MetaFlag { return MetaFlag("I") }

// M(token): mode switch to change behavior (add, replace, append, prepend, set, incr, decr)
func FlagMode(mode byte) MetaFlag { return MetaFlag("M" + string(mode)) }

// S(token): data length for ms (meta set) if not using value
func FlagSetDataLength(length int) MetaFlag { return MetaFlag("S" + strconv.Itoa(length)) }

// x: removes the item value, but leaves the item (meta delete)
func FlagRemoveValue() MetaFlag { return MetaFlag("x") }

// D(token): delta to apply (arithmetic)
func FlagDelta(delta uint64) MetaFlag { return MetaFlag("D" + strconv.FormatUint(delta, 10)) }

// J(token): initial value to use if auto created after miss (arithmetic)
func FlagInitialValue(val uint64) MetaFlag { return MetaFlag("J" + strconv.FormatUint(val, 10)) }

// P(token): proxy hint (ignored by memcached)
func FlagProxyHint(hint string) MetaFlag { return MetaFlag("P" + hint) }

// L(token): path hint (ignored by memcached)
func FlagPathHint(hint string) MetaFlag { return MetaFlag("L" + hint) }

// S: return the size of the stored item on success (meta set)
func FlagReturnStoredSize() MetaFlag { return MetaFlag("s") }

// A: append mode (meta set)
func FlagModeAppend() MetaFlag { return FlagMode('A') }

// P: prepend mode (meta set)
func FlagModePrepend() MetaFlag { return FlagMode('P') }

// E: add mode (meta set)
func FlagModeAdd() MetaFlag { return FlagMode('E') }

// R: replace mode (meta set)
func FlagModeReplace() MetaFlag { return FlagMode('R') }

// S: set mode (meta set)
func FlagModeSet() MetaFlag { return FlagMode('S') }

// I: increment mode (arithmetic)
func FlagModeIncr() MetaFlag { return FlagMode('I') }

// D: decrement mode (arithmetic)
func FlagModeDecr() MetaFlag { return FlagMode('D') }

// +: increment mode (arithmetic, alias)
func FlagModeIncrAlias() MetaFlag { return FlagMode('+') }

// -: decrement mode (arithmetic, alias)
func FlagModeDecrAlias() MetaFlag { return FlagMode('-') }
