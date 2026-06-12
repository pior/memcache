package meta

import (
	"testing"
)

// responseWithFlags builds a Response carrying the given raw flags string.
func responseWithFlags(flags string) *Response {
	return &Response{Status: StatusHD, Flags: Flags(flags)}
}

func TestResponse_TypedGetters(t *testing.T) {
	t.Run("CAS", func(t *testing.T) {
		v, ok := responseWithFlags(" c12345").CAS()
		if !ok || v != 12345 {
			t.Errorf("CAS = %d/%v, want 12345/true", v, ok)
		}
	})

	t.Run("CAS missing", func(t *testing.T) {
		if _, ok := responseWithFlags("").CAS(); ok {
			t.Error("CAS on empty flags must return false")
		}
	})

	t.Run("CAS invalid token", func(t *testing.T) {
		if _, ok := responseWithFlags(" cabc").CAS(); ok {
			t.Error("CAS with non-numeric token must return false")
		}
	})

	t.Run("TTL", func(t *testing.T) {
		v, ok := responseWithFlags(" t3600").TTL()
		if !ok || v != 3600 {
			t.Errorf("TTL = %d/%v, want 3600/true", v, ok)
		}
	})

	t.Run("TTL infinite", func(t *testing.T) {
		v, ok := responseWithFlags(" t-1").TTL()
		if !ok || v != -1 {
			t.Errorf("TTL = %d/%v, want -1/true", v, ok)
		}
	})

	t.Run("ClientFlags", func(t *testing.T) {
		v, ok := responseWithFlags(" f123").ClientFlags()
		if !ok || v != 123 {
			t.Errorf("ClientFlags = %d/%v, want 123/true", v, ok)
		}
	})

	t.Run("ClientFlags overflows uint32", func(t *testing.T) {
		if _, ok := responseWithFlags(" f99999999999").ClientFlags(); ok {
			t.Error("ClientFlags beyond uint32 must return false")
		}
	})

	t.Run("Size", func(t *testing.T) {
		v, ok := responseWithFlags(" s1024").Size()
		if !ok || v != 1024 {
			t.Errorf("Size = %d/%v, want 1024/true", v, ok)
		}
	})

	t.Run("Hit true", func(t *testing.T) {
		v, ok := responseWithFlags(" h1").Hit()
		if !ok || !v {
			t.Errorf("Hit = %v/%v, want true/true", v, ok)
		}
	})

	t.Run("Hit false", func(t *testing.T) {
		v, ok := responseWithFlags(" h0").Hit()
		if !ok || v {
			t.Errorf("Hit = %v/%v, want false/true", v, ok)
		}
	})

	t.Run("LastAccess", func(t *testing.T) {
		v, ok := responseWithFlags(" l30").LastAccess()
		if !ok || v != 30 {
			t.Errorf("LastAccess = %d/%v, want 30/true", v, ok)
		}
	})

	t.Run("Key", func(t *testing.T) {
		v, ok := responseWithFlags(" kmykey").Key()
		if !ok || string(v) != "mykey" {
			t.Errorf("Key = %q/%v, want mykey/true", v, ok)
		}
	})

	t.Run("Opaque", func(t *testing.T) {
		v, ok := responseWithFlags(" Otok").Opaque()
		if !ok || string(v) != "tok" {
			t.Errorf("Opaque = %q/%v, want tok/true", v, ok)
		}
	})

	t.Run("Win Stale AlreadyWon", func(t *testing.T) {
		resp := responseWithFlags(" W X Z")
		if !resp.Win() || !resp.Stale() || !resp.AlreadyWon() {
			t.Errorf("Win/Stale/AlreadyWon = %v/%v/%v, want all true",
				resp.Win(), resp.Stale(), resp.AlreadyWon())
		}

		empty := responseWithFlags("")
		if empty.Win() || empty.Stale() || empty.AlreadyWon() {
			t.Error("empty flags must report no win/stale/already-won")
		}
	})
}

func TestParseDebugParams_Malformed(t *testing.T) {
	params := ParseDebugParams([]byte("exp=3600 garbage la=12 ="))
	if got := params["exp"]; got != "3600" {
		t.Errorf("params[exp] = %q, want %q", got, "3600")
	}
	if got := params["la"]; got != "12" {
		t.Errorf("params[la] = %q, want %q", got, "12")
	}
	if _, ok := params["garbage"]; ok {
		t.Error("token without '=' must be skipped")
	}
}
