package meta

import (
	"bytes"
	"testing"
)

// Every typed flag method, asserted against the exact wire bytes it produces.
func TestRequest_FlagMethods_WireFormat(t *testing.T) {
	tests := []struct {
		name  string
		build func(r *Request) *Request
		want  string // expected Flags content, including leading spaces
	}{
		{name: "AddOpaque", build: func(r *Request) *Request { return r.AddOpaque("tok42") }, want: " Otok42"},
		{name: "AddQuiet", build: func(r *Request) *Request { return r.AddQuiet() }, want: " q"},
		{name: "AddBase64Key", build: func(r *Request) *Request { return r.AddBase64Key() }, want: " b"},
		{name: "AddReturnKey", build: func(r *Request) *Request { return r.AddReturnKey() }, want: " k"},
		{name: "AddReturnValue", build: func(r *Request) *Request { return r.AddReturnValue() }, want: " v"},
		{name: "AddReturnCAS", build: func(r *Request) *Request { return r.AddReturnCAS() }, want: " c"},
		{name: "AddReturnTTL", build: func(r *Request) *Request { return r.AddReturnTTL() }, want: " t"},
		{name: "AddReturnClientFlags", build: func(r *Request) *Request { return r.AddReturnClientFlags() }, want: " f"},
		{name: "AddReturnSize", build: func(r *Request) *Request { return r.AddReturnSize() }, want: " s"},
		{name: "AddReturnHit", build: func(r *Request) *Request { return r.AddReturnHit() }, want: " h"},
		{name: "AddReturnLastAccess", build: func(r *Request) *Request { return r.AddReturnLastAccess() }, want: " l"},
		{name: "AddTTL", build: func(r *Request) *Request { return r.AddTTL(60) }, want: " T60"},
		{name: "AddCAS", build: func(r *Request) *Request { return r.AddCAS(12345) }, want: " C12345"},
		{name: "AddExplicitCAS", build: func(r *Request) *Request { return r.AddExplicitCAS(99) }, want: " E99"},
		{name: "AddClientFlags", build: func(r *Request) *Request { return r.AddClientFlags(7) }, want: " F7"},
		{name: "AddNoLRUBump", build: func(r *Request) *Request { return r.AddNoLRUBump() }, want: " u"},
		{name: "AddRecache", build: func(r *Request) *Request { return r.AddRecache(30) }, want: " R30"},
		{name: "AddVivify", build: func(r *Request) *Request { return r.AddVivify(120) }, want: " N120"},
		{name: "AddMode custom", build: func(r *Request) *Request { return r.AddMode("S") }, want: " MS"},
		{name: "AddModeSet", build: func(r *Request) *Request { return r.AddModeSet() }, want: " MS"},
		{name: "AddModeAdd", build: func(r *Request) *Request { return r.AddModeAdd() }, want: " ME"},
		{name: "AddModeReplace", build: func(r *Request) *Request { return r.AddModeReplace() }, want: " MR"},
		{name: "AddModeAppend", build: func(r *Request) *Request { return r.AddModeAppend() }, want: " MA"},
		{name: "AddModePrepend", build: func(r *Request) *Request { return r.AddModePrepend() }, want: " MP"},
		{name: "AddInvalidate", build: func(r *Request) *Request { return r.AddInvalidate() }, want: " I"},
		{name: "AddDelta", build: func(r *Request) *Request { return r.AddDelta(5) }, want: " D5"},
		{name: "AddInitialValue", build: func(r *Request) *Request { return r.AddInitialValue(100) }, want: " J100"},
		{name: "AddModeIncrement", build: func(r *Request) *Request { return r.AddModeIncrement() }, want: " MI"},
		{name: "AddModeDecrement", build: func(r *Request) *Request { return r.AddModeDecrement() }, want: " MD"},
		{name: "AddRemoveValue", build: func(r *Request) *Request { return r.AddRemoveValue() }, want: " x"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := tt.build(NewRequest(CmdGet, "key", nil))
			if got := string(req.Flags); got != tt.want {
				t.Errorf("Flags = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRequest_FlagMethods_Chaining(t *testing.T) {
	req := NewRequest(CmdSet, "key", []byte("v")).AddTTL(60).AddModeAdd().AddReturnCAS()
	want := " T60 ME c"
	if got := string(req.Flags); got != want {
		t.Errorf("Flags = %q, want %q", got, want)
	}

	var buf bytes.Buffer
	if err := WriteRequest(&buf, req); err != nil {
		t.Fatalf("WriteRequest failed: %v", err)
	}
	wantWire := "ms key 1 T60 ME c\r\nv\r\n"
	if got := buf.String(); got != wantWire {
		t.Errorf("wire = %q, want %q", got, wantWire)
	}
}

func TestFlags_Methods(t *testing.T) {
	t.Run("IsEmpty and Reset", func(t *testing.T) {
		var f Flags
		if !f.IsEmpty() {
			t.Error("zero value must be empty")
		}
		f.Add(FlagQuiet)
		if f.IsEmpty() {
			t.Error("must not be empty after Add")
		}
		f.Reset()
		if !f.IsEmpty() {
			t.Error("must be empty after Reset")
		}
	})

	t.Run("Clone is independent", func(t *testing.T) {
		var f Flags
		f.AddInt(FlagTTL, 60)
		clone := f.Clone()
		f.Reset()
		f.Add(FlagQuiet)
		if got := string(clone); got != " T60" {
			t.Errorf("clone = %q, want %q (must not alias the original)", got, " T60")
		}
	})

	t.Run("AddTokenBytes", func(t *testing.T) {
		var f Flags
		f.AddTokenBytes(FlagOpaque, []byte("abc"))
		if got := string(f); got != " Oabc" {
			t.Errorf("flags = %q, want %q", got, " Oabc")
		}
	})

	t.Run("AddInt64 negative value", func(t *testing.T) {
		var f Flags
		f.AddInt64(FlagTTL, -1)
		if got := string(f); got != " T-1" {
			t.Errorf("flags = %q, want %q", got, " T-1")
		}
	})

	t.Run("Get returns first match", func(t *testing.T) {
		var f Flags
		f.AddInt(FlagTTL, 1)
		f.AddInt(FlagTTL, 2)
		token, ok := f.Get(FlagTTL)
		if !ok || string(token) != "1" {
			t.Errorf("Get = %q/%v, want %q/true", token, ok, "1")
		}
	})

	t.Run("Get flag without token returns nil true", func(t *testing.T) {
		var f Flags
		f.Add(FlagWin)
		token, ok := f.Get(FlagWin)
		if !ok || token != nil {
			t.Errorf("Get = %q/%v, want nil/true", token, ok)
		}
	})

	t.Run("Get missing flag", func(t *testing.T) {
		var f Flags
		f.Add(FlagWin)
		token, ok := f.Get(FlagStale)
		if ok || token != nil {
			t.Errorf("Get = %q/%v, want nil/false", token, ok)
		}
	})

	t.Run("Get on flags with extra spaces", func(t *testing.T) {
		f := Flags("  v   c123 ")
		token, ok := f.Get(FlagReturnCAS)
		if !ok || string(token) != "123" {
			t.Errorf("Get = %q/%v, want %q/true", token, ok, "123")
		}
	})
}
