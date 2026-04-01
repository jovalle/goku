package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/jovalle/goku/internal/model"
)

// Load reads and parses a YAML config file.
// If the file does not exist, it returns a default empty config.
func Load(path string) (model.Config, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return model.Config{
			Links: make(map[string]string),
			Rules: []model.Rule{},
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

	if cfg.Links == nil {
		cfg.Links = make(map[string]string)
	}
	if cfg.Rules == nil {
		cfg.Rules = []model.Rule{}
	}

	return cfg, nil
}

// Save writes the config back to YAML atomically (write to tmp, then rename).
func Save(path string, cfg model.Config) error {
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
