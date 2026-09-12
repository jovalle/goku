package server

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/jovalle/goku/internal/model"
	"github.com/jovalle/goku/internal/store"
)

func detectImportFormat(r *http.Request) string {
	ct := strings.ToLower(r.Header.Get("Content-Type"))
	switch {
	case strings.Contains(ct, "json"):
		return "json"
	case strings.Contains(ct, "yaml"), strings.Contains(ct, "yml"):
		return "yaml"
	case strings.Contains(ct, "text/plain"):
		return "text"
	default:
		return "auto"
	}
}

func normalizeImportFormat(raw string) string {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "", "auto":
		return "auto"
	case "pseudo", "text", "plain", "txt":
		return "pseudo"
	case "yaml", "yml":
		return "yaml"
	case "json":
		return "json"
	default:
		return ""
	}
}

func (s *Server) buildImportPreview(body []byte, format string) (importPreviewResponse, error) {
	format = normalizeImportFormat(format)
	if format == "" {
		return importPreviewResponse{}, errors.New("format must be auto, pseudo, yaml, or json")
	}

	items, detected, err := parseImportItems(body, format)
	if err != nil {
		return importPreviewResponse{}, err
	}

	resp := importPreviewResponse{
		Format: detected,
		Items:  items,
	}
	existing := make(map[string]struct{}, len(s.store.Aliases()))
	for _, alias := range s.store.Aliases() {
		existing[alias.Alias] = struct{}{}
	}

	seen := make(map[string]int)
	for i := range resp.Items {
		item := &resp.Items[i]
		item.Index = i
		if item.Error != "" {
			item.Status = "invalid"
			resp.InvalidCount++
			continue
		}

		item.Alias = strings.Trim(item.Alias, "/")
		item.Destination = store.NormalizeDestination(item.Destination)
		if err := store.ValidateAlias(item.Alias, item.Destination); err != nil {
			item.Error = err.Error()
			item.Status = "invalid"
			resp.InvalidCount++
			continue
		}
		if firstLine, dup := seen[item.Alias]; dup {
			item.Error = "duplicate alias in import payload; first defined on line " + strconv.Itoa(firstLine)
			item.Status = "invalid"
			resp.InvalidCount++
			continue
		}
		seen[item.Alias] = item.Line
		item.Valid = true
		if _, ok := existing[item.Alias]; ok {
			item.Status = "replace"
			resp.ReplaceCount++
		} else {
			item.Status = "new"
			resp.NewCount++
		}
		resp.ValidCount++
	}

	return resp, nil
}

func parseImportItems(body []byte, format string) ([]importPreviewItem, string, error) {
	text := strings.TrimSpace(string(body))
	switch format {
	case "pseudo":
		return parseTextImportItems(text), "pseudo", nil
	case "json":
		items, err := parseStructuredImportItems(body, "json")
		return items, "json", err
	case "yaml":
		items, err := parseStructuredImportItems(body, "yaml")
		return items, "yaml", err
	case "auto":
		if json.Valid(body) {
			items, err := parseStructuredImportItems(body, "json")
			if err == nil {
				return items, "json", nil
			}
		}
		if items, err := parseStructuredImportItems(body, "yaml"); err == nil && len(items) > 0 {
			return items, "yaml", nil
		}
		return parseTextImportItems(text), "pseudo", nil
	default:
		return nil, "", errors.New("unsupported import format")
	}
}

func parseTextImportItems(body string) []importPreviewItem {
	items := make([]importPreviewItem, 0)
	scanner := bufio.NewScanner(strings.NewReader(body))
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		raw := scanner.Text()
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		item := importPreviewItem{
			Line:    lineNo,
			Source:  raw,
			Enabled: true,
		}
		if strings.Contains(line, ",") {
			parts := strings.SplitN(line, ",", 2)
			item.Alias = strings.TrimSpace(parts[0])
			item.Destination = strings.TrimSpace(parts[1])
		} else {
			parts := strings.Fields(line)
			if len(parts) != 2 {
				item.Error = "line must contain exactly alias and destination"
				items = append(items, item)
				continue
			}
			item.Alias = parts[0]
			item.Destination = parts[1]
		}
		items = append(items, item)
	}
	return items
}

func parseStructuredImportItems(body []byte, format string) ([]importPreviewItem, error) {
	aliases, err := parseStructuredAliases(body, format)
	if err != nil {
		if format == "json" {
			return nil, errors.New("invalid JSON payload")
		}
		return nil, errors.New("invalid YAML payload")
	}

	lineNumbers := structuredAliasLineNumbers(body, format)
	items := make([]importPreviewItem, 0, len(aliases))
	for i, alias := range aliases {
		enabled := alias.IsEnabled()
		items = append(items, importPreviewItem{
			Line:        nextStructuredAliasLine(lineNumbers, alias.Alias, i+1),
			Alias:       alias.Alias,
			Destination: alias.Destination,
			Enabled:     enabled,
			Source:      alias.Alias + " " + alias.Destination,
		})
	}
	return items, nil
}

func structuredAliasLineNumbers(body []byte, format string) map[string][]int {
	lines := map[string][]int{}
	patterns := []*regexp.Regexp{}
	if format == "json" {
		patterns = append(patterns, regexp.MustCompile(`"alias"\s*:\s*"([^"]*)"`))
	} else {
		patterns = append(patterns,
			regexp.MustCompile(`^\s*-\s*alias\s*:\s*(.+?)\s*$`),
			regexp.MustCompile(`^\s*alias\s*:\s*(.+?)\s*$`),
		)
	}

	scanner := bufio.NewScanner(bytes.NewReader(body))
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		for _, pattern := range patterns {
			matches := pattern.FindStringSubmatch(line)
			if len(matches) < 2 {
				continue
			}
			alias := cleanStructuredAliasScalar(matches[1])
			if alias == "" {
				continue
			}
			lines[alias] = append(lines[alias], lineNo)
			break
		}
	}
	return lines
}

func nextStructuredAliasLine(lines map[string][]int, alias string, fallback int) int {
	lineNumbers := lines[alias]
	if len(lineNumbers) == 0 {
		return fallback
	}
	line := lineNumbers[0]
	lines[alias] = lineNumbers[1:]
	return line
}

func cleanStructuredAliasScalar(value string) string {
	value = strings.TrimSpace(value)
	if comment := strings.Index(value, " #"); comment >= 0 {
		value = strings.TrimSpace(value[:comment])
	}
	value = strings.TrimSuffix(value, ",")
	if len(value) >= 2 {
		first := value[0]
		last := value[len(value)-1]
		if (first == '\'' && last == '\'') || (first == '"' && last == '"') {
			value = value[1 : len(value)-1]
		}
	}
	return strings.TrimSpace(value)
}

func parseStructuredAliases(body []byte, format string) ([]model.Alias, error) {
	var direct []importAliasInput
	if format == "json" {
		if err := json.Unmarshal(body, &direct); err == nil && len(direct) > 0 {
			return aliasInputsToAliases(direct), nil
		}
	} else {
		if err := yaml.Unmarshal(body, &direct); err == nil && len(direct) > 0 {
			return aliasInputsToAliases(direct), nil
		}
	}

	var directAliases []model.Alias
	if format == "json" {
		if err := json.Unmarshal(body, &directAliases); err == nil && len(directAliases) > 0 {
			return normalizeStructuredAliases(directAliases), nil
		}
	} else {
		if err := yaml.Unmarshal(body, &directAliases); err == nil && len(directAliases) > 0 {
			return normalizeStructuredAliases(directAliases), nil
		}
	}

	var payload importPayload
	if format == "json" {
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil, err
		}
	} else {
		if err := yaml.Unmarshal(body, &payload); err != nil {
			return nil, err
		}
	}

	aliases := make([]model.Alias, 0, len(payload.Aliases)+len(payload.Links))
	aliases = append(aliases, normalizeStructuredAliases(payload.Aliases)...)
	for alias, destination := range payload.Links {
		aliases = append(aliases, model.Alias{
			Alias:       alias,
			Destination: destination,
			Enabled:     new(true),
		})
	}
	if len(aliases) == 0 {
		var linkMap map[string]string
		if format == "json" {
			if err := json.Unmarshal(body, &linkMap); err == nil && len(linkMap) > 0 {
				return linksMapToAliases(linkMap), nil
			}
		} else {
			if err := yaml.Unmarshal(body, &linkMap); err == nil && len(linkMap) > 0 {
				return linksMapToAliases(linkMap), nil
			}
		}
		return nil, errors.New("no aliases found in payload")
	}
	return aliases, nil
}

func aliasInputsToAliases(inputs []importAliasInput) []model.Alias {
	aliases := make([]model.Alias, 0, len(inputs))
	for _, input := range inputs {
		alias := model.Alias{
			Alias:       input.Alias,
			Destination: input.Destination,
			Enabled:     input.Enabled,
		}
		if alias.Enabled == nil {
			alias.Enabled = new(true)
		}
		aliases = append(aliases, alias)
	}
	return aliases
}

func normalizeStructuredAliases(inputs []model.Alias) []model.Alias {
	aliases := make([]model.Alias, 0, len(inputs))
	for _, alias := range inputs {
		if alias.Enabled == nil {
			alias.Enabled = new(true)
		}
		aliases = append(aliases, alias)
	}
	return aliases
}

func linksMapToAliases(links map[string]string) []model.Alias {
	aliases := make([]model.Alias, 0, len(links))
	for alias, destination := range links {
		aliases = append(aliases, model.Alias{
			Alias:       alias,
			Destination: destination,
			Enabled:     new(true),
		})
	}
	slices.SortFunc(aliases, func(a, b model.Alias) int {
		return cmp.Compare(a.Alias, b.Alias)
	})
	return aliases
}

func importItemError(item importPreviewItem) string {
	if item.Line > 0 && item.Error != "" {
		return "line " + strconv.Itoa(item.Line) + ": " + item.Error
	}
	if item.Error != "" {
		return item.Error
	}
	return "invalid import item"
}
