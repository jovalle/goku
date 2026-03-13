package store

import (
	"strings"
	"sync"

	"github.com/jovalle/goku/internal/model"
	"github.com/jovalle/goku/internal/resolve"
)

// LinkStore holds the current config and resolves paths to URLs.
// All methods are safe for concurrent use.
type LinkStore struct {
	mu     sync.RWMutex
	config model.Config
}

// New creates a LinkStore with nil-safe defaults.
func New(cfg model.Config) *LinkStore {
	if cfg.Links == nil {
		cfg.Links = make(map[string]string)
	}
	if cfg.Rules == nil {
		cfg.Rules = []model.Rule{}
	}
	return &LinkStore{config: cfg}
}

// Resolve finds the redirect URL for a path.
// Priority: exact match -> prefix rules -> template rules.
func (s *LinkStore) Resolve(path string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if path == "" {
		return "", &resolve.ResolveError{
			Path:   path,
			Reason: "empty path",
			Err:    resolve.ErrNotFound,
		}
	}

	parts := strings.SplitN(path, "/", 2)
	shortName := parts[0]

	// 1. Exact match (only if no remainder)
	if url, ok := s.config.Links[shortName]; ok && len(parts) == 1 {
		return url, nil
	}

	// 2. Prefix rules
	if len(parts) > 1 {
		remainder := parts[1]
		for _, rule := range s.config.Rules {
			if rule.Type == "prefix" && rule.Pattern == shortName {
				return rule.Redirect + "/" + remainder, nil
			}
		}
	}

	// 3. Template rules
	for _, rule := range s.config.Rules {
		if rule.Type == "template" {
			if url, ok := matchTemplate(path, rule.Pattern, rule.Redirect); ok {
				return url, nil
			}
		}
	}

	return "", &resolve.ResolveError{
		Path:   path,
		Reason: "no matching link or rule",
		Err:    resolve.ErrNotFound,
	}
}

// matchTemplate checks if a path matches a template pattern and fills placeholders.
func matchTemplate(path, pattern, redirect string) (string, bool) {
	patternParts := strings.Split(pattern, "/")
	pathParts := strings.Split(path, "/")

	if len(patternParts) != len(pathParts) {
		return "", false
	}

	result := redirect
	for i, pp := range patternParts {
		if strings.HasPrefix(pp, "{") && strings.HasSuffix(pp, "}") {
			result = strings.Replace(result, pp, pathParts[i], 1)
		} else if pp != pathParts[i] {
			return "", false
		}
	}
	return result, true
}

// Links returns a copy of all static links.
func (s *LinkStore) Links() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string]string, len(s.config.Links))
	for k, v := range s.config.Links {
		result[k] = v
	}
	return result
}

// Rules returns a copy of all rules.
func (s *LinkStore) Rules() []model.Rule {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]model.Rule, len(s.config.Rules))
	copy(result, s.config.Rules)
	return result
}

// AddLink adds a static link and returns the updated config.
func (s *LinkStore) AddLink(name, url string) model.Config {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.config.Links[name] = url
	return s.configCopy()
}

// DeleteLink removes a static link and returns the updated config.
func (s *LinkStore) DeleteLink(name string) model.Config {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.config.Links, name)
	return s.configCopy()
}

// AddRule adds a rule and returns the updated config.
func (s *LinkStore) AddRule(rule model.Rule) model.Config {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.config.Rules = append(s.config.Rules, rule)
	return s.configCopy()
}

// DeleteRule removes a rule by name and returns the updated config.
func (s *LinkStore) DeleteRule(name string) model.Config {
	s.mu.Lock()
	defer s.mu.Unlock()

	rules := make([]model.Rule, 0, len(s.config.Rules))
	for _, r := range s.config.Rules {
		if r.Name != name {
			rules = append(rules, r)
		}
	}
	s.config.Rules = rules
	return s.configCopy()
}

// Config returns a copy of the current config.
func (s *LinkStore) Config() model.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.configCopy()
}

// Update atomically replaces the config (used by the file watcher).
func (s *LinkStore) Update(cfg model.Config) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if cfg.Links == nil {
		cfg.Links = make(map[string]string)
	}
	if cfg.Rules == nil {
		cfg.Rules = []model.Rule{}
	}
	s.config = cfg
}

// configCopy returns a deep copy of config. Caller must hold at least a read lock.
func (s *LinkStore) configCopy() model.Config {
	links := make(map[string]string, len(s.config.Links))
	for k, v := range s.config.Links {
		links[k] = v
	}
	rules := make([]model.Rule, len(s.config.Rules))
	copy(rules, s.config.Rules)
	return model.Config{Links: links, Rules: rules}
}
