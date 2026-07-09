package config

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/jovalle/goku/internal/model"
)

// Load reads and parses a YAML config file.
// If the file does not exist, it returns a default empty config.
func Load(path string) (model.Config, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return model.Config{
			Aliases: []model.Alias{},
		}, nil
	}
	if err != nil {
		return model.Config{}, fmt.Errorf("opening config %s: %w", path, err)
	}
	defer f.Close()

	var cfg model.Config
	if err := yaml.NewDecoder(f).Decode(&cfg); err != nil {
		return model.Config{}, fmt.Errorf("parsing config %s: %w", path, err)
	}

	if cfg.Aliases == nil {
		cfg.Aliases = []model.Alias{}
	}

	return cfg, nil
}

// Save writes the config back to YAML atomically (write to tmp, then rename).
func Save(path string, cfg model.Config) error {
	if cfg.Aliases == nil {
		cfg.Aliases = []model.Alias{}
	}
	cfg.Aliases = dedupeAliases(cfg.Aliases)

	data, err := marshalConfigYAML(cfg)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("writing temp file: %w", err)
	}

	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("renaming temp file: %w", err)
	}

	return nil
}

func marshalConfigYAML(cfg model.Config) ([]byte, error) {
	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(cfg); err != nil {
		encoder.Close()
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func dedupeAliases(aliases []model.Alias) []model.Alias {
	seen := make(map[string]struct{}, len(aliases))
	deduped := make([]model.Alias, 0, len(aliases))
	for i := len(aliases) - 1; i >= 0; i-- {
		alias := aliases[i]
		alias.Alias = strings.Trim(alias.Alias, "/")
		alias.Destination = normalizeDestination(alias.Destination)
		if alias.Alias == "" || alias.Destination == "" {
			continue
		}
		if _, ok := seen[alias.Alias]; ok {
			continue
		}
		seen[alias.Alias] = struct{}{}
		deduped = append(deduped, alias)
	}
	for i, j := 0, len(deduped)-1; i < j; i, j = i+1, j-1 {
		deduped[i], deduped[j] = deduped[j], deduped[i]
	}
	return deduped
}

func normalizeDestination(destination string) string {
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
