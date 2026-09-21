package memcache

import "fmt"

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
