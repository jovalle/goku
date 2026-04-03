package config

import (
	"fmt"
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
			Links:   map[string]string{},
			Rules:   []model.Rule{},
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
	if cfg.Links == nil {
		cfg.Links = make(map[string]string)
	}
	if cfg.Rules == nil {
		cfg.Rules = []model.Rule{}
	}

	// Backward compatibility: merge legacy links/rules into aliases.
	for name, dest := range cfg.Links {
		cfg.Aliases = append(cfg.Aliases, model.Alias{Alias: strings.Trim(name, "/"), Destination: strings.TrimSpace(dest), Enabled: model.BoolPtr(true)})
	}
	for _, rule := range cfg.Rules {
		if rule.Type == "prefix" {
			cfg.Aliases = append(cfg.Aliases, model.Alias{
				Alias:       strings.Trim(rule.Pattern, "/") + "/{rest...}",
				Destination: strings.TrimRight(rule.Redirect, "/") + "/{rest...}",
				Enabled:     model.BoolPtr(true),
			})
			continue
		}
		cfg.Aliases = append(cfg.Aliases, model.Alias{Alias: strings.Trim(rule.Pattern, "/"), Destination: strings.TrimSpace(rule.Redirect), Enabled: model.BoolPtr(true)})
	}

	return cfg, nil
}

// Save writes the config back to YAML atomically (write to tmp, then rename).
func Save(path string, cfg model.Config) error {
	if cfg.Aliases == nil {
		cfg.Aliases = []model.Alias{}
	}
	if cfg.Links == nil {
		cfg.Links = make(map[string]string)
		for _, a := range cfg.Aliases {
			if !a.IsEnabled() {
				continue
			}
			if strings.Contains(a.Alias, "{") {
				continue
			}
			cfg.Links[a.Alias] = a.Destination
		}
	}
	if cfg.Rules == nil {
		cfg.Rules = []model.Rule{}
	}

	data, err := yaml.Marshal(cfg)
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
