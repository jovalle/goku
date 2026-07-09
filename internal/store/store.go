package store

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"

	"github.com/jovalle/goku/internal/model"
	"github.com/jovalle/goku/internal/resolve"
)

// AliasStore holds the current config and resolves alias paths to URLs.
// All methods are safe for concurrent use.
type AliasStore struct {
	mu     sync.RWMutex
	config model.Config
}

// New creates an AliasStore with nil-safe defaults.
func New(cfg model.Config) *AliasStore {
	cfg = normalizeConfig(cfg)
	return &AliasStore{config: cfg}
}

// Resolve finds the redirect URL for an alias path.
func (s *AliasStore) Resolve(path string) (string, error) {
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
func (s *AliasStore) Aliases() []model.Alias {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]model.Alias, len(s.config.Aliases))
	for i := range s.config.Aliases {
		result[i] = cloneAlias(s.config.Aliases[i])
	}
	return result
}

// AddAlias adds or replaces an alias and returns the updated config.
func (s *AliasStore) AddAlias(alias, destination string) (model.Config, error) {
	return s.UpsertAlias(model.Alias{Alias: alias, Destination: destination, Enabled: model.BoolPtr(true)})
}

// UpsertAlias adds or replaces an alias and preserves the provided enabled state.
func (s *AliasStore) UpsertAlias(alias model.Alias) (model.Config, error) {
	normalizedAlias, normalizedDestination, err := NormalizeAliasAndDestination(alias.Alias, alias.Destination)
	if err != nil {
		return model.Config{}, err
	}
	alias.Alias = normalizedAlias
	alias.Destination = normalizedDestination
	if err := ValidateAlias(alias.Alias, alias.Destination); err != nil {
		return model.Config{}, err
	}
	if alias.Enabled == nil {
		alias.Enabled = model.BoolPtr(true)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	replaced := false
	for i := range s.config.Aliases {
		if s.config.Aliases[i].Alias == alias.Alias {
			s.config.Aliases[i] = cloneAlias(alias)
			replaced = true
			break
		}
	}
	if !replaced {
		s.config.Aliases = append(s.config.Aliases, cloneAlias(alias))
	}
	return s.configCopy(), nil
}

// UpdateAlias edits an existing alias. If oldAlias does not exist, it upserts by alias.
func (s *AliasStore) UpdateAlias(oldAlias, alias, destination string, enabled bool) (model.Config, error) {
	alias, destination, err := NormalizeAliasAndDestination(alias, destination)
	if err != nil {
		return model.Config{}, err
	}
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
	return s.configCopy(), nil
}

// SetAliasEnabled updates the enabled state for an existing alias.
func (s *AliasStore) SetAliasEnabled(alias string, enabled bool) (model.Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.config.Aliases {
		if s.config.Aliases[i].Alias != alias {
			continue
		}
		s.config.Aliases[i].Enabled = model.BoolPtr(enabled)
		return s.configCopy(), nil
	}

	return model.Config{}, fmt.Errorf("alias not found")
}

// Alias returns one alias by exact alias pattern.
func (s *AliasStore) Alias(alias string) (model.Alias, bool) {
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
func (s *AliasStore) DeleteAlias(alias string) model.Config {
	return s.DeleteAliases([]string{alias})
}

// DeleteAliases removes aliases by exact alias pattern and returns the updated config.
func (s *AliasStore) DeleteAliases(aliases []string) model.Config {
	s.mu.Lock()
	defer s.mu.Unlock()

	toDelete := make(map[string]struct{}, len(aliases))
	for _, alias := range aliases {
		trimmed := strings.Trim(alias, "/")
		if trimmed == "" {
			continue
		}
		toDelete[trimmed] = struct{}{}
	}

	kept := make([]model.Alias, 0, len(s.config.Aliases))
	for _, a := range s.config.Aliases {
		if _, drop := toDelete[a.Alias]; drop {
			continue
		}
		kept = append(kept, a)
	}
	s.config.Aliases = kept
	return s.configCopy()
}

// Config returns a copy of the current config.
func (s *AliasStore) Config() model.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.configCopy()
}

// Update atomically replaces the config (used by the file watcher).
func (s *AliasStore) Update(cfg model.Config) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config = normalizeConfig(cfg)
}

// configCopy returns a deep copy of config. Caller must hold at least a read lock.
func (s *AliasStore) configCopy() model.Config {
	aliases := make([]model.Alias, len(s.config.Aliases))
	for i := range s.config.Aliases {
		aliases[i] = cloneAlias(s.config.Aliases[i])
	}

	return model.Config{Aliases: aliases}
}

func normalizeConfig(cfg model.Config) model.Config {
	aliases := make([]model.Alias, 0, len(cfg.Aliases))
	aliases = append(aliases, cfg.Aliases...)

	seen := make(map[string]struct{}, len(aliases))
	dedup := make([]model.Alias, 0, len(aliases))
	for i := len(aliases) - 1; i >= 0; i-- {
		a := cloneAlias(aliases[i])
		a.Alias = strings.Trim(a.Alias, "/")
		a.Destination = NormalizeDestination(a.Destination)
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
	}
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
	for _, token := range placeholdersInString(destination) {
		if value, ok := captures[canonicalPlaceholder(token)]; ok {
			resolved = strings.ReplaceAll(resolved, token, value)
		}
	}
	for key, value := range captures {
		resolved = strings.ReplaceAll(resolved, key, value)
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
	name, _, placeholder, greedy = parsePlaceholderSpec(seg)
	return name, placeholder, greedy
}

func parsePlaceholderSpec(seg string) (name string, defaultValue string, placeholder bool, greedy bool) {
	if len(seg) < 2 || !strings.HasPrefix(seg, "{") || !strings.HasSuffix(seg, "}") {
		return "", "", false, false
	}
	body := strings.TrimSuffix(strings.TrimPrefix(seg, "{"), "}")
	body = strings.TrimSpace(body)
	if before, after, ok := strings.Cut(body, ":="); ok {
		body = strings.TrimSpace(before)
		defaultValue = strings.TrimSpace(after)
	}
	if strings.HasSuffix(body, "...") {
		return strings.TrimSuffix(body, "..."), defaultValue, true, true
	}
	return body, defaultValue, true, false
}

// ValidatePlaceholderSyntax validates placeholder default syntax in a string.
func ValidatePlaceholderSyntax(value string) error {
	for _, token := range placeholdersInString(value) {
		if err := validatePlaceholderTokenSyntax(token); err != nil {
			return err
		}
	}
	return nil
}

func validatePlaceholderTokenSyntax(token string) error {
	body := strings.TrimSuffix(strings.TrimPrefix(token, "{"), "}")
	body = strings.TrimSpace(body)
	name := body
	if before, after, ok := strings.Cut(body, ":="); ok {
		name = strings.TrimSpace(before)
		if strings.TrimSpace(after) == "" {
			return fmt.Errorf("placeholder default in %s needs a value, e.g. {owner:=jovalle}", token)
		}
	} else if strings.Contains(body, ":") {
		return fmt.Errorf("placeholder default in %s must use :=, e.g. {owner:=jovalle}", token)
	}
	if strings.Contains(name, ":") {
		return fmt.Errorf("placeholder %s has invalid syntax", token)
	}
	return nil
}

// NormalizeAliasAndDestination normalizes an alias pair while keeping defaults on aliases only.
func NormalizeAliasAndDestination(alias, destination string) (string, string, error) {
	alias = strings.Trim(alias, "/")
	destination = NormalizeDestination(destination)

	if err := ValidatePlaceholderSyntax(alias); err != nil {
		return "", "", err
	}
	if err := ValidateDestinationPlaceholderSyntax(destination); err != nil {
		return "", "", err
	}
	return alias, destination, nil
}

// DestinationWithAliasDefaults returns a destination template with matching alias defaults applied.
func DestinationWithAliasDefaults(alias, destination string) string {
	defaults, err := aliasPlaceholderDefaults(alias)
	if err != nil || len(defaults) == 0 {
		return destination
	}
	return applyPlaceholderDefaults(destination, defaults)
}

// StripPlaceholderDefaults removes default values from placeholder tokens for directory display.
func StripPlaceholderDefaults(value string) string {
	for _, token := range placeholdersInString(value) {
		name, _, ok, greedy := parsePlaceholderSpec(token)
		if !ok {
			continue
		}
		replacement := ""
		if greedy {
			replacement = "{" + name + "...}"
		} else if name == "" {
			replacement = "{}"
		} else {
			replacement = "{" + name + "}"
		}
		if replacement != token {
			value = strings.ReplaceAll(value, token, replacement)
		}
	}
	return value
}

// ValidateDestinationPlaceholderSyntax rejects destination defaults. Defaults belong to aliases.
func ValidateDestinationPlaceholderSyntax(value string) error {
	if err := ValidatePlaceholderSyntax(value); err != nil {
		return err
	}
	for _, token := range placeholdersInString(value) {
		_, defaultValue, ok, _ := parsePlaceholderSpec(token)
		if ok && defaultValue != "" {
			return fmt.Errorf("placeholder defaults must be defined in alias, not destination")
		}
	}
	return nil
}

func aliasPlaceholderDefaults(alias string) (map[string]string, error) {
	defaults := map[string]string{}
	if err := ValidatePlaceholderSyntax(alias); err != nil {
		return nil, err
	}
	for _, token := range placeholdersInString(alias) {
		_, defaultValue, ok, _ := parsePlaceholderSpec(token)
		if !ok || defaultValue == "" {
			continue
		}
		canonical := canonicalPlaceholder(token)
		if existing, ok := defaults[canonical]; ok && existing != defaultValue {
			return nil, fmt.Errorf("placeholder %s has conflicting defaults %q and %q", canonical, existing, defaultValue)
		}
		defaults[canonical] = defaultValue
	}
	return defaults, nil
}

func applyPlaceholderDefaults(value string, defaults map[string]string) string {
	for _, token := range placeholdersInString(value) {
		replacement := placeholderWithInheritedDefault(token, defaults)
		if replacement != token {
			value = strings.ReplaceAll(value, token, replacement)
		}
	}
	return value
}

func placeholderWithInheritedDefault(token string, defaults map[string]string) string {
	name, defaultValue, ok, greedy := parsePlaceholderSpec(token)
	if !ok || defaultValue != "" {
		return token
	}
	defaultValue, ok = defaults[canonicalPlaceholder(token)]
	if !ok {
		return token
	}
	if greedy {
		return "{" + name + "...:=" + defaultValue + "}"
	}
	if name == "" {
		return "{:=" + defaultValue + "}"
	}
	return "{" + name + ":=" + defaultValue + "}"
}

// ValidateAlias validates alias placeholder semantics.
func ValidateAlias(alias, destination string) error {
	alias = strings.Trim(alias, "/")
	destination = NormalizeDestination(destination)
	if alias == "" || destination == "" {
		return fmt.Errorf("alias and destination are required")
	}
	if err := ValidatePlaceholderSyntax(alias); err != nil {
		return err
	}
	if err := validateDestinationURL(destination); err != nil {
		return err
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
			if !destinationContainsPlaceholder(destination, p) {
				return fmt.Errorf("destination must include placeholder %s", p)
			}
		}
	}

	if len(pls) == 1 {
		p := pls[0]
		if p != "{}" && !destinationContainsPlaceholder(destination, p) {
			return fmt.Errorf("destination must include placeholder %s", p)
		}
	}

	for _, p := range placeholdersInString(destination) {
		name, _, ok, greedy := parsePlaceholderSpec(p)
		if !ok {
			continue
		}
		canonical := "{}"
		if name != "" {
			canonical = "{" + name + "}"
			if greedy {
				canonical = "{" + name + "...}"
			}
		}
		if canonical == "{}" {
			if len(pls) != 1 || pls[0] != "{}" {
				return fmt.Errorf("destination placeholder {} requires alias placeholder {}")
			}
			continue
		}
		found := false
		for _, ap := range pls {
			if ap == canonical || ap == strings.TrimSuffix(canonical, "...}")+"}" {
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

func destinationContainsPlaceholder(destination string, canonical string) bool {
	for _, token := range placeholdersInString(destination) {
		if canonicalPlaceholder(token) == canonical {
			return true
		}
	}
	return false
}

func canonicalPlaceholder(token string) string {
	name, _, ok, greedy := parsePlaceholderSpec(token)
	if !ok || name == "" {
		return "{}"
	}
	if greedy {
		return "{" + name + "...}"
	}
	return "{" + name + "}"
}

func NormalizeDestination(destination string) string {
	destination = strings.TrimSpace(destination)
	if destination == "" || hasExplicitProtocol(destination) {
		return destination
	}
	if shouldDefaultHTTP(destination) {
		return "http://" + normalizeHTTPHostDestination(destination)
	}
	return "https://" + destination
}

func shouldDefaultHTTP(destination string) bool {
	host := destinationHost(destination)
	if host == "" {
		return false
	}
	return strings.EqualFold(host, "localhost") || net.ParseIP(strings.Trim(host, "[]")) != nil
}

func normalizeHTTPHostDestination(destination string) string {
	hostPart, rest, ok := splitDestinationHost(destination)
	if !ok {
		return destination
	}
	host := destinationHost(destination)
	if host == "" || strings.HasPrefix(hostPart, "[") || strings.Count(host, ":") < 2 {
		return destination
	}
	return "[" + host + "]" + rest
}

func destinationHost(destination string) string {
	hostPart, _, ok := splitDestinationHost(destination)
	if !ok {
		return ""
	}
	if strings.HasPrefix(hostPart, "[") {
		end := strings.Index(hostPart, "]")
		if end < 0 {
			return ""
		}
		return hostPart[1:end]
	}
	if strings.Count(hostPart, ":") == 1 {
		before, after, ok := strings.Cut(hostPart, ":")
		if ok && isPort(after) {
			return before
		}
	}
	return hostPart
}

func splitDestinationHost(destination string) (string, string, bool) {
	if destination == "" || strings.HasPrefix(destination, "/") {
		return "", "", false
	}
	index := strings.IndexAny(destination, "/?#")
	if index < 0 {
		return destination, "", true
	}
	return destination[:index], destination[index:], true
}

func isPort(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func hasExplicitProtocol(destination string) bool {
	colon := strings.Index(destination, ":")
	if colon <= 0 {
		return false
	}
	if !isSchemeName(destination[:colon]) {
		return false
	}
	if delimiter := strings.IndexAny(destination, "/?#"); delimiter >= 0 && delimiter < colon {
		return false
	}
	afterColon := destination[colon+1:]
	if slash := strings.IndexAny(afterColon, "/?#"); slash >= 0 {
		afterColon = afterColon[:slash]
	}
	if afterColon != "" {
		allDigits := true
		for _, r := range afterColon {
			if r < '0' || r > '9' {
				allDigits = false
				break
			}
		}
		if allDigits {
			return false
		}
	}
	return true
}

func isSchemeName(value string) bool {
	if value == "" {
		return false
	}
	for i, r := range value {
		if i == 0 {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				continue
			}
			return false
		}
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '+' || r == '-' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func validateDestinationURL(destination string) error {
	if strings.ContainsAny(destination, " \t\r\n") {
		return fmt.Errorf("destination must be a URL")
	}
	if err := ValidateDestinationPlaceholderSyntax(destination); err != nil {
		return err
	}

	candidate := destination
	for _, p := range placeholdersInString(destination) {
		candidate = strings.ReplaceAll(candidate, p, "value")
	}

	parsed, err := url.Parse(candidate)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("destination must be a URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("destination URL scheme must be http or https")
	}
	return nil
}

func placeholdersInAlias(alias string) []string {
	parts := splitPath(alias)
	out := make([]string, 0)
	for _, p := range parts {
		name, _, ok, greedy := parsePlaceholderSpec(p)
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
