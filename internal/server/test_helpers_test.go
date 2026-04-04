package server

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/jovalle/goku/internal/config"
	"github.com/jovalle/goku/internal/model"
	"github.com/jovalle/goku/internal/store"
)

func newTestServer(t *testing.T, cfg model.Config) *Server {
	t.Helper()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	s := store.New(cfg)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(s, logger, cfgPath, AuthConfig{})
}
