package meta

import (
	"bufio"
	"bytes"
	"strconv"
	"strings"
	"testing"
)

// fuzzCommands is the command set the request targets pick from. The index is
// taken modulo the length so the fuzzer never has to guess a valid string.
var fuzzCommands = []CmdType{
	CmdGet, CmdSet, CmdDelete, CmdArithmetic, CmdDebug, CmdNoOp, CmdStats, CmdFlushAll,
}

// FuzzWriteRequest is the protocol-injection target. A request that
// ValidateRequest accepts must serialize to exactly one command line plus, for
// ms, exactly one length-prefixed data block. Any extra line terminator in the
// output means a caller-supplied key, flag or argument can smuggle a second
// command onto the connection.
func FuzzWriteRequest(f *testing.F) {
	seeds := []struct {
		cmd   uint8
		key   string
		flags string
		data  []byte
	}{
		{0, "mykey", " v c f", nil},
		{1, "mykey", " T60 F0", []byte("value")},
		{2, "mykey", " C123", nil},
		{3, "counter", " v D5 MI", nil},
		{4, "mykey", "", nil},
		{5, "", "", nil},
		{6, "items", "", nil},
		{7, "", "", nil},
		// Injection attempts through every user-controlled field.
		{0, "key\r\nmn", "", nil},
		{0, "key\nmn", "", nil},
		{0, "key with space", "", nil},
		{0, "key", " O" + strings.Repeat("x", 40), nil},
		{0, "key", " v\r\nmn\r\n", nil},
		{0, "key", "\r\n", nil},
		{1, "key", " T60", []byte("data\r\nmn\r\n")},
		{6, "args\r\nmn", "", nil},
		{6, "args with space", "", nil},
		{0, strings.Repeat("k", 250), "", nil},
		{0, strings.Repeat("k", 251), "", nil},
		{0, "", " v", nil},
		{1, "key", "    ", []byte("")},
		{3, "key", " M" + strings.Repeat("m", 100), nil},
		// Flags without their leading space merge into the key.
		{2, "0", "0", nil},
		{0, "key", "v c", nil},
	}
	for _, s := range seeds {
		f.Add(s.cmd, s.key, s.flags, s.data)
	}

	f.Fuzz(func(t *testing.T, cmdIdx uint8, key string, flags string, data []byte) {
		req := &Request{
			Command: fuzzCommands[int(cmdIdx)%len(fuzzCommands)],
			Key:     key,
			Data:    data,
			Flags:   Flags(flags),
		}

		var buf bytes.Buffer
		if err := WriteRequest(&buf, req); err != nil {
			// A rejected request must write nothing at all: a partial write
			// would leave a fragment on the connection.
			if buf.Len() != 0 {
				t.Errorf("rejected request wrote %q", buf.Bytes())
			}
			return
		}

		out := buf.Bytes()
		header, rest, found := bytes.Cut(out, []byte(CRLF))
		if !found {
			t.Fatalf("request %+v produced no CRLF-terminated header: %q", req, out)
		}
		// The header must be a single line: no CR and no LF inside it.
		if i := bytes.IndexAny(header, "\r\n"); i >= 0 {
			t.Fatalf("header line %q contains a line terminator at %d (command smuggling)", header, i)
		}

		if req.Command != CmdSet {
			if len(rest) != 0 {
				t.Fatalf("non-set request %+v wrote trailing bytes %q", req, rest)
			}
			checkHeaderFields(t, req, string(header))
			return
		}

		// ms: the remainder is exactly the data block and its terminator, so
		// the body cannot be read as commands even when it contains CRLF.
		want := append(append([]byte{}, req.Data...), CRLF...)
		if !bytes.Equal(rest, want) {
			t.Fatalf("set body mismatch\n got: %q\nwant: %q", rest, want)
		}
		fields := checkHeaderFields(t, req, string(header))
		// The declared size must equal the bytes actually written.
		if len(fields) < 3 {
			t.Fatalf("set header %q has too few fields", header)
		}
		size, err := strconv.Atoi(fields[2])
		if err != nil {
			t.Fatalf("set header %q has non-numeric size %q", header, fields[2])
		}
		if size != len(req.Data) {
			t.Fatalf("set header declares %d bytes, body carries %d", size, len(req.Data))
		}
	})
}

// checkHeaderFields verifies the command and key survive serialization intact
// and returns the header's space-separated fields.
func checkHeaderFields(t *testing.T, req *Request, header string) []string {
	t.Helper()

	fields := strings.Split(header, " ")
	if len(fields) == 0 || fields[0] != string(req.Command) {
		t.Fatalf("header %q does not start with command %q", header, req.Command)
	}
	switch req.Command {
	case CmdNoOp, CmdFlushAll:
		if len(fields) != 1 {
			t.Fatalf("bare command %q serialized with arguments: %q", req.Command, header)
		}
	case CmdStats:
		want := string(req.Command)
		if req.Key != "" {
			want += Space + req.Key
		}
		if header != want {
			t.Fatalf("stats header %q does not serialize argument %q", header, req.Key)
		}
	default:
		if len(fields) < 2 || fields[1] != req.Key {
			t.Fatalf("header %q lost its key %q", header, req.Key)
		}
	}
	return fields
}

// FuzzRequestResponseRoundTrip drives a written request back through the
// response reader's line scanner: whatever WriteRequest emits for an accepted
// request must be readable as exactly one line, with no residue for the next
// read. This is the desynchronization invariant the connection depends on.
func FuzzRequestResponseRoundTrip(f *testing.F) {
	f.Add(uint8(0), "key", " v c")
	f.Add(uint8(1), "key", " T300")
	f.Add(uint8(5), "", "")
	f.Add(uint8(0), "\x00\x01key", " v")
	f.Add(uint8(0), "ключ", " v")
	// A stats argument is the one caller-supplied field with no length of its
	// own, so it is the way to overrun a command line.
	f.Add(uint8(6), strings.Repeat("a", MaxStatsArgLength), "")
	f.Add(uint8(6), strings.Repeat("a", MaxStatsArgLength+1), "")
	f.Add(uint8(6), strings.Repeat("a", 16<<10), "")

	f.Fuzz(func(t *testing.T, cmdIdx uint8, key string, flags string) {
		req := &Request{
			Command: fuzzCommands[int(cmdIdx)%len(fuzzCommands)],
			Key:     key,
			Flags:   Flags(flags),
		}
		if req.Command == CmdSet {
			req.Command = CmdGet // keep this target on single-line commands
		}

		var buf bytes.Buffer
		if err := WriteRequest(&buf, req); err != nil {
			return
		}

		r := bufio.NewReaderSize(bytes.NewReader(buf.Bytes()), MaxLineSize)
		line, err := readLine(r)
		if err != nil {
			t.Fatalf("written request %q is not one readable line: %v", buf.Bytes(), err)
		}
		if r.Buffered() != 0 {
			t.Fatalf("written request %q left %d unread bytes after one line", buf.Bytes(), r.Buffered())
		}
		if strings.ContainsAny(line, "\r\n") {
			t.Fatalf("request line %q contains a line terminator", line)
		}
	})
}

// FuzzFlagsGet is a differential target: the in-place scanner Flags.Get must
// agree with a straightforward strings.Fields reference for every input,
// including ones no Add* method would produce.
func FuzzFlagsGet(f *testing.F) {
	for _, seed := range []string{
		" v c t", " T60 Oopaque", "", " ", "   ", "v", " vv", " v  c",
		" O", " O ", " Ovalue c123", " c", "c", " \x00", " 12 34",
		" Mx MI MD", strings.Repeat(" f", 50),
	} {
		for _, ft := range []byte{'v', 'c', 'O', 'T', ' ', 0} {
			f.Add(seed, ft)
		}
	}

	f.Fuzz(func(t *testing.T, flagsStr string, flagByte byte) {
		flags := Flags(flagsStr)
		ft := FlagType(flagByte)

		gotToken, gotOK := flags.Get(ft)
		wantToken, wantOK := referenceFlagGet(flagsStr, flagByte)

		if gotOK != wantOK || string(gotToken) != wantToken {
			t.Fatalf("Flags(%q).Get(%q)\n got: token=%q ok=%v\nwant: token=%q ok=%v",
				flagsStr, string(flagByte), gotToken, gotOK, wantToken, wantOK)
		}
		if gotOK != flags.Has(ft) {
			t.Fatalf("Flags(%q): Get ok=%v but Has=%v for %q", flagsStr, gotOK, flags.Has(ft), string(flagByte))
		}
	})
}

// referenceFlagGet is the obvious implementation of flag lookup, used to check
// the optimized scanner. Flags are separated on the wire by a single space, so
// it splits on ' ' alone: strings.Fields would also split on CR, LF and tab,
// which are ordinary token bytes here.
func referenceFlagGet(flags string, flagByte byte) (string, bool) {
	for field := range strings.SplitSeq(flags, " ") {
		if field != "" && field[0] == flagByte {
			return field[1:], true
		}
	}
	return "", false
}

// FuzzValidateKey checks that key validation and serialization agree: a key
// ValidateKey accepts must never be able to break the command line.
func FuzzValidateKey(f *testing.F) {
	for _, seed := range []string{
		"", "k", "key", strings.Repeat("k", 250), strings.Repeat("k", 251),
		"key with space", "key\t", "key\r", "key\n", "\x00", "\x7f", "ключ",
		"a b", " ", "\v", "\f",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, key string) {
		err := ValidateKey(key)
		if err != nil {
			return
		}
		if key == "" {
			t.Fatal("empty key accepted")
		}
		if len(key) > MaxKeyLength {
			t.Fatalf("key of %d bytes accepted", len(key))
		}
		// The protocol document is explicit: "the key must not include
		// control characters or whitespace" (references/doc-protocol.txt).
		for i, c := range []byte(key) {
			if c <= ' ' || c == 0x7f {
				t.Fatalf("key %q accepted with control character %#x at %d", key, c, i)
			}
		}
	})
}
