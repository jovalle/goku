package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jovalle/goku/internal/model"
	"github.com/jovalle/goku/internal/store"
)

func newTestServer(t *testing.T, cfg model.Config) *Server {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	os.WriteFile(cfgPath, []byte("links: {}"), 0644)
	s := store.New(cfg)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(s, logger, cfgPath)
}

// Health

func TestHandleHealth(t *testing.T) {
	srv := newTestServer(t, model.Config{
		Links: map[string]string{"gh": "https://github.com"},
	})
	req := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp healthResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp.Status != "ok" {
		t.Errorf("status = %q, want %q", resp.Status, "ok")
	}
	if resp.Links != 1 {
		t.Errorf("links = %d, want 1", resp.Links)
	}
}

// Redirects

func TestHandleRedirect_ExactLink(t *testing.T) {
	srv := newTestServer(t, model.Config{
		Links: map[string]string{"gh": "https://github.com"},
	})
	req := httptest.NewRequest("GET", "/gh", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "https://github.com" {
		t.Errorf("Location = %q", loc)
	}
}

func TestHandleRedirect_PrefixRule(t *testing.T) {
	srv := newTestServer(t, model.Config{
		Rules: []model.Rule{
			{Name: "reddit", Type: "prefix", Pattern: "r", Redirect: "https://www.reddit.com/r"},
		},
	})
	req := httptest.NewRequest("GET", "/r/golang", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "https://www.reddit.com/r/golang" {
		t.Errorf("Location = %q", loc)
	}
}

func TestHandleRedirect_TemplateRule(t *testing.T) {
	srv := newTestServer(t, model.Config{
		Rules: []model.Rule{
			{Name: "gh", Type: "template", Pattern: "gh/{owner}/{name}", Redirect: "https://github.com/{owner}/{name}"},
		},
	})
	req := httptest.NewRequest("GET", "/gh/jovalle/goku", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "https://github.com/jovalle/goku" {
		t.Errorf("Location = %q", loc)
	}
}

func TestHandleRedirect_NotFound(t *testing.T) {
	srv := newTestServer(t, model.Config{})
	req := httptest.NewRequest("GET", "/nosuch", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

// API: List Links

func TestHandleListLinks(t *testing.T) {
	srv := newTestServer(t, model.Config{
		Links: map[string]string{"gh": "https://github.com", "g": "https://google.com"},
	})
	req := httptest.NewRequest("GET", "/api/links", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var links map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &links); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if links["gh"] != "https://github.com" {
		t.Errorf("links[gh] = %q", links["gh"])
	}
}

// API: Add Link

func TestHandleAddLink(t *testing.T) {
	srv := newTestServer(t, model.Config{})

	form := url.Values{"name": {"docs"}, "url": {"https://docs.example.com"}}
	req := httptest.NewRequest("POST", "/api/links", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", w.Code)
	}

	req2 := httptest.NewRequest("GET", "/docs", nil)
	w2 := httptest.NewRecorder()
	srv.ServeHTTP(w2, req2)
	if w2.Code != http.StatusFound {
		t.Fatalf("redirect status = %d, want 302", w2.Code)
	}
}

func TestHandleAddLink_MissingFields(t *testing.T) {
	srv := newTestServer(t, model.Config{})
	tests := []struct {
		name string
		form url.Values
	}{
		{"missing url", url.Values{"name": {"foo"}}},
		{"missing name", url.Values{"url": {"https://x.com"}}},
		{"both empty", url.Values{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/api/links", strings.NewReader(tt.form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", w.Code)
			}
		})
	}
}

// API: Delete Link

func TestHandleDeleteLink(t *testing.T) {
	srv := newTestServer(t, model.Config{
		Links: map[string]string{"gh": "https://github.com"},
	})
	req := httptest.NewRequest("POST", "/api/links/gh/delete", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", w.Code)
	}

	req2 := httptest.NewRequest("GET", "/gh", nil)
	w2 := httptest.NewRecorder()
	srv.ServeHTTP(w2, req2)
	if w2.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", w2.Code)
	}
}

// API: Add Rule

func TestHandleAddRule(t *testing.T) {
	srv := newTestServer(t, model.Config{})

	form := url.Values{
		"name":     {"wiki"},
		"type":     {"template"},
		"pattern":  {"wiki/{topic}"},
		"redirect": {"https://en.wikipedia.org/wiki/{topic}"},
	}
	req := httptest.NewRequest("POST", "/api/rules", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", w.Code)
	}
}

func TestHandleAddRule_MissingFields(t *testing.T) {
	srv := newTestServer(t, model.Config{})

	form := url.Values{"name": {"x"}}
	req := httptest.NewRequest("POST", "/api/rules", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandleAddRule_InvalidType(t *testing.T) {
	srv := newTestServer(t, model.Config{})

	form := url.Values{"name": {"x"}, "type": {"invalid"}, "pattern": {"x"}, "redirect": {"https://x.com"}}
	req := httptest.NewRequest("POST", "/api/rules", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

// API: Delete Rule

func TestHandleDeleteRule(t *testing.T) {
	srv := newTestServer(t, model.Config{
		Rules: []model.Rule{
			{Name: "reddit", Type: "prefix", Pattern: "r", Redirect: "https://www.reddit.com/r"},
		},
	})
	req := httptest.NewRequest("POST", "/api/rules/reddit/delete", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", w.Code)
	}
}
