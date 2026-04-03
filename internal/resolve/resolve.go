package resolve

import "github.com/jovalle/goku/internal/model"

// Resolver resolves a short path to a redirect URL.
type Resolver interface {
	Resolve(path string) (string, error)
}

// AliasLister provides read access to aliases (for UI and APIs).
type AliasLister interface {
	Aliases() []model.Alias
}

// LinkLister provides legacy read access to links and rules.
type LinkLister interface {
	Links() map[string]string
	Rules() []model.Rule
}
