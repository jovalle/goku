package resolve

import (
	"errors"
	"testing"
)

func TestResolveError_Error(t *testing.T) {
	tests := []struct {
		name string
		err  ResolveError
		want string
	}{
		{
			name: "with underlying error",
			err:  ResolveError{Path: "gh", Reason: "no match", Err: ErrNotFound},
			want: `resolve "gh": no match: not found`,
		},
		{
			name: "without underlying error",
			err:  ResolveError{Path: "foo", Reason: "empty path"},
			want: `resolve "foo": empty path`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveError_Unwrap(t *testing.T) {
	err := &ResolveError{Path: "gh", Reason: "no match", Err: ErrNotFound}

	if !errors.Is(err, ErrNotFound) {
		t.Error("expected errors.Is(err, ErrNotFound) to be true")
	}

	var re *ResolveError
	if !errors.As(err, &re) {
		t.Error("expected errors.As to succeed")
	}
	if re.Path != "gh" {
		t.Errorf("Path = %q, want %q", re.Path, "gh")
	}
}

func TestResolveError_Unwrap_nil(t *testing.T) {
	err := &ResolveError{Path: "x", Reason: "test"}
	if err.Unwrap() != nil {
		t.Error("expected Unwrap() to return nil")
	}
}

func TestSentinelErrors(t *testing.T) {
	if ErrNotFound.Error() != "not found" {
		t.Errorf("ErrNotFound = %q, want %q", ErrNotFound.Error(), "not found")
	}
	if ErrBadPattern.Error() != "invalid rule pattern" {
		t.Errorf("ErrBadPattern = %q, want %q", ErrBadPattern.Error(), "invalid rule pattern")
	}
}
