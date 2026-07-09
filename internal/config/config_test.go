package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jovalle/goku/internal/model"
)

func TestLoad_Valid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	data := "aliases:\n  - alias: gh\n    destination: https://github.com\n  - alias: g\n    destination: https://google.com\n"
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if got, ok := findAlias(cfg.Aliases, "gh"); !ok || got.Destination != "https://github.com" {
		t.Fatalf("alias gh not loaded: %#v", cfg.Aliases)
	}
	if got, ok := findAlias(cfg.Aliases, "g"); !ok || got.Destination != "https://google.com" {
		t.Fatalf("alias g not loaded: %#v", cfg.Aliases)
	}
}

func TestLoad_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Error("expected error for empty YAML file")
	}
}

func TestLoad_MissingFile(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Aliases == nil {
		t.Error("Aliases should default to a non-nil slice for missing files")
	}
	if len(cfg.Aliases) != 0 {
		t.Errorf("expected no aliases for missing file, got %d", len(cfg.Aliases))
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte("{{not yaml}}"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestLoad_NilFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("aliases:\n  - alias: x\n    destination: https://x.com\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Aliases == nil {
		t.Error("Aliases should default to non-nil slice when missing from YAML")
	}
}

func TestSave_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	original := model.Config{
		Aliases: []model.Alias{
			{Alias: "gh", Destination: "https://github.com", Enabled: model.BoolPtr(true)},
			{Alias: "g", Destination: "https://google.com", Enabled: model.BoolPtr(true)},
		},
	}
	if err := Save(path, original); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() after Save error: %v", err)
	}
	if got, ok := findAlias(loaded.Aliases, "gh"); !ok || got.Destination != "https://github.com" {
		t.Fatalf("alias gh was not saved: %#v", loaded.Aliases)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(saved), "links:") {
		t.Fatalf("saved config should not emit links: %s", saved)
	}
}

func TestSave_UsesTwoSpaceIndentation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	cfg := model.Config{
		Aliases: []model.Alias{
			{Alias: "gh", Destination: "https://github.com", Enabled: model.BoolPtr(true)},
		},
	}

	if err := Save(path, cfg); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(saved)
	if !strings.Contains(text, "aliases:\n  - alias: \"gh\"\n    destination: \"https://github.com\"\n    enabled: true\n") {
		t.Fatalf("saved config did not use 2-space indentation:\n%s", text)
	}
	if strings.Contains(text, "\n    - alias:") {
		t.Fatalf("saved config used 4-space sequence indentation:\n%s", text)
	}
}

func TestSave_AtomicNoPreviousFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new.yaml")
	cfg := model.Config{Aliases: []model.Alias{{Alias: "x", Destination: "https://x.com", Enabled: model.BoolPtr(true)}}}
	if err := Save(path, cfg); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Error("temp file should be cleaned up after Save")
	}
}

func TestSave_OverwriteExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := Save(path, model.Config{Aliases: []model.Alias{{Alias: "a", Destination: "https://a.com", Enabled: model.BoolPtr(true)}}}); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, model.Config{Aliases: []model.Alias{{Alias: "b", Destination: "https://b.com", Enabled: model.BoolPtr(true)}}}); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findAlias(loaded.Aliases, "a"); ok {
		t.Error("old alias 'a' should be gone after overwrite")
	}
	if got, ok := findAlias(loaded.Aliases, "b"); !ok || got.Destination != "https://b.com" {
		t.Fatalf("alias b was not saved: %#v", loaded.Aliases)
	}
}

func findAlias(aliases []model.Alias, alias string) (model.Alias, bool) {
	for _, a := range aliases {
		if a.Alias == alias {
			return a, true
		}
	}
	return model.Alias{}, false
}
