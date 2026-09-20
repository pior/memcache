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

// StoreResult reports the result of a store. CAS is the item's token after a
// successful store, for chaining a follow-up conditional write without a reread.
type StoreResult struct {
	// Status is StatusApplied when the value was stored, or the condition
	// that stopped it: StatusExists (add on an existing key), StatusNotFound
	// (replace, append or prepend on a missing key) or StatusCASMismatch.
	Status Status

	CAS CAS
}

// StoreOptions modifies Set, Add, and Replace. The zero value stores with no
// expiration, no client flags, and no CAS precondition.
type StoreOptions struct {
	TTL   TTL
	Flags uint32 // client flags stored with the item; zero: none
	CAS   CAS    // require the item's CAS to match; zero: store unconditionally
}

// ConcatOptions modifies Append and Prepend. The zero value requires the key
// to exist; a missing key reports StatusNotFound.
type ConcatOptions struct {
	CAS          CAS
	CreateOnMiss bool   // create the key when it is absent (memcached's autovivify)
	TTL          TTL    // expiration of the created item; ignored unless CreateOnMiss
	Flags        uint32 // client flags of the created item; ignored unless CreateOnMiss
}

// GetOptions modifies Get. The zero value reads without altering the item.
type GetOptions struct {
	TTL TTL // non-zero: touch-on-read, updating the item's expiration (get-and-touch)
}

// DeleteOptions modifies Delete.
type DeleteOptions struct {
	CAS CAS // require the item's CAS to match; zero: delete unconditionally
}

// CounterOptions modifies Increment and Decrement. The zero value requires the
// key to exist; a missing key reports StatusNotFound.
type CounterOptions struct {
	TTL     TTL
	CAS     CAS
	Create  bool   // create the key when it is absent, seeded with Initial
	Initial uint64 // value of the created key; ignored unless Create
}

// firstOpt returns the single options value, or the zero value when none is
// given. The variadic form keeps the common call site free of an empty struct;
// it accepts at most one value, and passing more is a programming error.
func firstOpt[T any](opts []T) T {
	switch len(opts) {
	case 0:
		var zero T
		return zero
	case 1:
		return opts[0]
	default:
		panic(fmt.Sprintf("memcache: at most one %T may be passed, got %d", opts[0], len(opts)))
	}
}
