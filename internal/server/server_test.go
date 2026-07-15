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

type watchReadyHandler struct {
	slog.Handler
	ready chan<- struct{}
}

func (h watchReadyHandler) Handle(ctx context.Context, record slog.Record) error {
	if record.Message == "watching config" {
		select {
		case h.ready <- struct{}{}:
		default:
		}
	}
	return h.Handler.Handle(ctx, record)
}

func TestConfigReload_Integration(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	initial := model.Config{Aliases: []model.Alias{{Alias: "gh", Destination: "https://github.com", Enabled: model.BoolPtr(true)}}}
	if err := config.Save(cfgPath, initial); err != nil {
		t.Fatal(err)
	}

	s := store.New(initial)
	watchReady := make(chan struct{}, 1)
	logger := slog.New(watchReadyHandler{
		Handler: slog.NewTextHandler(io.Discard, nil),
		ready:   watchReady,
	})
	srv := New(s, logger, cfgPath, AuthConfig{})

	req := httptest.NewRequest("GET", "/gh", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusFound {
		t.Fatalf("initial: status = %d, want 302", w.Code)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watchErr := make(chan error, 1)
	go func() {
		watchErr <- config.Watch(ctx, cfgPath, s, logger)
	}()
	select {
	case <-watchReady:
	case err := <-watchErr:
		if err != nil {
			t.Fatalf("starting config watcher: %v", err)
		}
		t.Fatal("config watcher stopped")
	case <-time.After(time.Second):
		t.Fatal("config watcher did not start")
	}

	updated := model.Config{
		Aliases: []model.Alias{
			{Alias: "gh", Destination: "https://github.com", Enabled: model.BoolPtr(true)},
			{Alias: "g", Destination: "https://google.com", Enabled: model.BoolPtr(true)},
		},
	}
	if err := config.Save(cfgPath, updated); err != nil {
		t.Fatal(err)
	}

	retry := time.NewTicker(10 * time.Millisecond)
	defer retry.Stop()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for {
		req := httptest.NewRequest("GET", "/g", nil)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		if w.Code == http.StatusFound && w.Header().Get("Location") == "https://google.com" {
			break
		}

		select {
		case err := <-watchErr:
			if err != nil {
				t.Fatalf("watching config: %v", err)
			}
			t.Fatal("config watcher stopped")
		case <-retry.C:
		case <-deadline.C:
			t.Fatalf("redirect was not reloaded; status = %d, location = %q", w.Code, w.Header().Get("Location"))
		}
	}
}

func TestE2E_AddLinkThenRedirect(t *testing.T) {
	srv := newTestServer(t, model.Config{})

	req := httptest.NewRequest("GET", "/docs", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 before add, got %d", w.Code)
	}

	form := url.Values{"name": {"docs"}, "url": {"https://docs.example.com"}}
	addReq := httptest.NewRequest("POST", "/api/aliases", strings.NewReader(form.Encode()))
	addReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	addW := httptest.NewRecorder()
	srv.ServeHTTP(addW, addReq)
	if addW.Code != http.StatusSeeOther {
		t.Fatalf("add status = %d, want 303", addW.Code)
	}

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

	delReq := httptest.NewRequest("POST", "/api/aliases/gh/delete", nil)
	delW := httptest.NewRecorder()
	srv.ServeHTTP(delW, delReq)
	if delW.Code != http.StatusSeeOther {
		t.Fatalf("delete status = %d, want 303", delW.Code)
	}

	req := httptest.NewRequest("GET", "/gh", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}
