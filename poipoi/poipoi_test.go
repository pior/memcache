package poipoi

import (
	"testing"
)

func TestSelect(t *testing.T) {
	ctx := t.Context()

	var c chan struct{}

	select {
	case <-ctx.Done():
	case <-c:
	}
}
