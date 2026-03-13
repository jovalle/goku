package model

import "fmt"

// Config represents the full goku configuration loaded from YAML.
type Config struct {
	Links map[string]string `yaml:"links"`
	Rules []Rule            `yaml:"rules"`
}

// Rule defines a dynamic redirect pattern.
type Rule struct {
	Name     string `yaml:"name"`
	Type     string `yaml:"type"`     // "prefix" or "template"
	Pattern  string `yaml:"pattern"`
	Redirect string `yaml:"redirect"`
}

// String implements fmt.Stringer for readable logging.
func (r Rule) String() string {
	return fmt.Sprintf("[%s] %s: %s -> %s", r.Type, r.Name, r.Pattern, r.Redirect)
}
