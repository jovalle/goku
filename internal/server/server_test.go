package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jovalle/goku/internal/config"
	"github.com/jovalle/goku/internal/model"
	"github.com/jovalle/goku/internal/store"
)

func TestConfigReload_Integration(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	initial := model.Config{Aliases: []model.Alias{{Alias: "gh", Destination: "https://github.com", Enabled: model.BoolPtr(true)}}}
	if err := config.Save(cfgPath, initial); err != nil {
		t.Fatal(err)
	}

	s := store.New(initial)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := New(s, logger, cfgPath, AuthConfig{})

	// Verify initial redirect
	req := httptest.NewRequest("GET", "/gh", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusFound {
		t.Fatalf("initial: status = %d, want 302", w.Code)
	}

	// Start watcher
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go config.Watch(ctx, cfgPath, s, logger)
	time.Sleep(50 * time.Millisecond)

	// Update config
	updated := model.Config{
		Aliases: []model.Alias{
			{Alias: "gh", Destination: "https://github.com", Enabled: model.BoolPtr(true)},
			{Alias: "g", Destination: "https://google.com", Enabled: model.BoolPtr(true)},
		},
	}
	if err := config.Save(cfgPath, updated); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)

	// Verify new link
	req2 := httptest.NewRequest("GET", "/g", nil)
	w2 := httptest.NewRecorder()
	srv.ServeHTTP(w2, req2)
	if w2.Code != http.StatusFound {
		t.Fatalf("after reload: status = %d, want 302", w2.Code)
	}
	if loc := w2.Header().Get("Location"); loc != "https://google.com" {
		t.Errorf("Location = %q, want %q", loc, "https://google.com")
	}
}

func TestE2E_AddLinkThenRedirect(t *testing.T) {
	srv := newTestServer(t, model.Config{})

	// 404 before add
	req := httptest.NewRequest("GET", "/docs", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 before add, got %d", w.Code)
	}

	// Add link
	form := url.Values{"name": {"docs"}, "url": {"https://docs.example.com"}}
	addReq := httptest.NewRequest("POST", "/api/aliases", strings.NewReader(form.Encode()))
	addReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	addW := httptest.NewRecorder()
	srv.ServeHTTP(addW, addReq)
	if addW.Code != http.StatusSeeOther {
		t.Fatalf("add status = %d, want 303", addW.Code)
	}

	// Redirect works now
	req2 := httptest.NewRequest("GET", "/docs", nil)
	w2 := httptest.NewRecorder()
	srv.ServeHTTP(w2, req2)
	if w2.Code != http.StatusFound {
		t.Fatalf("redirect status = %d, want 302", w2.Code)
	}
	if loc := w2.Header().Get("Location"); loc != "https://docs.example.com" {
		t.Errorf("Location = %q", loc)
	}
}

func TestE2E_AddThenDeleteLink(t *testing.T) {
	srv := newTestServer(t, model.Config{
		Aliases: []model.Alias{{Alias: "gh", Destination: "https://github.com", Enabled: model.BoolPtr(true)}},
	})

	// Delete
	delReq := httptest.NewRequest("POST", "/api/aliases/gh/delete", nil)
	delW := httptest.NewRecorder()
	srv.ServeHTTP(delW, delReq)
	if delW.Code != http.StatusSeeOther {
		t.Fatalf("delete status = %d, want 303", delW.Code)
	}

	// 404
	req := httptest.NewRequest("GET", "/gh", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}
