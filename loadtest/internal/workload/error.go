package workload

import "fmt"

// DesyncError reports a key-embedding invariant violation: a read returned a
// value belonging to a different key, proving connection desynchronization.
type DesyncError struct {
	KeyID int
	Got   string // truncated prefix of the wrong value
}

func (e *DesyncError) Error() string {
	return fmt.Sprintf("DESYNC: value for key %q does not embed it, got %q", Key(e.KeyID), e.Got)
}
