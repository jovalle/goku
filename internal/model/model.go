package model

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// Config represents the full goku configuration loaded from YAML.
type Config struct {
	Aliases []Alias `yaml:"aliases,omitempty"`
}

// Alias defines a single alias pattern and destination URL template.
//
// Examples:
//   - alias: "gh"
//     destination: "https://github.com"
//   - alias: "gh/{owner}/{repo}"
//     destination: "https://github.com/{owner}/{repo}"
type Alias struct {
	Alias       string `yaml:"alias" json:"alias"`
	Destination string `yaml:"destination" json:"destination"`
	Enabled     *bool  `yaml:"enabled,omitempty" json:"enabled,omitempty"`
}

// MarshalYAML keeps URL-like values quoted for stable, parser-friendly config.
func (a Alias) MarshalYAML() (any, error) {
	node := &yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Value: "alias"},
			{Kind: yaml.ScalarNode, Value: a.Alias, Tag: "!!str", Style: yaml.DoubleQuotedStyle},
			{Kind: yaml.ScalarNode, Value: "destination"},
			{Kind: yaml.ScalarNode, Value: a.Destination, Tag: "!!str", Style: yaml.DoubleQuotedStyle},
		},
	}
	if a.Enabled != nil {
		node.Content = append(node.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "enabled"},
			&yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%t", *a.Enabled), Tag: "!!bool"},
		)
	}
	return node, nil
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
