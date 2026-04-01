package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jovalle/goku/internal/model"
)

func TestLoad_Valid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	data := "links:\n  gh: https://github.com\n  g: https://google.com\nrules:\n  - name: reddit\n    type: prefix\n    pattern: r\n    redirect: https://www.reddit.com/r\n"
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Links["gh"] != "https://github.com" {
		t.Errorf("Links[gh] = %q", cfg.Links["gh"])
	}
	if cfg.Links["g"] != "https://google.com" {
		t.Errorf("Links[g] = %q", cfg.Links["g"])
	}
	if len(cfg.Rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(cfg.Rules))
	}
	if cfg.Rules[0].Name != "reddit" {
		t.Errorf("Rules[0].Name = %q", cfg.Rules[0].Name)
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
	if cfg.Links == nil {
		t.Error("Links should default to a non-nil map for missing files")
	}
	if cfg.Rules == nil {
		t.Error("Rules should default to a non-nil slice for missing files")
	}
	if len(cfg.Links) != 0 {
		t.Errorf("expected no links for missing file, got %d", len(cfg.Links))
	}
	if len(cfg.Rules) != 0 {
		t.Errorf("expected no rules for missing file, got %d", len(cfg.Rules))
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
	if err := os.WriteFile(path, []byte("links:\n  x: https://x.com\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Rules == nil {
		t.Error("Rules should default to non-nil slice when missing from YAML")
	}
}

func TestSave_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	original := model.Config{
		Links: map[string]string{"gh": "https://github.com", "g": "https://google.com"},
		Rules: []model.Rule{{Name: "reddit", Type: "prefix", Pattern: "r", Redirect: "https://www.reddit.com/r"}},
	}
	if err := Save(path, original); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() after Save error: %v", err)
	}
	if loaded.Links["gh"] != original.Links["gh"] {
		t.Errorf("Links[gh] = %q, want %q", loaded.Links["gh"], original.Links["gh"])
	}
	if len(loaded.Rules) != 1 || loaded.Rules[0].Name != "reddit" {
		t.Errorf("rules mismatch after round-trip")
	}
}

func TestSave_AtomicNoPreviousFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new.yaml")
	cfg := model.Config{Links: map[string]string{"x": "https://x.com"}}
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
	if err := Save(path, model.Config{Links: map[string]string{"a": "https://a.com"}}); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, model.Config{Links: map[string]string{"b": "https://b.com"}}); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.Links["a"]; ok {
		t.Error("old link 'a' should be gone after overwrite")
	}
	if loaded.Links["b"] != "https://b.com" {
		t.Errorf("Links[b] = %q", loaded.Links["b"])
	}
}
