package resolve

import (
	"errors"
	"fmt"
)

var (
	// ErrNotFound means no enabled alias matched the path.
	ErrNotFound = errors.New("not found")

	// ErrBadPattern means a rule pattern is malformed.
	ErrBadPattern = errors.New("invalid rule pattern")
)

// ResolveError reports why a path could not be resolved.
type ResolveError struct {
	Path   string
	Reason string
	Err    error
}

func (e *ResolveError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("resolve %q: %s: %v", e.Path, e.Reason, e.Err)
	}
	return fmt.Sprintf("resolve %q: %s", e.Path, e.Reason)
}

func (e *ResolveError) Unwrap() error {
	return e.Err
}
