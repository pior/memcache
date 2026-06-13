// Package workload defines the load generator's key/value scheme, the
// key-embedding desync invariant, and the operation mix.
//
// The core invariant: every stored value embeds its key as
// "stress:lt:<id>|<padding>". Any read whose value does not start with the
// requested key's "<key>|" prefix proves the connection desynchronized — the
// worst failure for a cache client. The scheme is self-contained: loadgen reads
// the values loadgen wrote, so it needs no agreement with the main module's
// stress suite.
package workload

import (
	"math/rand/v2"
	"strconv"
	"strings"
)

// KeyPrefix namespaces all keys this harness uses and makes the numeric id
// recoverable from a key or value (so op-log records can store a uint32 id
// rather than the full string).
const KeyPrefix = "stress:lt:"

const sep = "|"

// maxPadding bounds the random value size; values range from len(key)+1 up to
// len(key)+1+maxPadding bytes.
const maxPadding = 512

// Key renders a numeric key id into its wire string.
func Key(keyID int) string {
	return KeyPrefix + strconv.Itoa(keyID)
}

// Value builds the canonical key-embedding value for keyID, padded to a random
// size so responses split unpredictably across socket reads.
func Value(keyID int, rng *rand.Rand) []byte {
	key := Key(keyID)
	pad := rng.IntN(maxPadding)
	b := make([]byte, 0, len(key)+1+pad)
	b = append(b, key...)
	b = append(b, sep...)
	for range pad {
		b = append(b, 'x')
	}
	return b
}

// CheckValue returns an error if value violates the key-embedding invariant for
// keyID. A non-nil return means the connection delivered another key's data.
func CheckValue(keyID int, value []byte) error {
	want := Key(keyID) + sep
	if !strings.HasPrefix(string(value), want) {
		return &DesyncError{KeyID: keyID, Got: truncate(string(value), 80)}
	}
	return nil
}

// ParseKeyID recovers the numeric id embedded in a key or value, e.g.
// "stress:lt:42|xxxx" -> 42. ok is false if the input is not in the scheme.
func ParseKeyID(b []byte) (keyID int, ok bool) {
	s := string(b)
	if !strings.HasPrefix(s, KeyPrefix) {
		return 0, false
	}
	s = s[len(KeyPrefix):]
	if i := strings.IndexByte(s, sep[0]); i >= 0 {
		s = s[:i]
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
