package protocol

import "testing"

func assertEqualString(t testing.TB, want, got string) {
	if want != got {
		t.Errorf("expected: %q, got: %q", want, got)
		t.FailNow()
	}
}
