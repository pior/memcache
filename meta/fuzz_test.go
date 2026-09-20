package meta

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// newFuzzReader builds a reader sized exactly like the ones this library gives
// a Connection, so line-length limits are exercised as they are in production.
func newFuzzReader(data []byte) *bufio.Reader {
	return bufio.NewReaderSize(bytes.NewReader(data), MaxLineSize)
}

// responseSeeds are the shared seed corpus for the response-parsing targets:
// valid replies, edge cases, and hostile inputs.
var responseSeeds = [][]byte{
	// Valid responses covering every status.
	[]byte("HD\r\n"),
	[]byte("VA 5\r\nhello\r\n"),
	[]byte("VA 0\r\n\r\n"),
	[]byte("EN\r\n"),
	[]byte("NF\r\n"),
	[]byte("NS\r\n"),
	[]byte("EX\r\n"),
	[]byte("MN\r\n"),
	[]byte("OK\r\n"),
	[]byte("CLIENT_ERROR invalid key\r\n"),
	[]byte("SERVER_ERROR out of memory\r\n"),
	[]byte("ERROR\r\n"),
	[]byte("VA 10 v\r\n0123456789\r\n"),
	[]byte("HD c123 t456\r\n"),
	[]byte("VA 3 W Z\r\nabc\r\n"),
	[]byte("ME key foo=bar baz=qux\r\n"),
	[]byte("HD O12345\r\n"),
	[]byte("HD kmykey c1 t-1 f0 s5 h1 l30\r\n"),

	// Edge cases.
	[]byte("VA 5\r\nhello\n"),
	[]byte("HD \r\n"),
	[]byte("VA 1 \r\nx\r\n"),
	[]byte("\r\n"),
	[]byte(""),
	[]byte("UNKNOWN\r\n"),
	[]byte("VA\r\n"),
	[]byte("VA abc\r\n"),
	[]byte("VA -1\r\n"),
	[]byte("VA +5\r\nhello\r\n"),
	[]byte("VA 05\r\nhello\r\n"),
	[]byte("VA 5\r\nabc"),
	[]byte("VA 5\r\nhello"),
	[]byte("VA 5\r\nhelloXX"),
	[]byte("CLIENT_ERROR \r\n"),
	[]byte("SERVER_ERROR\r\n"),
	[]byte("HD v v v v v\r\n"),
	[]byte("VA 5 c1 c2 c3\r\nhello\r\n"),
	[]byte("ME\r\n"),
	[]byte("ME key\r\n"),

	// Hostile sizes: the declared length is not backed by data.
	[]byte("VA 1073741824\r\n"),
	[]byte("VA 1073741825\r\n"),
	[]byte("VA 999999999999999999999\r\n"),
	[]byte("VA 1000000\r\nshort\r\n"),

	// Unbounded line: no newline within the reader buffer.
	append([]byte("HD "), bytes.Repeat([]byte("x"), 8192)...),
	append([]byte("VA 3 "), bytes.Repeat([]byte("f"), 8192)...),

	// Multi-response streams, for the stream target.
	[]byte("HD\r\nVA 2\r\nhi\r\nMN\r\n"),
	[]byte("VA 4\r\nabcd\r\nEN\r\nVA 1\r\nz\r\nHD c9\r\n"),
	[]byte("VA 3\r\nabc\r\nHD\r\nVA 1\r\nx\r\n"),
	[]byte("ME k a=1\r\nME k2 b=2\r\nMN\r\n"),
	[]byte("SERVER_ERROR oom\r\nHD\r\nMN\r\n"),
}

func addResponseSeeds(f *testing.F) {
	f.Helper()
	for _, seed := range responseSeeds {
		f.Add(seed)
	}
}

// renderResponse formats a parse outcome as a string, so a differential
// mismatch reports readable values instead of struct dumps.
func renderResponse(resp *Response, err error) string {
	if err != nil {
		return "err=" + err.Error()
	}
	errText := "<nil>"
	if resp.Error != nil {
		errText = resp.Error.Error()
	}
	return fmt.Sprintf("status=%q data=%q flags=%q error=%s",
		resp.Status, resp.Data, resp.Flags, errText)
}

// FuzzReadResponse checks that parsing one response never panics and that a
// successful parse satisfies the invariants the rest of the client relies on.
func FuzzReadResponse(f *testing.F) {
	addResponseSeeds(f)

	f.Fuzz(func(t *testing.T, data []byte) {
		var resp Response
		err := ReadResponse(newFuzzReader(data), &resp)

		if err != nil {
			if parseErr, ok := errors.AsType[*ParseError](err); ok && parseErr.Message == "" {
				t.Errorf("ParseError has empty message for input %q", data)
			}
			return
		}

		// A protocol error and a status are mutually exclusive outcomes.
		if resp.Error != nil {
			if resp.Status != "" {
				t.Errorf("input %q: protocol error %v carries status %q", data, resp.Error, resp.Status)
			}
			if !resp.HasError() {
				t.Errorf("input %q: Error set but HasError() is false", data)
			}
			return
		}
		if resp.HasError() {
			t.Errorf("input %q: HasError() true with nil Error", data)
		}
		if resp.Status == "" {
			t.Errorf("input %q: empty status without error", data)
		}
		if !isKnownStatus(resp.Status) {
			t.Errorf("input %q: accepted unknown status %q", data, resp.Status)
		}

		// Only VA and ME carry payload bytes.
		if len(resp.Data) > 0 && resp.Status != StatusVA && resp.Status != StatusME {
			t.Errorf("input %q: status %q carries %d data bytes", data, resp.Status, len(resp.Data))
		}
		// MN and OK are bare: no flags, no data.
		if resp.Status == StatusMN || resp.Status == StatusOK {
			if !resp.Flags.IsEmpty() || len(resp.Data) > 0 {
				t.Errorf("input %q: bare status %q carries flags %q data %q", data, resp.Status, resp.Flags, resp.Data)
			}
		}
		// A VA value must be exactly the size the header declared, and the
		// declared size must have been within the protocol ceiling.
		if resp.Status == StatusVA {
			declared, ok := declaredVASize(data)
			if ok && declared != len(resp.Data) {
				t.Errorf("input %q: VA declared %d bytes, parsed %d", data, declared, len(resp.Data))
			}
			if len(resp.Data) > MaxDataSize {
				t.Errorf("input %q: VA data %d exceeds MaxDataSize", data, len(resp.Data))
			}
		}
		// Every flag stored must be retrievable by its own type.
		for _, ft := range flagTypesIn(resp.Flags) {
			if !resp.HasFlag(ft) {
				t.Errorf("input %q: flag %q in %q not found by HasFlag", data, string(ft), resp.Flags)
			}
		}
	})
}

// FuzzReadResponseStream is the differential target for the buffer-reuse
// optimization: parsing a stream of responses into one reused Response must
// produce exactly what parsing it into a fresh Response each time produces.
// Any stale byte surviving from a previous response shows up here.
func FuzzReadResponseStream(f *testing.F) {
	addResponseSeeds(f)

	f.Fuzz(func(t *testing.T, data []byte) {
		reused := parseStream(data, true)
		fresh := parseStream(data, false)
		if reused != fresh {
			t.Errorf("reused Response differs from fresh Response for input %q\nreused:\n%s\nfresh:\n%s",
				data, reused, fresh)
		}
	})
}

// maxStreamResponses bounds a fuzz stream so a pathological input cannot spin.
const maxStreamResponses = 32

// parseStream parses up to maxStreamResponses responses out of data and renders
// them as one string. With reuse, every response decodes into the same
// Response value, exercising the retained Data and Flags buffers.
func parseStream(data []byte, reuse bool) string {
	r := newFuzzReader(data)
	var shared Response
	var out strings.Builder

	for i := range maxStreamResponses {
		resp := &shared
		if !reuse {
			resp = &Response{}
		}
		err := ReadResponse(r, resp)
		fmt.Fprintf(&out, "%d: %s\n", i, renderResponse(resp, err))
		if err != nil {
			break
		}
	}
	return out.String()
}

// FuzzReadStatsResponse checks the stats reader never panics and never reports
// success on a stream that was not terminated by END.
func FuzzReadStatsResponse(f *testing.F) {
	seeds := [][]byte{
		[]byte("END\r\n"),
		[]byte("STAT pid 1\r\nEND\r\n"),
		[]byte("STAT uptime 3600\r\nSTAT version 1.6.0\r\nEND\r\n"),
		[]byte("STAT name value with spaces\r\nEND\r\n"),
		[]byte("STAT noval\r\nEND\r\n"),
		[]byte("STAT \r\nEND\r\n"),
		[]byte("STAT a 1\r\n"),
		[]byte("ERROR\r\n"),
		[]byte("CLIENT_ERROR bad\r\n"),
		[]byte("SERVER_ERROR oom\r\n"),
		[]byte("garbage\r\nEND\r\n"),
		[]byte(""),
		[]byte("STAT dup 1\r\nSTAT dup 2\r\nEND\r\n"),
		[]byte("STAT \r \nEND\n"),
		[]byte("STAT a\tb 1\r\nEND\r\n"),
		[]byte("STAT  0\nEND\n"),
		append(append([]byte("STAT k "), bytes.Repeat([]byte("v"), 8192)...), []byte("\r\nEND\r\n")...),
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		stats, err := ReadStatsResponse(newFuzzReader(data))
		if err != nil {
			return
		}
		// Success means an END line was reached; every key must round-trip.
		if !bytes.Contains(data, []byte("END")) {
			t.Errorf("input %q: success without an END marker", data)
		}
		// The client does not police the shape of a stat name: the server is
		// trusted for what it sends back, so an empty or whitespace-carrying
		// name is the server's business. What must hold is that a name the
		// reader reports came from the bytes it was given.
		for k := range stats {
			if !bytes.Contains(data, []byte(k)) {
				t.Errorf("input %q: reported stat name %q that is not present", data, k)
			}
		}
	})
}

// FuzzParseDebugParams checks the ME debug parser never panics and only
// reports pairs that were really present.
func FuzzParseDebugParams(f *testing.F) {
	for _, seed := range []string{
		"size=1024 ttl=3600 flags=0",
		"",
		"=",
		"a=",
		"=b",
		"a=b=c",
		"   spaced   out=1   ",
		"noequals",
		strings.Repeat("k=v ", 100),
	} {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		params := ParseDebugParams(data)
		for k, v := range params {
			pair := k + "=" + v
			if !bytes.Contains(data, []byte(pair)) {
				t.Errorf("input %q: reported pair %q that is not present", data, pair)
			}
			if strings.ContainsAny(k, " \t") || strings.ContainsAny(v, " \t") {
				t.Errorf("input %q: pair %q contains whitespace", data, pair)
			}
		}
	})
}

// --- helpers ---

func isKnownStatus(s StatusType) bool {
	switch s {
	case StatusHD, StatusVA, StatusEN, StatusNF, StatusNS, StatusEX, StatusMN, StatusME, StatusOK:
		return true
	default:
		return false
	}
}

// declaredVASize extracts the size token from a VA header line, so the parsed
// value length can be checked against what the server claimed.
func declaredVASize(data []byte) (int, bool) {
	line, _, found := bytes.Cut(data, []byte("\n"))
	if !found {
		return 0, false
	}
	fields := strings.Fields(string(bytes.TrimSuffix(line, []byte("\r"))))
	if len(fields) < 2 || fields[0] != string(StatusVA) {
		return 0, false
	}
	n := 0
	for _, c := range []byte(fields[1]) {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
		if n > MaxDataSize {
			return 0, false
		}
	}
	return n, true
}

// flagTypesIn lists the flag types present in a serialized Flags value.
func flagTypesIn(flags Flags) []FlagType {
	wire := flags.String()

	var out []FlagType
	for i := 0; i < len(wire); {
		if wire[i] == ' ' {
			i++
			continue
		}
		out = append(out, FlagType(wire[i]))
		i++
		for i < len(wire) && wire[i] != ' ' {
			i++
		}
	}
	return out
}
