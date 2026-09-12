package config

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/jovalle/goku/internal/model"
)

type recordingUpdater struct {
	configs []model.Config
}

func (u *recordingUpdater) Update(cfg model.Config) {
	u.configs = append(u.configs, cfg)
}

func TestWatchReturnsWhenContextIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := Watch(ctx, filepath.Join(t.TempDir(), "config.yaml"), &recordingUpdater{}, testLogger())
	if err != nil {
		t.Fatalf("Watch() error: %v", err)
	}
}

func TestReloadPublishesValidConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("aliases:\n  - alias: docs\n    destination: https://docs.example.com\n"), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	updater := &recordingUpdater{}

	reload(path, updater, testLogger())

	if len(updater.configs) != 1 {
		t.Fatalf("updates = %d, want 1", len(updater.configs))
	}
	aliases := updater.configs[0].Aliases
	if len(aliases) != 1 || aliases[0].Alias != "docs" || aliases[0].Destination != "https://docs.example.com" {
		t.Fatalf("reloaded aliases = %#v", aliases)
	}
}

func TestReloadKeepsCurrentConfigWhenFileIsInvalid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("{{invalid yaml}}"), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	updater := &recordingUpdater{}

	reload(path, updater, testLogger())

	if len(updater.configs) != 0 {
		t.Fatalf("updates = %d, want 0", len(updater.configs))
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
