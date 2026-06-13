package memcache

import "time"

// maxRelativeTTL is the largest expiration value memcached treats as a
// relative duration (30 days). Larger values are interpreted by the server
// as absolute unix timestamps.
const maxRelativeTTL = 30 * 24 * time.Hour

// minAbsoluteExptime is the smallest exptime value the server reads as an
// absolute unix timestamp rather than a relative duration.
const minAbsoluteExptime = int64(maxRelativeTTL/time.Second) + 1

// TTL specifies when an item expires.
// The zero value (NoTTL) means the item never expires (it persists until
// evicted). Use ExpiresIn for an expiration relative to now, ExpiresAt for
// an absolute point in time.
type TTL struct {
	duration time.Duration
	at       time.Time
}

// NoTTL is the zero TTL: the item never expires (it persists until evicted).
var NoTTL = TTL{}

// ExpiresIn returns a TTL expiring d after the request is sent.
// Sub-second durations are rounded up to one second, memcached's resolution.
// Durations longer than 30 days are encoded on the wire as an absolute unix
// timestamp, as the memcached protocol requires; the meaning is unchanged.
// A non-positive d is NoTTL.
func ExpiresIn(d time.Duration) TTL {
	return TTL{duration: d}
}

// ExpiresAt returns a TTL expiring at time t.
// A t in the past expires the item immediately. A zero t is NoTTL.
func ExpiresAt(t time.Time) TTL {
	return TTL{at: t}
}

// Expiration encodes the TTL as memcached's exptime value: 0 for no
// expiration, relative seconds up to 30 days, and an absolute unix timestamp
// beyond that. Relative durations longer than 30 days are converted to an
// absolute timestamp against now.
func (t TTL) Expiration(now time.Time) int {
	if !t.at.IsZero() {
		if unix := t.at.Unix(); unix >= minAbsoluteExptime {
			return int(unix)
		}
		// Timestamps this old (before 1970-01-31) would be read by the
		// server as relative durations; they are in the distant past, so
		// encode the oldest valid absolute timestamp: already expired.
		return int(minAbsoluteExptime)
	}
	if t.duration <= 0 {
		return 0
	}
	seconds := int((t.duration + time.Second - 1) / time.Second)
	if t.duration > maxRelativeTTL {
		return int(now.Unix()) + seconds
	}
	return seconds
}
