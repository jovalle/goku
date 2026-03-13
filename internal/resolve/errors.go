package resolve

import (
	"errors"
	"fmt"
)

// Sentinel errors for the resolve package.
var (
	// ErrNotFound means no link or rule matched the path.
	ErrNotFound = errors.New("not found")

	// ErrBadPattern means a rule pattern is malformed.
	ErrBadPattern = errors.New("invalid rule pattern")
)

// ResolveError provides detailed context about a resolution failure.
type ResolveError struct {
	Path   string // the requested path
	Reason string // human-readable explanation
	Err    error  // underlying error (may be nil)
}

func (e *ResolveError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("resolve %q: %s: %v", e.Path, e.Reason, e.Err)
	}
	return fmt.Sprintf("resolve %q: %s", e.Path, e.Reason)
}

// Unwrap supports errors.Is and errors.As.
func (e *ResolveError) Unwrap() error {
	return e.Err
}
