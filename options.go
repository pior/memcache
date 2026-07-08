package memcache

import "fmt"

// CAS is a compare-and-swap token, memcached's optimistic-concurrency handle
// (its ETag). A CAS is obtained from Get (or any operation that returns one)
// and supplied back through an options struct to make a write conditional. The
// zero value means "no precondition": stores apply unconditionally and deletes
// ignore CAS.
type CAS uint64

// Status reports whether a conditional operation took effect, and if not, why.
// It is the interpreted, public counterpart to the wire-level meta.StatusType:
// it preserves the distinction the raw status alone would lose (a store "not
// stored" could mean the key was missing, already existed, or the CAS
// mismatched).
type Status int

const (
	Applied     Status = iota // the write or delete took effect
	NotFound                  // the key did not exist
	Exists                    // the key already existed (add on existing key)
	CASMismatch               // the CAS precondition did not match
)

// OK reports whether the operation took effect.
func (s Status) OK() bool { return s == Applied }

// NotFound reports whether the key was absent.
func (s Status) NotFound() bool { return s == NotFound }

// Exists reports whether the key already existed (add on an existing key).
func (s Status) Exists() bool { return s == Exists }

// CASMismatch reports whether the CAS precondition did not match.
func (s Status) CASMismatch() bool { return s == CASMismatch }

func (s Status) String() string {
	switch s {
	case Applied:
		return "Applied"
	case NotFound:
		return "NotFound"
	case Exists:
		return "Exists"
	case CASMismatch:
		return "CASMismatch"
	default:
		return fmt.Sprintf("Status(%d)", int(s))
	}
}

// StoreResult reports the result of a store. CAS is the item's token after a
// successful store, for chaining a follow-up conditional write without a reread.
type StoreResult struct {
	Status Status
	CAS    CAS
}

// Stored reports whether the item was written.
func (r StoreResult) Stored() bool { return r.Status.OK() }

// StoreOptions modifies Set, Add, and Replace. The zero value stores with no
// expiration, no client flags, and no CAS precondition.
type StoreOptions struct {
	TTL   TTL
	Flags uint32 // client flags stored with the item; zero: none
	CAS   CAS    // require the item's CAS to match; zero: store unconditionally
}

// ConcatOptions modifies Append and Prepend. There is no TTL field: the server
// ignores expiration on these modes, which never overwrite the item.
type ConcatOptions struct {
	CAS          CAS
	CreateOnMiss *CreateOnMiss // non-nil: create the key when it is absent
}

// CreateOnMiss describes the item to create when Append, Prepend, or a counter
// reaches a missing key (memcached's autovivify).
type CreateOnMiss struct {
	TTL   TTL
	Flags uint32
}

// GetOptions modifies Get. The zero value reads without altering the item.
type GetOptions struct {
	TTL TTL // non-zero: touch-on-read, updating the item's expiration (get-and-touch)
}

// DeleteOptions modifies Delete.
type DeleteOptions struct {
	CAS CAS // require the item's CAS to match; zero: delete unconditionally
}

// CounterOptions modifies Increment and Decrement.
type CounterOptions struct {
	TTL     TTL
	CAS     CAS
	Initial *uint64 // non-nil: create on miss seeded with this value; nil: don't create
}

// firstOpt returns the single options value, or the zero value when none is
// given. The variadic form keeps the common call site free of an empty struct
// while accepting at most one options value.
func firstOpt[T any](opts []T) T {
	var zero T
	if len(opts) > 0 {
		return opts[0]
	}
	return zero
}
