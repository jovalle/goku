package model

import "fmt"

// Config represents the full goku configuration loaded from YAML.
type Config struct {
	Aliases []Alias `yaml:"aliases,omitempty"`

	// Legacy fields are still accepted on load and converted to aliases.
	Links map[string]string `yaml:"links,omitempty"`
	Rules []Rule            `yaml:"rules,omitempty"`
}

// Alias defines a single alias pattern and destination URL template.
//
// Examples:
//   - alias: gh
//     destination: https://github.com
//   - alias: gh/{owner}/{repo}
//     destination: https://github.com/{owner}/{repo}
type Alias struct {
	Alias       string `yaml:"alias" json:"alias"`
	Destination string `yaml:"destination" json:"destination"`
	Enabled     *bool  `yaml:"enabled,omitempty" json:"enabled,omitempty"`
}

// IsEnabled returns true when the alias is enabled.
// Nil means enabled for backward compatibility with existing configs.
func (a Alias) IsEnabled() bool {
	return a.Enabled == nil || *a.Enabled
}

// WithEnabled returns a copy with the given enabled state.
func (a Alias) WithEnabled(enabled bool) Alias {
	a.Enabled = BoolPtr(enabled)
	return a
}

// BoolPtr returns a pointer to the provided bool value.
func BoolPtr(v bool) *bool {
	b := v
	return &b
}

// Rule defines a dynamic redirect pattern.
type Rule struct {
	Name     string `yaml:"name"`
	Type     string `yaml:"type"` // "prefix" or "template"
	Pattern  string `yaml:"pattern"`
	Redirect string `yaml:"redirect"`
}

// String implements fmt.Stringer for readable logging.
func (r Rule) String() string {
	return fmt.Sprintf("[%s] %s: %s -> %s", r.Type, r.Name, r.Pattern, r.Redirect)
}
