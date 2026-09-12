package server

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"path"
	"strings"

	"github.com/jovalle/goku/internal/model"
	"github.com/jovalle/goku/internal/store"
)

func validateAliasInput(alias string, oldAlias string, destination string, aliases []model.Alias) validationFieldResult {
	result := validationFieldResult{
		State:      "success",
		Message:    "Alias is unique.",
		Normalized: alias,
	}

	if alias != "" {
		if err := store.ValidatePlaceholderSyntax(alias); err != nil {
			result.State = "error"
			result.Message = err.Error()
			return result
		}
	}

	if destination != "" {
		if err := store.ValidateAlias(alias, destination); err != nil {
			result.State = "error"
			result.Message = err.Error()
			return result
		}
	}

	for _, existing := range aliases {
		if existing.Alias == oldAlias {
			continue
		}
		if existing.Alias == alias {
			return validationFieldResult{
				State:      "error",
				Message:    fmt.Sprintf("Alias already exists as %s.", existing.Alias),
				Normalized: alias,
				Matches:    []string{existing.Alias},
			}
		}
		if aliasSameShape(alias, existing.Alias) {
			return validationFieldResult{
				State:      "error",
				Message:    fmt.Sprintf("Alias conflicts with %s.", existing.Alias),
				Normalized: alias,
				Matches:    []string{existing.Alias},
			}
		}
	}

	overlaps := make([]string, 0)
	for _, existing := range aliases {
		if existing.Alias == oldAlias || existing.Alias == alias {
			continue
		}
		if aliasesHaveAmbiguousOverlap(alias, existing.Alias) {
			overlaps = append(overlaps, existing.Alias)
		}
	}
	if len(overlaps) > 0 {
		result.State = "warning"
		result.Message = "Alias may overlap with " + strings.Join(overlaps, ", ") + "."
		result.Matches = overlaps
	}

	return result
}

func validateDestinationInput(ctx context.Context, checker *aliasStatusChecker, oldAlias string, alias string, destination string, aliases []model.Alias) validationFieldResult {
	result := validationFieldResult{
		State:      "success",
		Message:    "Destination is unique and reachable.",
		Normalized: destination,
	}

	if err := store.ValidateDestination(destination); err != nil {
		result.State = "error"
		result.Message = err.Error()
		return result
	}

	matches := make([]string, 0)
	destinationIdentity := normalizedDestinationIdentity(destination)
	for _, existing := range aliases {
		if existing.Alias == oldAlias {
			continue
		}
		existingDestination := store.NormalizeDestination(existing.Destination)
		if destinationIdentity == normalizedDestinationIdentity(existingDestination) {
			matches = append(matches, existing.Alias)
		}
	}
	if len(matches) > 0 {
		result.State = "warning"
		result.Message = "Destination is already used by " + strings.Join(matches, ", ") + "."
		result.Matches = matches
	}

	effectiveDestination := store.DestinationWithAliasDefaults(alias, destination)
	probe := checker.check(ctx, statusProbeURL(alias, destination))
	if placeholderTokenPattern.MatchString(effectiveDestination) {
		probe = placeholderAwareProbe(effectiveDestination, probe)
	}
	if probe.State != "online" {
		result.State = "warning"
		if len(matches) > 0 {
			result.Message += " "
		} else {
			result.Message = ""
		}
		result.Message += "Reachability: " + probe.Detail + "."
		return result
	}
	if len(matches) > 0 {
		return result
	}

	result.Message = "Destination is reachable."
	return result
}

func normalizedDestinationIdentity(destination string) string {
	normalized := store.NormalizeDestination(destination)
	parsed, err := url.Parse(normalized)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return normalized
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	if parsed.Path == "/" {
		parsed.Path = ""
	}
	return parsed.String()
}

type aliasSegment struct {
	value       string
	wildcard    bool
	greedy      bool
	description string
}

func aliasSameShape(a string, b string) bool {
	aSegs := parseAliasSegments(a)
	bSegs := parseAliasSegments(b)
	if len(aSegs) != len(bSegs) {
		return false
	}
	for i := range aSegs {
		if aSegs[i].wildcard || bSegs[i].wildcard {
			if !aSegs[i].wildcard || !bSegs[i].wildcard || aSegs[i].greedy != bSegs[i].greedy {
				return false
			}
			continue
		}
		if aSegs[i].value != bSegs[i].value {
			return false
		}
	}
	return true
}

func aliasesHaveAmbiguousOverlap(a string, b string) bool {
	return aliasesOverlap(a, b) && store.CompareAliasSpecificity(a, b) == 0
}

func aliasesOverlap(a string, b string) bool {
	aSegs := parseAliasSegments(a)
	bSegs := parseAliasSegments(b)
	seen := map[string]bool{}
	var walk func(int, int) bool
	walk = func(i int, j int) bool {
		key := fmt.Sprintf("%d:%d", i, j)
		if seen[key] {
			return false
		}
		seen[key] = true

		if i == len(aSegs) && j == len(bSegs) {
			return true
		}
		if i < len(aSegs) && aSegs[i].greedy {
			if walk(i+1, j) {
				return true
			}
			if j < len(bSegs) && walk(i, j+1) {
				return true
			}
		}
		if j < len(bSegs) && bSegs[j].greedy {
			if walk(i, j+1) {
				return true
			}
			if i < len(aSegs) && walk(i+1, j) {
				return true
			}
		}
		if i == len(aSegs) || j == len(bSegs) {
			return false
		}
		if !aliasSegmentsCompatible(aSegs[i], bSegs[j]) {
			return false
		}
		return walk(i+1, j+1)
	}
	return walk(0, 0)
}

func parseAliasSegments(alias string) []aliasSegment {
	parts := strings.Split(strings.Trim(alias, "/"), "/")
	if len(parts) == 1 && parts[0] == "" {
		return nil
	}
	segments := make([]aliasSegment, 0, len(parts))
	for _, part := range parts {
		body := strings.TrimSuffix(strings.TrimPrefix(part, "{"), "}")
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			segments = append(segments, aliasSegment{
				value:       body,
				wildcard:    true,
				greedy:      strings.HasSuffix(body, "..."),
				description: part,
			})
			continue
		}
		segments = append(segments, aliasSegment{value: part, description: part})
	}
	return segments
}

func aliasSegmentsCompatible(a aliasSegment, b aliasSegment) bool {
	if a.wildcard || b.wildcard {
		return true
	}
	return a.value == b.value
}

func isHTTPProbeURL(probeURL string) bool {
	parsed, err := url.Parse(probeURL)
	if err != nil {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

func stripPlaceholderValues(alias string, destination string) string {
	effectiveDestination := store.DestinationWithAliasDefaults(alias, destination)
	return replacePlaceholderTokens(effectiveDestination, "")
}

func destinationHref(alias string, destination string) string {
	effectiveDestination := store.DestinationWithAliasDefaults(alias, destination)
	href := store.NormalizeDestination(replacePlaceholderTokens(effectiveDestination, ""))
	parsed, err := url.Parse(href)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return href
	}

	host := strings.Trim(parsed.Hostname(), ".")
	for strings.Contains(host, "..") {
		host = strings.ReplaceAll(host, "..", ".")
	}
	if host == "" {
		return href
	}
	if port := parsed.Port(); port != "" {
		host = net.JoinHostPort(host, port)
	}
	parsed.Host = host

	if parsed.Path != "" {
		trailingSlash := strings.HasSuffix(parsed.Path, "/")
		parsed.Path = path.Clean(parsed.Path)
		if parsed.Path == "." {
			parsed.Path = ""
		}
		if trailingSlash && parsed.Path != "/" {
			parsed.Path += "/"
		}
	}

	return parsed.String()
}

func replacePlaceholderTokens(destination string, fallback string) string {
	return placeholderTokenPattern.ReplaceAllStringFunc(destination, func(token string) string {
		if value, ok := placeholderDefaultValue(token); ok {
			return value
		}
		return fallback
	})
}

func placeholderDefaultValue(token string) (string, bool) {
	if len(token) < 2 || !strings.HasPrefix(token, "{") || !strings.HasSuffix(token, "}") {
		return "", false
	}
	body := strings.TrimSuffix(strings.TrimPrefix(token, "{"), "}")
	_, value, ok := strings.Cut(body, ":=")
	if !ok {
		return "", false
	}
	return strings.TrimSpace(value), true
}
