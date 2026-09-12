package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAPIKeyDoesNotLogConfiguredSecret(t *testing.T) {
	const secret = "configured-secret"
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))

	key, err := loadAPIKey(filepath.Join(t.TempDir(), "config.yaml"), secret, logger)
	if err != nil {
		t.Fatalf("loadAPIKey() error: %v", err)
	}
	if key != secret {
		t.Fatalf("key = %q, want configured key", key)
	}
	if strings.Contains(logs.String(), secret) {
		t.Fatalf("logs exposed API key: %s", logs.String())
	}
}

func TestLoadAPIKeyDoesNotLogFileSecret(t *testing.T) {
	const secret = "file-secret"
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".api_key"), []byte(secret+"\n"), 0600); err != nil {
		t.Fatalf("write API key: %v", err)
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))

	key, err := loadAPIKey(filepath.Join(dir, "config.yaml"), "", logger)
	if err != nil {
		t.Fatalf("loadAPIKey() error: %v", err)
	}
	if key != secret {
		t.Fatalf("key = %q, want file key", key)
	}
	if strings.Contains(logs.String(), secret) {
		t.Fatalf("logs exposed API key: %s", logs.String())
	}
}

func TestLoadAPIKeyCreatesPrivateKeyFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "config")
	configPath := filepath.Join(dir, "config.yaml")
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))

	key, err := loadAPIKey(configPath, "", logger)
	if err != nil {
		t.Fatalf("loadAPIKey() error: %v", err)
	}
	if len(key) != 48 {
		t.Fatalf("generated key length = %d, want 48", len(key))
	}
	keyPath := filepath.Join(dir, ".api_key")
	data, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("read generated key: %v", err)
	}
	if strings.TrimSpace(string(data)) != key {
		t.Fatal("generated key file does not contain returned key")
	}
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("stat generated key: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("generated key permissions = %o, want 600", info.Mode().Perm())
	}
	if strings.Contains(logs.String(), key) {
		t.Fatalf("logs exposed generated API key: %s", logs.String())
	}
	if !strings.Contains(logs.String(), keyPath) || !strings.Contains(logs.String(), "permissions=0600") {
		t.Fatalf("logs did not identify the private key file: %s", logs.String())
	}
}

func TestShutdownAllAttemptsEveryShutdown(t *testing.T) {
	firstErr := errors.New("first shutdown")
	secondErr := errors.New("second shutdown")
	called := make([]bool, 2)

	err := shutdownAll(t.Context(),
		func(context.Context) error {
			called[0] = true
			return firstErr
		},
		func(context.Context) error {
			called[1] = true
			return secondErr
		},
	)

	if !called[0] || !called[1] {
		t.Fatalf("called = %v, want both shutdowns attempted", called)
	}
	if !errors.Is(err, firstErr) || !errors.Is(err, secondErr) {
		t.Fatalf("shutdownAll() error = %v, want both errors joined", err)
	}
}
