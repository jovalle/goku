package store

import (
	"fmt"
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
	cfg = normalizeConfig(cfg)
	return &LinkStore{config: cfg}
}

// Resolve finds the redirect URL for a path.
func (s *LinkStore) Resolve(path string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	path = strings.Trim(path, "/")
	if path == "" {
		return "", &resolve.ResolveError{
			Path:   path,
			Reason: "empty path",
			Err:    resolve.ErrNotFound,
		}
	}

	for _, a := range s.config.Aliases {
		if !a.IsEnabled() {
			continue
		}
		if url, ok := matchAlias(path, a.Alias, a.Destination); ok {
			return url, nil
		}
	}

	return "", &resolve.ResolveError{
		Path:   path,
		Reason: "no matching alias",
		Err:    resolve.ErrNotFound,
	}
}

// Aliases returns a copy of all configured aliases.
func (s *LinkStore) Aliases() []model.Alias {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]model.Alias, len(s.config.Aliases))
	for i := range s.config.Aliases {
		result[i] = cloneAlias(s.config.Aliases[i])
	}
	return result
}

// AddAlias adds or replaces an alias and returns the updated config.
func (s *LinkStore) AddAlias(alias, destination string) (model.Config, error) {
	if err := ValidateAlias(alias, destination); err != nil {
		return model.Config{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	replaced := false
	for i := range s.config.Aliases {
		if s.config.Aliases[i].Alias == alias {
			s.config.Aliases[i].Destination = destination
			if s.config.Aliases[i].Enabled == nil {
				s.config.Aliases[i].Enabled = model.BoolPtr(true)
			}
			replaced = true
			break
		}
	}
	if !replaced {
		s.config.Aliases = append(s.config.Aliases, model.Alias{Alias: alias, Destination: destination, Enabled: model.BoolPtr(true)})
	}
	s.config.Links = legacyLinksFromAliases(s.config.Aliases)

	return s.configCopy(), nil
}

// UpdateAlias edits an existing alias. If oldAlias does not exist, it upserts by alias.
func (s *LinkStore) UpdateAlias(oldAlias, alias, destination string, enabled bool) (model.Config, error) {
	if err := ValidateAlias(alias, destination); err != nil {
		return model.Config{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	updated := model.Alias{Alias: alias, Destination: destination, Enabled: model.BoolPtr(enabled)}
	newAliases := make([]model.Alias, 0, len(s.config.Aliases)+1)
	replaced := false

	for _, a := range s.config.Aliases {
		if a.Alias == oldAlias {
			if !replaced {
				newAliases = append(newAliases, updated)
				replaced = true
			}
			continue
		}
		if oldAlias != alias && a.Alias == alias {
			continue
		}
		newAliases = append(newAliases, a)
	}

	if !replaced {
		upserted := false
		for i := range newAliases {
			if newAliases[i].Alias == alias {
				newAliases[i] = updated
				upserted = true
				break
			}
		}
		if !upserted {
			newAliases = append(newAliases, updated)
		}
	}

	s.config.Aliases = newAliases
	s.config.Links = legacyLinksFromAliases(s.config.Aliases)

	return s.configCopy(), nil
}

// SetAliasEnabled updates the enabled state for an existing alias.
func (s *LinkStore) SetAliasEnabled(alias string, enabled bool) (model.Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.config.Aliases {
		if s.config.Aliases[i].Alias != alias {
			continue
		}
		s.config.Aliases[i].Enabled = model.BoolPtr(enabled)
		s.config.Links = legacyLinksFromAliases(s.config.Aliases)
		return s.configCopy(), nil
	}

	return model.Config{}, fmt.Errorf("alias not found")
}

// Alias returns one alias by exact alias pattern.
func (s *LinkStore) Alias(alias string) (model.Alias, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, a := range s.config.Aliases {
		if a.Alias == alias {
			return cloneAlias(a), true
		}
	}

	return model.Alias{}, false
}

// DeleteAlias removes an alias by exact alias pattern and returns the updated config.
func (s *LinkStore) DeleteAlias(alias string) model.Config {
	s.mu.Lock()
	defer s.mu.Unlock()

	kept := make([]model.Alias, 0, len(s.config.Aliases))
	for _, a := range s.config.Aliases {
		if a.Alias != alias {
			kept = append(kept, a)
		}
	}
	s.config.Aliases = kept
	s.config.Links = legacyLinksFromAliases(s.config.Aliases)

	return s.configCopy()
}

// Links returns legacy exact aliases as a link map.
func (s *LinkStore) Links() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string]string)
	for _, a := range s.config.Aliases {
		if !strings.Contains(a.Alias, "{") {
			result[a.Alias] = a.Destination
		}
	}
	return result
}

// Rules returns no legacy rules in alias mode.
func (s *LinkStore) Rules() []model.Rule {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]model.Rule, len(s.config.Rules))
	copy(result, s.config.Rules)
	return result
}

// AddLink adds a static link and returns the updated config.
func (s *LinkStore) AddLink(name, url string) model.Config {
	cfg, err := s.AddAlias(name, url)
	if err != nil {
		return s.Config()
	}
	return cfg
}

// DeleteLink removes a static link and returns the updated config.
func (s *LinkStore) DeleteLink(name string) model.Config {
	return s.DeleteAlias(name)
}

// AddRule adds a legacy rule and returns the updated config.
func (s *LinkStore) AddRule(rule model.Rule) model.Config {
	s.mu.Lock()
	defer s.mu.Unlock()

	alias, destination := legacyRuleToAlias(rule)
	if err := ValidateAlias(alias, destination); err != nil {
		return s.configCopy()
	}

	s.config.Rules = append(s.config.Rules, rule)
	s.config.Aliases = append(s.config.Aliases, model.Alias{Alias: alias, Destination: destination})
	s.config.Links = legacyLinksFromAliases(s.config.Aliases)
	return s.configCopy()
}

// DeleteRule removes a legacy rule by name. In alias mode this is a no-op.
func (s *LinkStore) DeleteRule(name string) model.Config {
	s.mu.Lock()
	defer s.mu.Unlock()

	remainingRules := make([]model.Rule, 0, len(s.config.Rules))
	aliasesToDelete := make(map[string]struct{})
	for _, r := range s.config.Rules {
		if r.Name == name {
			alias, _ := legacyRuleToAlias(r)
			aliasesToDelete[alias] = struct{}{}
			continue
		}
		remainingRules = append(remainingRules, r)
	}

	remainingAliases := make([]model.Alias, 0, len(s.config.Aliases))
	for _, a := range s.config.Aliases {
		if _, drop := aliasesToDelete[a.Alias]; drop {
			continue
		}
		remainingAliases = append(remainingAliases, a)
	}

	s.config.Rules = remainingRules
	s.config.Aliases = remainingAliases
	s.config.Links = legacyLinksFromAliases(s.config.Aliases)
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
	s.config = normalizeConfig(cfg)
}

// configCopy returns a deep copy of config. Caller must hold at least a read lock.
func (s *LinkStore) configCopy() model.Config {
	aliases := make([]model.Alias, len(s.config.Aliases))
	for i := range s.config.Aliases {
		aliases[i] = cloneAlias(s.config.Aliases[i])
	}
	links := make(map[string]string, len(s.config.Links))
	for k, v := range s.config.Links {
		links[k] = v
	}
	rules := make([]model.Rule, len(s.config.Rules))
	copy(rules, s.config.Rules)

	return model.Config{Aliases: aliases, Links: links, Rules: rules}
}

func normalizeConfig(cfg model.Config) model.Config {
	if cfg.Links == nil {
		cfg.Links = make(map[string]string)
	}
	if cfg.Rules == nil {
		cfg.Rules = []model.Rule{}
	}

	aliases := make([]model.Alias, 0, len(cfg.Aliases)+len(cfg.Links)+len(cfg.Rules))
	aliases = append(aliases, cfg.Aliases...)

	for name, url := range cfg.Links {
		aliases = append(aliases, model.Alias{Alias: name, Destination: url})
	}

	for _, r := range cfg.Rules {
		alias, destination := legacyRuleToAlias(r)
		aliases = append(aliases, model.Alias{Alias: alias, Destination: destination})
	}

	if aliases == nil {
		aliases = []model.Alias{}
	}

	seen := make(map[string]struct{}, len(aliases))
	dedup := make([]model.Alias, 0, len(aliases))
	for i := len(aliases) - 1; i >= 0; i-- {
		a := cloneAlias(aliases[i])
		a.Alias = strings.Trim(a.Alias, "/")
		a.Destination = strings.TrimSpace(a.Destination)
		if a.Alias == "" || a.Destination == "" {
			continue
		}
		if _, ok := seen[a.Alias]; ok {
			continue
		}
		seen[a.Alias] = struct{}{}
		dedup = append(dedup, a)
	}

	for i, j := 0, len(dedup)-1; i < j; i, j = i+1, j-1 {
		dedup[i], dedup[j] = dedup[j], dedup[i]
	}

	return model.Config{
		Aliases: dedup,
		Links:   legacyLinksFromAliases(dedup),
		Rules:   append([]model.Rule(nil), cfg.Rules...),
	}
}

func legacyLinksFromAliases(aliases []model.Alias) map[string]string {
	links := make(map[string]string)
	for _, a := range aliases {
		if !a.IsEnabled() {
			continue
		}
		if strings.Contains(a.Alias, "{") {
			continue
		}
		links[a.Alias] = a.Destination
	}
	return links
}

func cloneAlias(a model.Alias) model.Alias {
	cloned := model.Alias{
		Alias:       a.Alias,
		Destination: a.Destination,
	}
	if a.Enabled != nil {
		cloned.Enabled = model.BoolPtr(*a.Enabled)
	}
	return cloned
}

func legacyRuleToAlias(rule model.Rule) (string, string) {
	if rule.Type == "prefix" {
		base := strings.Trim(rule.Pattern, "/")
		if base == "" {
			return "{rest...}", strings.TrimRight(rule.Redirect, "/") + "/{rest...}"
		}
		return base + "/{rest...}", strings.TrimRight(rule.Redirect, "/") + "/{rest...}"
	}
	return strings.Trim(rule.Pattern, "/"), rule.Redirect
}

func matchAlias(path, aliasPattern, destination string) (string, bool) {
	pathParts := splitPath(path)
	patternParts := splitPath(aliasPattern)

	captures := make(map[string]string)
	unnamedCount := 0
	i := 0
	for ; i < len(patternParts); i++ {
		part := patternParts[i]

		name, placeholder, greedy := parsePlaceholder(part)
		if !placeholder {
			if i >= len(pathParts) || part != pathParts[i] {
				return "", false
			}
			continue
		}

		if greedy {
			if i > len(pathParts) {
				return "", false
			}
			captured := strings.Join(pathParts[i:], "/")
			if name == "" {
				unnamedCount++
				captures[fmt.Sprintf("{}#%d", unnamedCount)] = captured
				captures["{}"] = captured
			} else {
				captures["{"+name+"...}"] = captured
				captures["{"+name+"}"] = captured
			}
			i = len(pathParts)
			break
		}

		if i >= len(pathParts) {
			return "", false
		}
		if name == "" {
			unnamedCount++
			captures[fmt.Sprintf("{}#%d", unnamedCount)] = pathParts[i]
			captures["{}"] = pathParts[i]
		} else {
			captures["{"+name+"}"] = pathParts[i]
		}
	}

	if i < len(pathParts) {
		return "", false
	}

	resolved := destination
	for k, v := range captures {
		resolved = strings.ReplaceAll(resolved, k, v)
	}
	return resolved, true
}

func splitPath(path string) []string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}

func parsePlaceholder(seg string) (name string, placeholder bool, greedy bool) {
	if len(seg) < 2 || !strings.HasPrefix(seg, "{") || !strings.HasSuffix(seg, "}") {
		return "", false, false
	}
	body := strings.TrimSuffix(strings.TrimPrefix(seg, "{"), "}")
	body = strings.TrimSpace(body)
	if strings.HasSuffix(body, "...") {
		return strings.TrimSuffix(body, "..."), true, true
	}
	return body, true, false
}

// ValidateAlias validates alias placeholder semantics.
func ValidateAlias(alias, destination string) error {
	alias = strings.Trim(alias, "/")
	destination = strings.TrimSpace(destination)
	if alias == "" || destination == "" {
		return fmt.Errorf("alias and destination are required")
	}

	pls := placeholdersInAlias(alias)
	if len(pls) > 1 {
		seen := map[string]struct{}{}
		for _, p := range pls {
			if p == "{}" {
				return fmt.Errorf("multiple placeholders must be uniquely named")
			}
			if _, ok := seen[p]; ok {
				return fmt.Errorf("placeholder %s is duplicated", p)
			}
			seen[p] = struct{}{}
			if !strings.Contains(destination, p) {
				return fmt.Errorf("destination must include placeholder %s", p)
			}
		}
	}

	if len(pls) == 1 {
		p := pls[0]
		if p != "{}" && !strings.Contains(destination, p) {
			return fmt.Errorf("destination must include placeholder %s", p)
		}
	}

	for _, p := range placeholdersInString(destination) {
		if p == "{}" {
			if len(pls) != 1 || pls[0] != "{}" {
				return fmt.Errorf("destination placeholder {} requires alias placeholder {}")
			}
			continue
		}
		found := false
		for _, ap := range pls {
			if ap == p || ap == strings.TrimSuffix(p, "...}")+"}" {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("destination placeholder %s is not defined in alias", p)
		}
	}

	return nil
}

func placeholdersInAlias(alias string) []string {
	parts := splitPath(alias)
	out := make([]string, 0)
	for _, p := range parts {
		name, ok, greedy := parsePlaceholder(p)
		if !ok {
			continue
		}
		if name == "" {
			out = append(out, "{}")
			continue
		}
		if greedy {
			out = append(out, "{"+name+"...}")
			continue
		}
		out = append(out, "{"+name+"}")
	}
	return out
}

func placeholdersInString(s string) []string {
	out := make([]string, 0)
	start := -1
	for i, r := range s {
		if r == '{' {
			start = i
			continue
		}
		if r == '}' && start >= 0 {
			out = append(out, s[start:i+1])
			start = -1
		}
	}
	return out
}
