package memcache

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pior/memcache/internal/testutils"
	"github.com/pior/memcache/meta"
)

// serverScripts are seed streams for the client-level targets: well-formed
// replies, desynchronized streams, and truncated ones.
var serverScripts = []string{
	"HD\r\n",
	"VA 5 c12 f0\r\nhello\r\n",
	"VA 0 c1 f0\r\n\r\n",
	"EN\r\n",
	"NF\r\n",
	"NS\r\n",
	"EX\r\n",
	"MN\r\n",
	"ERROR\r\n",
	"CLIENT_ERROR bad command line format\r\n",
	"SERVER_ERROR out of memory\r\n",
	// A single command answered twice: what memcached really does for a key
	// containing a NUL byte.
	"CLIENT_ERROR bad command line format\r\nERROR\r\n",
	// Batch shapes.
	"HD\r\nHD\r\nMN\r\n",
	"VA 1 c1 f0\r\na\r\nVA 1 c2 f0\r\nb\r\nMN\r\n",
	"VA 1 c1 f0\r\na\r\nMN\r\n",
	"HD\r\nHD\r\nHD\r\nHD\r\nMN\r\n",
	"MN\r\n",
	"MN\r\nHD\r\n",
	// Truncated and hostile.
	"VA 5\r\nhel",
	"VA 100\r\nshort\r\n",
	"VA 1073741824\r\n",
	"",
	"\r\n",
	"junk\r\n",
	strings.Repeat("HD\r\n", 40) + "MN\r\n",
}

func newMockConn(script []byte) *Connection {
	return NewConnection(testutils.NewConnectionMock(string(script)), defaultOperationTimeout)
}

// FuzzConnectionExecute drives one real request against an arbitrary server
// byte stream. A single Execute must call fn at most once, must call it
// exactly once when it reports success, and must never hand back a status the
// request's command cannot produce.
func FuzzConnectionExecute(f *testing.F) {
	for _, s := range serverScripts {
		for _, op := range []uint8{0, 1, 2, 3} {
			f.Add(op, []byte(s))
		}
	}

	f.Fuzz(func(t *testing.T, opIdx uint8, script []byte) {
		req := fuzzRequest(opIdx)
		conn := newMockConn(script)

		calls := 0
		var status meta.StatusType
		var protoErr error
		err := conn.Execute(context.Background(), req, func(resp *meta.Response) {
			calls++
			status = resp.Status
			protoErr = resp.Error
		})

		if calls > 1 {
			t.Fatalf("script %q: fn called %d times for one request", script, calls)
		}
		if err != nil {
			if calls != 0 {
				t.Fatalf("script %q: fn called although Execute failed: %v", script, err)
			}
			return
		}
		if calls != 1 {
			t.Fatalf("script %q: Execute succeeded without calling fn", script)
		}
		if protoErr != nil {
			return // a protocol error is a valid reply to every command
		}
		if err := meta.ValidateResponse(req, &meta.Response{Status: status}); err != nil {
			t.Fatalf("script %q: Execute accepted %q for %q: %v", script, status, req.Command, err)
		}
	})
}

// FuzzConnectionExecuteBatch drives a pipeline of n requests against an
// arbitrary server byte stream. The pipeline must never report more responses
// than requests, and a successful batch of non-quiet requests must return
// exactly one valid response per request, in order.
func FuzzConnectionExecuteBatch(f *testing.F) {
	for _, s := range serverScripts {
		for _, n := range []uint8{1, 2, 3, 8} {
			f.Add(n, []byte(s))
		}
	}

	f.Fuzz(func(t *testing.T, n uint8, script []byte) {
		count := int(n)%16 + 1
		reqs := make([]*meta.Request, count)
		for i := range reqs {
			reqs[i] = fuzzRequest(uint8(i))
		}

		conn := newMockConn(script)
		responses, err := conn.ExecuteBatch(context.Background(), reqs)

		if len(responses) > len(reqs) {
			t.Fatalf("script %q: %d responses for %d requests", script, len(responses), len(reqs))
		}
		if err != nil {
			return
		}
		if len(responses) != len(reqs) {
			t.Fatalf("script %q: batch succeeded with %d responses for %d requests", script, len(responses), len(reqs))
		}
		for i, resp := range responses {
			if resp.Error != nil {
				continue
			}
			if err := meta.ValidateResponse(reqs[i], resp); err != nil {
				t.Fatalf("script %q: response %d is %q for %q: %v", script, i, resp.Status, reqs[i].Command, err)
			}
		}
	})
}

// FuzzBatchResponseIndependence checks the contract BatchCommands relies on:
// every response a batch returns owns its own storage. MultiGet keeps
// resp.Data without cloning it, so two responses sharing a backing array would
// make one key silently return another key's value.
func FuzzBatchResponseIndependence(f *testing.F) {
	f.Add([]byte("VA 1 c1 f0\r\na\r\nVA 1 c2 f0\r\nb\r\nMN\r\n"))
	f.Add([]byte("VA 3 c1 f0\r\nabc\r\nVA 3 c2 f0\r\nxyz\r\nMN\r\n"))
	f.Add([]byte("VA 0 c1 f0\r\n\r\nVA 4 c2 f0\r\nlong\r\nMN\r\n"))
	f.Add([]byte("HD\r\nVA 2 c1 f0\r\nhi\r\nMN\r\n"))
	f.Add([]byte("VA 5 c1 f0\r\nhello\r\nVA 1 c2 f0\r\nz\r\nVA 9 c3 f0\r\nlongervalue\r\nMN\r\n"))

	f.Fuzz(func(t *testing.T, script []byte) {
		const count = 4
		reqs := make([]*meta.Request, count)
		for i := range reqs {
			reqs[i] = getRequest(fmt.Sprintf("key%d", i), GetOptions{})
		}

		conn := newMockConn(script)
		responses, err := conn.ExecuteBatch(context.Background(), reqs)
		if err != nil {
			return
		}

		// Snapshot every value, then mutate each one in turn: no other
		// response may change.
		snapshots := make([][]byte, len(responses))
		for i, resp := range responses {
			snapshots[i] = bytes.Clone(resp.Data)
		}
		for i, resp := range responses {
			for j := range resp.Data {
				resp.Data[j] ^= 0xff
			}
			for k, other := range responses {
				if k == i {
					continue
				}
				if !bytes.Equal(other.Data, snapshots[k]) {
					t.Fatalf("script %q: writing response %d changed response %d (shared buffer)", script, i, k)
				}
			}
			copy(resp.Data, snapshots[i])
		}
	})
}

// FuzzCommandsGetIntegrity checks that a value handed to the caller survives
// the next operation on the same connection. Get clones resp.Data precisely
// because the connection reuses that buffer; a regression there would corrupt
// an Item the caller still holds.
func FuzzCommandsGetIntegrity(f *testing.F) {
	f.Add([]byte("VA 5 c1 f0\r\nhello\r\nVA 2 c2 f0\r\nby\r\n"))
	f.Add([]byte("VA 3 c1 f0\r\nabc\r\nVA 9 c2 f0\r\nlongvalue\r\n"))
	f.Add([]byte("VA 9 c1 f0\r\nlongvalue\r\nVA 1 c2 f0\r\nx\r\n"))
	f.Add([]byte("VA 0 c1 f0\r\n\r\nVA 4 c2 f0\r\nabcd\r\n"))
	f.Add([]byte("VA 5 c1 f0\r\nhello\r\nEN\r\n"))

	f.Fuzz(func(t *testing.T, script []byte) {
		cmds := NewCommands(newMockConn(script))
		ctx := context.Background()

		first, err := cmds.Get(ctx, "alpha")
		if err != nil {
			return
		}
		kept := bytes.Clone(first.Value)

		// The outcome of the second Get does not matter: it only has to leave
		// the first value alone.
		_, _ = cmds.Get(ctx, "beta")

		if !bytes.Equal(first.Value, kept) {
			t.Fatalf("script %q: a second Get changed the first item's value\n got: %q\nwant: %q",
				script, first.Value, kept)
		}
	})
}

// fuzzRequest returns one of the request shapes the high-level client builds,
// selected by index so the fuzzer never has to construct a valid request.
func fuzzRequest(idx uint8) *meta.Request {
	switch idx % 4 {
	case 0:
		return getRequest("fuzzkey", GetOptions{})
	case 1:
		return storeRequest{ttl: NoTTL}.request("fuzzkey", []byte("value"))
	case 2:
		return meta.NewRequest(meta.CmdDelete, "fuzzkey", nil)
	default:
		return meta.NewRequest(meta.CmdArithmetic, "fuzzkey", nil).AddReturnValue().AddReturnCAS().AddDelta(1)
	}
}

// FuzzTTLExpiration checks the wire encoding of a TTL. memcached truncates
// exptime to 32 bits and reads the result as a signed time, so an encoded
// value at or above 2^31 is a time in the past: the server accepts the item
// and it is immediately unreadable. A requested expiration in the future must
// therefore never encode to a value the server would read as already past.
func FuzzTTLExpiration(f *testing.F) {
	f.Add(int64(0), int64(0))
	f.Add(int64(time.Second), int64(0))
	f.Add(int64(30*24*time.Hour), int64(0))
	f.Add(int64(31*24*time.Hour), int64(0))
	f.Add(int64(math.MaxInt64), int64(0))
	f.Add(int64(math.MinInt64), int64(0))
	f.Add(int64(0), int64(1))
	f.Add(int64(0), int64(math.MaxInt64))
	f.Add(int64(0), int64(math.MinInt64))
	f.Add(int64(0), time.Date(3000, 1, 1, 0, 0, 0, 0, time.UTC).Unix())
	f.Add(int64(0), int64(1<<31))
	f.Add(int64(0), int64(1<<32))

	f.Fuzz(func(t *testing.T, durNanos int64, atUnix int64) {
		for name, ttl := range map[string]TTL{
			"ExpiresIn": ExpiresIn(time.Duration(durNanos)),
			"ExpiresAt": ExpiresAt(time.Unix(atUnix, 0)),
		} {
			now := time.Now().Unix()
			exptime := int64(ttl.Expiration())

			if exptime < 0 {
				t.Fatalf("%s(%d): negative exptime %d", name, durNanos, exptime)
			}
			if exptime > maxExptime {
				t.Fatalf("%s(%d): exptime %d exceeds what the server reads as a future time",
					name, durNanos, exptime)
			}

			// A request for a future expiration must not encode to a past one.
			wantFuture := name == "ExpiresIn" && time.Duration(durNanos) > 0
			if !wantFuture || exptime == 0 {
				continue
			}
			deadline := exptime
			if exptime <= int64(maxRelativeTTL/time.Second) {
				deadline = now + exptime
			}
			if deadline < now {
				t.Fatalf("%s(%v): encodes to exptime %d, which the server reads as %d seconds in the past",
					name, time.Duration(durNanos), exptime, now-deadline)
			}
		}
	})
}

// FuzzServerSelector checks the properties key placement depends on. Every
// client in a fleet must agree on which server owns a key, so selection has to
// be deterministic and independent of the order the addresses arrive in.
// Removing a server other than the chosen one must not move the key, which is
// the whole point of rendezvous hashing: a membership change invalidates only
// the keys that lived on the departed node.
func FuzzServerSelector(f *testing.F) {
	f.Add("user:42", "a:1,b:2,c:3", uint8(1))
	f.Add("", "a:1", uint8(0))
	f.Add("k", "a:1,a:1", uint8(0))
	f.Add("k", strings.Repeat("s:1,", 20), uint8(3))
	f.Add("\x00\xff", "10.0.0.1:11211,10.0.0.2:11211", uint8(1))

	f.Fuzz(func(t *testing.T, key string, addrList string, dropIdx uint8) {
		var servers []Server
		for addr := range strings.SplitSeq(addrList, ",") {
			if addr != "" {
				servers = append(servers, Server{Address: addr})
			}
		}
		if len(servers) == 0 {
			return
		}

		for name, selector := range map[string]ServerSelector{
			"StableServerSelector":  StableServerSelector,
			"OrderedServerSelector": OrderedServerSelector,
		} {
			chosen := selector(key, servers)

			if !slices.Contains(servers, chosen) {
				t.Fatalf("%s(%q, %v) returned %v, which is not in the set", name, key, servers, chosen)
			}
			if again := selector(key, servers); again != chosen {
				t.Fatalf("%s(%q) is not deterministic: %v then %v", name, key, chosen, again)
			}

			// Only the membership-stable selector promises the next two.
			if name != "StableServerSelector" {
				continue
			}

			reversed := slices.Clone(servers)
			slices.Reverse(reversed)
			if got := selector(key, reversed); got != chosen {
				t.Fatalf("%s(%q) depends on order: %v forwards, %v reversed", name, key, chosen, got)
			}

			// Dropping a server that does not own the key must not move it.
			drop := int(dropIdx) % len(servers)
			if servers[drop] == chosen || len(servers) == 1 {
				continue
			}
			remaining := slices.Delete(slices.Clone(servers), drop, drop+1)
			if len(remaining) == 0 {
				continue
			}
			if got := selector(key, remaining); got != chosen {
				t.Fatalf("%s(%q): removing %v moved the key from %v to %v",
					name, key, servers[drop], chosen, got)
			}
		}
	})
}
