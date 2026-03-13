package resolve

import "github.com/jovalle/goku/internal/model"

// Resolver resolves a short path to a redirect URL.
type Resolver interface {
	Resolve(path string) (string, error)
}

// LinkLister provides read access to links and rules (for the UI).
type LinkLister interface {
	Links() map[string]string
	Rules() []model.Rule
}
