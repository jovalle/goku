package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/jovalle/goku/internal/model"
)

// Load reads a YAML config file, or returns an empty config if it does not exist.
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

// Save writes the config to YAML atomically.
func Save(path string, cfg model.Config) error {
	data, err := marshalConfigYAML(cfg)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	defer tmp.Close()

	if err := tmp.Chmod(0644); err != nil {
		return fmt.Errorf("setting temp file permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("syncing temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
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
