// The results of the client's operations: what an operation reports back, and
// the vocabulary it reports it in. The inputs live in options.go.

package memcache

import "fmt"

// CAS is a compare-and-swap token, memcached's optimistic-concurrency handle
// (its ETag). A CAS is obtained from Get (or any operation that returns one)
// and supplied back through an options struct to make a write conditional. The
// zero value means "no precondition": stores apply unconditionally and deletes
// ignore CAS.
type CAS uint64

// Status reports how an operation ended: it took effect, or the reason it did
// not. Every result type carries one — [Item], [StoreResult] and [Counter] in
// their Status field, Delete and Touch as their return value — and
// [Status.OK] is the one way to ask whether an operation took effect.
//
// It is the interpreted, public counterpart to the wire-level meta.StatusType:
// it preserves the distinction the raw status alone would lose (a store "not
// stored" could mean the key was missing, already existed, or the CAS
// mismatched).
//
// The zero value is not a status: it means none was reported, which happens
// only on the zero value of a result type.
type Status int

const (
	StatusApplied     Status = iota + 1 // the operation took effect: a read found the key, a write or delete applied
	StatusNotFound                      // the key did not exist
	StatusExists                        // the key already existed (add on existing key)
	StatusCASMismatch                   // the CAS precondition did not match
)

// OK reports whether the operation took effect. It is the single spelling of
// that question across the API: item.Status.OK(), result.Status.OK(),
// counter.Status.OK(), and the Status returned by Delete and Touch.
//
// A failed precondition is not OK, whether the key was missing, already
// present, or carried another CAS; read the Status itself to tell those apart.
func (s Status) OK() bool { return s == StatusApplied }

func (s Status) String() string {
	switch s {
	case StatusApplied:
		return "Applied"
	case StatusNotFound:
		return "NotFound"
	case StatusExists:
		return "Exists"
	case StatusCASMismatch:
		return "CASMismatch"
	default:
		return fmt.Sprintf("Status(%d)", int(s))
	}
}

// Item is what a read returns. Writes take the key and value as arguments
// (Set, Add, ...) or a SetItem (MultiSet).
type Item struct {
	Key   string
	Value []byte
	Flags uint32 // client flags stored alongside the value
	CAS   CAS    // compare-and-swap token

	// Status is StatusApplied when the key was found and StatusNotFound when
	// it was not; Value, Flags and CAS are only set on a hit. Test it with
	// Status.OK(), as on every other result type.
	Status Status
}

// StoreResult reports the result of a store. CAS is the item's token after a
// successful store, for chaining a follow-up conditional write without a reread.
type StoreResult struct {
	// Status is StatusApplied when the value was stored, or the condition
	// that stopped it: StatusExists (add on an existing key), StatusNotFound
	// (replace, append or prepend on a missing key) or StatusCASMismatch.
	Status Status

	CAS CAS
}

// Counter is the result of an arithmetic operation.
type Counter struct {
	Key   string
	Value uint64
	CAS   CAS

	// Status is StatusApplied when the counter was read (or created), or the
	// condition that stopped it: StatusNotFound (miss without Create) or
	// StatusCASMismatch. Value is only set on StatusApplied — a CAS mismatch
	// leaves the key in place but returns nothing about it.
	Status Status
}
