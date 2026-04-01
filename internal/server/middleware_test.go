package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jovalle/goku/internal/model"
)

// ── LoggingMiddleware ───────────────────────────────────────

func TestLoggingMiddleware(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := LoggingMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

// ── RecoveryMiddleware ──────────────────────────────────────

func TestRecoveryMiddleware_NoPanic(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := RecoveryMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/ok", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestRecoveryMiddleware_CatchesPanic(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := RecoveryMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))
	req := httptest.NewRequest("GET", "/panic", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
}

// ── RequestIDMiddleware ──────────────────────────────────────

func TestRequestIDMiddleware_GeneratesID(t *testing.T) {
	handler := RequestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	id := w.Header().Get("X-Request-ID")
	if id == "" {
		t.Error("expected X-Request-ID header")
	}
	if len(id) != 16 {
		t.Errorf("X-Request-ID length = %d, want 16", len(id))
	}
}

func TestRequestIDMiddleware_PassthroughExisting(t *testing.T) {
	handler := RequestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Request-ID", "my-custom-id")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if got := w.Header().Get("X-Request-ID"); got != "my-custom-id" {
		t.Errorf("X-Request-ID = %q, want %q", got, "my-custom-id")
	}
}

// ── MetricsMiddleware ────────────────────────────────────────

func TestMetricsMiddleware(t *testing.T) {
	handler := MetricsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

// ── statusRecorder ───────────────────────────────────────────

func TestStatusRecorder_DefaultStatus(t *testing.T) {
	w := httptest.NewRecorder()
	rec := &statusRecorder{ResponseWriter: w, status: 200}
	rec.Write([]byte("ok"))
	if rec.status != 200 {
		t.Errorf("default status = %d, want 200", rec.status)
	}
}

func TestStatusRecorder_ExplicitStatus(t *testing.T) {
	w := httptest.NewRecorder()
	rec := &statusRecorder{ResponseWriter: w, status: 200}
	rec.WriteHeader(http.StatusNotFound)
	if rec.status != 404 {
		t.Errorf("status = %d, want 404", rec.status)
	}
}

// ── checkAuth ────────────────────────────────────────────────

func TestCheckAuth_NoAuthConfigured(t *testing.T) {
	srv := newTestServer(t, model.Config{})
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	if !srv.checkAuth(w, req) {
		t.Error("checkAuth should return true when no auth is configured")
	}
}

func TestCheckAuth_BearerToken_Valid(t *testing.T) {
	srv := newAuthServer(t, model.Config{}, AuthConfig{Password: "secret", APIKey: "test-key-123"})
	req := httptest.NewRequest("GET", "/api/links", nil)
	req.Header.Set("Authorization", "Bearer test-key-123")
	w := httptest.NewRecorder()
	if !srv.checkAuth(w, req) {
		t.Error("checkAuth should accept valid bearer token")
	}
}

func TestCheckAuth_BearerToken_Invalid(t *testing.T) {
	srv := newAuthServer(t, model.Config{}, AuthConfig{Password: "secret", APIKey: "test-key-123"})
	req := httptest.NewRequest("GET", "/api/links", nil)
	req.Header.Set("Authorization", "Bearer wrong-key")
	w := httptest.NewRecorder()
	if srv.checkAuth(w, req) {
		t.Error("checkAuth should reject invalid bearer token")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestCheckAuth_BasicAuth_Valid(t *testing.T) {
	srv := newAuthServer(t, model.Config{}, AuthConfig{Username: "admin", Password: "secret"})
	req := httptest.NewRequest("GET", "/", nil)
	req.SetBasicAuth("admin", "secret")
	w := httptest.NewRecorder()
	if !srv.checkAuth(w, req) {
		t.Error("checkAuth should accept valid basic auth")
	}
}

func TestCheckAuth_BasicAuth_WrongPassword(t *testing.T) {
	srv := newAuthServer(t, model.Config{}, AuthConfig{Username: "admin", Password: "secret"})
	req := httptest.NewRequest("GET", "/", nil)
	req.SetBasicAuth("admin", "wrong")
	w := httptest.NewRecorder()
	if srv.checkAuth(w, req) {
		t.Error("checkAuth should reject wrong password")
	}
}

func TestCheckAuth_BasicAuth_WrongUsername(t *testing.T) {
	srv := newAuthServer(t, model.Config{}, AuthConfig{Username: "admin", Password: "secret"})
	req := httptest.NewRequest("GET", "/", nil)
	req.SetBasicAuth("hacker", "secret")
	w := httptest.NewRecorder()
	if srv.checkAuth(w, req) {
		t.Error("checkAuth should reject wrong username")
	}
}

func TestCheckAuth_NoCreds(t *testing.T) {
	srv := newAuthServer(t, model.Config{}, AuthConfig{Username: "admin", Password: "secret"})
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	if srv.checkAuth(w, req) {
		t.Error("checkAuth should reject missing credentials")
	}
	if w.Header().Get("WWW-Authenticate") == "" {
		t.Error("expected WWW-Authenticate header")
	}
}

func TestCheckAuth_APIKeyAloneDoesNotEnableAuth(t *testing.T) {
	srv := newAuthServer(t, model.Config{}, AuthConfig{APIKey: "test-key-123"})
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	if !srv.checkAuth(w, req) {
		t.Error("checkAuth should return true when only an API key is configured")
	}
	if w.Header().Get("WWW-Authenticate") != "" {
		t.Error("did not expect WWW-Authenticate header when auth is disabled")
	}
}

// ── Chain ────────────────────────────────────────────────────

func TestChain(t *testing.T) {
	var order []string
	mw1 := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "mw1-before")
			next.ServeHTTP(w, r)
			order = append(order, "mw1-after")
		})
	}
	mw2 := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "mw2-before")
			next.ServeHTTP(w, r)
			order = append(order, "mw2-after")
		})
	}
	base := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		order = append(order, "handler")
	})

	h := chain(base, mw1, mw2)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))

	expected := []string{"mw1-before", "mw2-before", "handler", "mw2-after", "mw1-after"}
	if len(order) != len(expected) {
		t.Fatalf("order = %v, want %v", order, expected)
	}
	for i, v := range expected {
		if order[i] != v {
			t.Errorf("order[%d] = %q, want %q", i, order[i], v)
		}
	}
}

// ── Full request lifecycle ───────────────────────────────────

func TestFullRequestLifecycle(t *testing.T) {
	srv := newTestServer(t, model.Config{
		Links: map[string]string{"gh": "https://github.com"},
	})
	req := httptest.NewRequest("GET", "/gh", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", w.Code)
	}
	if w.Header().Get("X-Request-ID") == "" {
		t.Error("expected X-Request-ID from middleware")
	}
}
