package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/jovalle/goku/internal/config"
	"github.com/jovalle/goku/internal/model"
	"github.com/jovalle/goku/internal/store"
)

type testServers struct {
	admin  *Server
	public *Server
	store  *store.LinkStore
}

func newTestServers(t *testing.T, cfg model.Config, auth AuthConfig) testServers {
	t.Helper()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	s := store.New(cfg)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	return testServers{
		admin:  NewAdmin(s, logger, cfgPath, auth),
		public: NewPublic(s, logger),
		store:  s,
	}
}

func newAdminTestServer(t *testing.T, cfg model.Config) *Server {
	t.Helper()
	return newTestServers(t, cfg, AuthConfig{}).admin
}

func newPublicTestServer(t *testing.T, cfg model.Config) *Server {
	t.Helper()
	return newTestServers(t, cfg, AuthConfig{}).public
}

func newAuthServer(t *testing.T, cfg model.Config, auth AuthConfig) *Server {
	t.Helper()
	return newTestServers(t, cfg, auth).admin
}

func TestHandleHealth(t *testing.T) {
	srv := newAdminTestServer(t, model.Config{
		Aliases: []model.Alias{{Alias: "gh", Destination: "https://github.com"}},
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
	if resp.Aliases != 1 {
		t.Errorf("aliases = %d, want 1", resp.Aliases)
	}
}

func TestPublicLandingPage(t *testing.T) {
	srv := newPublicTestServer(t, model.Config{})
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Golinks manager") {
		t.Fatalf("expected public landing page containing golinks manager subtitle, body = %q", w.Body.String())
	}
}

func TestAdminLandingPage(t *testing.T) {
	srv := newAdminTestServer(t, model.Config{})
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Goku") {
		t.Fatalf("expected admin page containing 'Goku', body = %q", body)
	}
	if strings.Contains(body, "Log out") {
		t.Fatalf("admin page should not show logout without password auth, body = %q", body)
	}
	if strings.Contains(body, "Directory:") {
		t.Fatalf("admin page should not contain legacy directory subtitle, body = %q", body)
	}
	if !strings.Contains(body, "Total golinks:") {
		t.Fatalf("expected admin page containing bottom golink count, body = %q", body)
	}
	if got := strings.Count(strings.ToLower(body), "<!doctype html>"); got != 1 {
		t.Fatalf("expected a single html document, got %d doctypes", got)
	}
}

func TestAdminLoginPageShownWhenPasswordConfigured(t *testing.T) {
	srv := newAuthServer(t, model.Config{}, AuthConfig{Username: "admin", Password: "secret"})

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "id=\"password\"") {
		t.Fatalf("expected login page password field, body = %q", body)
	}
	if !strings.Contains(body, "Keep me logged in") {
		t.Fatalf("expected login page, body = %q", w.Body.String())
	}
}

func TestSwaggerPage(t *testing.T) {
	srv := newAdminTestServer(t, model.Config{})
	req := httptest.NewRequest("GET", "/swagger", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "goku swagger") {
		t.Fatalf("expected swagger page, body = %q", w.Body.String())
	}
}

func TestOpenAPIJSON(t *testing.T) {
	srv := newAdminTestServer(t, model.Config{})
	req := httptest.NewRequest("GET", "/openapi.json", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("content-type = %q, want application/json", ct)
	}
	if !strings.Contains(w.Body.String(), "\"/api/aliases\"") {
		t.Fatalf("expected alias endpoint in openapi document, body = %q", w.Body.String())
	}
}

func TestHandleRedirect_ExactAlias(t *testing.T) {
	srv := newPublicTestServer(t, model.Config{
		Aliases: []model.Alias{{Alias: "gh", Destination: "https://github.com"}},
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

func TestHandleRedirect_GreedyAlias(t *testing.T) {
	srv := newPublicTestServer(t, model.Config{
		Aliases: []model.Alias{{Alias: "r/{rest...}", Destination: "https://www.reddit.com/r/{rest...}"}},
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

func TestHandleRedirect_TemplateAlias(t *testing.T) {
	srv := newPublicTestServer(t, model.Config{
		Aliases: []model.Alias{{Alias: "gh/{owner}/{repo}", Destination: "https://github.com/{owner}/{repo}"}},
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
	srv := newPublicTestServer(t, model.Config{})
	req := httptest.NewRequest("GET", "/nosuch", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	if !strings.Contains(w.Body.String(), "/nosuch") {
		t.Fatalf("expected query-not-found page, body = %q", w.Body.String())
	}
}

func TestPublicServerDoesNotExposeAdminAPI(t *testing.T) {
	srv := newPublicTestServer(t, model.Config{})
	req := httptest.NewRequest("GET", "/api/aliases", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestHandleListAliases(t *testing.T) {
	srv := newAdminTestServer(t, model.Config{
		Aliases: []model.Alias{
			{Alias: "gh", Destination: "https://github.com"},
			{Alias: "g", Destination: "https://google.com"},
		},
	})
	req := httptest.NewRequest("GET", "/api/aliases", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var aliases []model.Alias
	if err := json.Unmarshal(w.Body.Bytes(), &aliases); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(aliases) != 2 {
		t.Fatalf("expected 2 aliases, got %d", len(aliases))
	}
	if aliases[0].Alias != "gh" {
		t.Errorf("Aliases[0].Alias = %q", aliases[0].Alias)
	}
}

func TestHandleAddAlias(t *testing.T) {
	srvs := newTestServers(t, model.Config{}, AuthConfig{})

	form := url.Values{"alias": {"docs"}, "destination": {"https://docs.example.com"}}
	req := httptest.NewRequest("POST", "/api/aliases", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srvs.admin.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", w.Code)
	}

	req2 := httptest.NewRequest("GET", "/docs", nil)
	w2 := httptest.NewRecorder()
	srvs.public.ServeHTTP(w2, req2)
	if w2.Code != http.StatusFound {
		t.Fatalf("redirect status = %d, want 302", w2.Code)
	}
}

func TestHandleAddAlias_MissingFields(t *testing.T) {
	srv := newAdminTestServer(t, model.Config{})
	tests := []struct {
		name string
		form url.Values
	}{
		{"missing destination", url.Values{"alias": {"foo"}}},
		{"missing alias", url.Values{"destination": {"https://x.com"}}},
		{"both empty", url.Values{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/api/aliases", strings.NewReader(tt.form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", w.Code)
			}
		})
	}
}

func TestHandleAddAlias_ValidationError(t *testing.T) {
	srv := newAdminTestServer(t, model.Config{})
	form := url.Values{
		"alias":       {"gh/{owner}"},
		"destination": {"https://github.com/{repo}"},
	}
	req := httptest.NewRequest("POST", "/api/aliases", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHandleDeleteAlias(t *testing.T) {
	srvs := newTestServers(t, model.Config{
		Aliases: []model.Alias{{Alias: "gh", Destination: "https://github.com"}},
	}, AuthConfig{})

	form := url.Values{"alias": {"gh"}}
	req := httptest.NewRequest("POST", "/api/aliases/delete", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srvs.admin.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", w.Code)
	}

	req2 := httptest.NewRequest("GET", "/gh", nil)
	w2 := httptest.NewRecorder()
	srvs.public.ServeHTTP(w2, req2)
	if w2.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", w2.Code)
	}
}

func TestHandleDeleteAlias_MissingField(t *testing.T) {
	srv := newAdminTestServer(t, model.Config{})
	req := httptest.NewRequest("POST", "/api/aliases/delete", strings.NewReader(url.Values{}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHandleEditAlias(t *testing.T) {
	srvs := newTestServers(t, model.Config{
		Aliases: []model.Alias{{Alias: "gh", Destination: "https://github.com"}},
	}, AuthConfig{})

	form := url.Values{
		"old_alias":   {"gh"},
		"alias":       {"docs"},
		"destination": {"https://docs.example.com"},
		"enabled":     {"1"},
	}
	req := httptest.NewRequest("POST", "/api/aliases/edit", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srvs.admin.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", w.Code)
	}

	wOld := httptest.NewRecorder()
	srvs.public.ServeHTTP(wOld, httptest.NewRequest("GET", "/gh", nil))
	if wOld.Code != http.StatusNotFound {
		t.Fatalf("old alias should not resolve, got %d", wOld.Code)
	}

	wNew := httptest.NewRecorder()
	srvs.public.ServeHTTP(wNew, httptest.NewRequest("GET", "/docs", nil))
	if wNew.Code != http.StatusFound {
		t.Fatalf("new alias status = %d, want 302", wNew.Code)
	}
	if loc := wNew.Header().Get("Location"); loc != "https://docs.example.com" {
		t.Fatalf("Location = %q, want https://docs.example.com", loc)
	}
}

func TestHandleToggleAlias(t *testing.T) {
	srvs := newTestServers(t, model.Config{
		Aliases: []model.Alias{{Alias: "gh", Destination: "https://github.com"}},
	}, AuthConfig{})

	disableForm := url.Values{"alias": {"gh"}, "enabled": {"0"}}
	disableReq := httptest.NewRequest("POST", "/api/aliases/toggle", strings.NewReader(disableForm.Encode()))
	disableReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	disableW := httptest.NewRecorder()
	srvs.admin.ServeHTTP(disableW, disableReq)
	if disableW.Code != http.StatusSeeOther {
		t.Fatalf("disable status = %d, want 303", disableW.Code)
	}

	wDisabled := httptest.NewRecorder()
	srvs.public.ServeHTTP(wDisabled, httptest.NewRequest("GET", "/gh", nil))
	if wDisabled.Code != http.StatusNotFound {
		t.Fatalf("disabled alias should return 404, got %d", wDisabled.Code)
	}

	enableForm := url.Values{"alias": {"gh"}, "enabled": {"1"}}
	enableReq := httptest.NewRequest("POST", "/api/aliases/toggle", strings.NewReader(enableForm.Encode()))
	enableReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	enableW := httptest.NewRecorder()
	srvs.admin.ServeHTTP(enableW, enableReq)
	if enableW.Code != http.StatusSeeOther {
		t.Fatalf("enable status = %d, want 303", enableW.Code)
	}

	wEnabled := httptest.NewRecorder()
	srvs.public.ServeHTTP(wEnabled, httptest.NewRequest("GET", "/gh", nil))
	if wEnabled.Code != http.StatusFound {
		t.Fatalf("enabled alias should return 302, got %d", wEnabled.Code)
	}
}

func TestHandleAliasPreview(t *testing.T) {
	srv := newAdminTestServer(t, model.Config{
		Aliases: []model.Alias{{Alias: "gh", Destination: "https://github.com"}},
	})

	req := httptest.NewRequest("GET", "/preview?path=gh", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Redirect Preview") {
		t.Fatalf("expected redirect preview page, body = %q", body)
	}
	if !strings.Contains(body, "https://github.com") {
		t.Fatalf("expected resolved destination in page, body = %q", body)
	}
}

func TestHandleAliasPreview_StripsPlaceholderValues(t *testing.T) {
	srv := newAdminTestServer(t, model.Config{
		Aliases: []model.Alias{{Alias: "yt/{}", Destination: "https://www.youtube.com/results?search_query={}"}},
	})

	req := httptest.NewRequest("GET", "/preview?alias=yt/%7B%7D", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, "search_query={}") {
		t.Fatalf("preview destination should not include literal placeholder value, body = %q", body)
	}
	if !strings.Contains(body, "search_query=") {
		t.Fatalf("expected placeholder to resolve to empty value in preview destination, body = %q", body)
	}
}

func TestHandleHealthWebSocket(t *testing.T) {
	srv := newPublicTestServer(t, model.Config{})
	httpSrv := httptest.NewServer(srv)
	defer httpSrv.Close()

	wsURL := "ws" + strings.TrimPrefix(httpSrv.URL, "http") + "/ws/health"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("websocket dial failed: %v", err)
	}
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var resp healthResponse
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("failed reading websocket health payload: %v", err)
	}
	if resp.Status != "ok" {
		t.Fatalf("status = %q, want ok", resp.Status)
	}
}

func TestHandleBatchImport_JSON(t *testing.T) {
	srvs := newTestServers(t, model.Config{
		Aliases: []model.Alias{{Alias: "gh", Destination: "https://github.com"}},
	}, AuthConfig{})

	body := `[{"alias":"docs","destination":"https://docs.example.com"},{"alias":"wiki/{topic}","destination":"https://en.wikipedia.org/wiki/{topic}"}]`
	req := httptest.NewRequest("POST", "/api/import", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srvs.admin.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", w.Code)
	}

	var resp batchImportResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp.ImportedAliases != 2 {
		t.Fatalf("imported_aliases = %d, want 2", resp.ImportedAliases)
	}
	if resp.TotalAliases != 3 {
		t.Fatalf("total_aliases = %d, want 3", resp.TotalAliases)
	}
}

func TestHandleBatchImport_Text(t *testing.T) {
	srvs := newTestServers(t, model.Config{}, AuthConfig{})

	body := "docs https://docs.example.com\nr/{rest...},https://reddit.com/r/{rest...}\n"
	req := httptest.NewRequest("POST", "/api/import", strings.NewReader(body))
	req.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	srvs.admin.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", w.Code)
	}

	if len(srvs.store.Aliases()) != 2 {
		t.Fatalf("expected 2 aliases, got %d", len(srvs.store.Aliases()))
	}
}

func TestHandleBatchImport_EmptyBody(t *testing.T) {
	srv := newAdminTestServer(t, model.Config{})
	req := httptest.NewRequest("POST", "/api/import", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHandleBrokenLinks(t *testing.T) {
	srvs := newTestServers(t, model.Config{}, AuthConfig{})

	req := httptest.NewRequest("GET", "/missing-link", nil)
	w := httptest.NewRecorder()
	srvs.public.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}

	req2 := httptest.NewRequest("GET", "/api/broken-links", nil)
	w2 := httptest.NewRecorder()
	srvs.admin.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w2.Code)
	}

	var resp brokenLinksResponse
	if err := json.Unmarshal(w2.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp.Count != 1 {
		t.Fatalf("count = %d, want 1", resp.Count)
	}
	if len(resp.Items) != 1 || resp.Items[0].Path != "missing-link" {
		t.Fatalf("broken items = %#v, want missing-link", resp.Items)
	}
}

func TestHealthz_PublicEvenWithAuth(t *testing.T) {
	srvs := newTestServers(t, model.Config{}, AuthConfig{
		Username: "admin",
		Password: "secret",
		APIKey:   "test-key",
	})

	for _, srv := range []*Server{srvs.admin, srvs.public} {
		req := httptest.NewRequest("GET", "/healthz", nil)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("healthz should be public, got %d", w.Code)
		}
	}
}

func TestRedirects_PublicEvenWithAuth(t *testing.T) {
	srvs := newTestServers(t, model.Config{
		Aliases: []model.Alias{{Alias: "gh", Destination: "https://github.com"}},
	}, AuthConfig{Username: "admin", Password: "secret", APIKey: "test-key"})

	req := httptest.NewRequest("GET", "/gh", nil)
	w := httptest.NewRecorder()
	srvs.public.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("redirect should be public, got %d", w.Code)
	}
}

func TestAdminServerDoesNotExposePublicRedirects(t *testing.T) {
	srv := newAdminTestServer(t, model.Config{
		Aliases: []model.Alias{{Alias: "gh", Destination: "https://github.com"}},
	})

	req := httptest.NewRequest("GET", "/gh", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestAPI_RequiresAuth(t *testing.T) {
	srv := newAuthServer(t, model.Config{
		Aliases: []model.Alias{{Alias: "gh", Destination: "https://github.com"}},
	}, AuthConfig{Username: "admin", Password: "secret", APIKey: "test-key"})

	for _, path := range []string{"/api/aliases", "/api/broken-links"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest("GET", path, nil)
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, req)
			if w.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", w.Code)
			}
		})
	}
}

func TestMetrics_PublicEvenWithAuth(t *testing.T) {
	srv := newAuthServer(t, model.Config{}, AuthConfig{
		Username: "admin",
		Password: "secret",
		APIKey:   "test-key",
	})

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

func TestAPI_WithBearerToken(t *testing.T) {
	srv := newAuthServer(t, model.Config{
		Aliases: []model.Alias{{Alias: "gh", Destination: "https://github.com"}},
	}, AuthConfig{Password: "secret", APIKey: "test-key"})

	req := httptest.NewRequest("GET", "/api/aliases", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestAdminUI_RequiresBasicAuthWhenPasswordConfigured(t *testing.T) {
	srv := newAuthServer(t, model.Config{}, AuthConfig{Username: "admin", Password: "secret"})

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "id=\"password\"") {
		t.Fatalf("expected login page, body = %q", w.Body.String())
	}
}

func TestSwagger_RequiresBasicAuthWhenPasswordConfigured(t *testing.T) {
	srv := newAuthServer(t, model.Config{}, AuthConfig{Username: "admin", Password: "secret"})

	req := httptest.NewRequest("GET", "/swagger", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "id=\"password\"") {
		t.Fatalf("expected login page, body = %q", w.Body.String())
	}
}

func TestUI_PublicWhenOnlyAPIKeyConfigured(t *testing.T) {
	srv := newAuthServer(t, model.Config{
		Aliases: []model.Alias{{Alias: "gh", Destination: "https://github.com"}},
	}, AuthConfig{APIKey: "test-key"})

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code == http.StatusUnauthorized {
		t.Fatalf("status = %d, want UI to stay public without admin password", w.Code)
	}

	var session *http.Cookie
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == adminSessionCookieName {
			session = cookie
			break
		}
	}
	if session == nil {
		t.Fatal("expected admin session cookie when API key is configured")
	}
}

func TestLogin_WithPasswordSetsSessionCookie(t *testing.T) {
	srv := newAuthServer(t, model.Config{}, AuthConfig{Username: "admin", Password: "secret"})

	form := url.Values{"password": {"secret"}, "remember": {"on"}}
	req := httptest.NewRequest("POST", "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", w.Code)
	}
	if w.Header().Get("Location") != "/" {
		t.Fatalf("location = %q, want /", w.Header().Get("Location"))
	}

	var session *http.Cookie
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == adminSessionCookieName {
			session = cookie
			break
		}
	}
	if session == nil {
		t.Fatal("expected admin session cookie")
	}
	if session.MaxAge <= 0 {
		t.Fatalf("MaxAge = %d, want persistent cookie", session.MaxAge)
	}
}

func TestLogin_WrongPasswordRendersValidationState(t *testing.T) {
	srv := newAuthServer(t, model.Config{}, AuthConfig{Username: "admin", Password: "secret"})

	form := url.Values{"password": {"wrong"}}
	req := httptest.NewRequest("POST", "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "Wrong password") {
		t.Fatalf("expected wrong password message, body = %q", body)
	}
	if !strings.Contains(body, "Keep me logged in") {
		t.Fatalf("expected keep me logged in option, body = %q", body)
	}
	if !strings.Contains(body, "data-state=\"invalid\"") {
		t.Fatalf("expected invalid password state, body = %q", body)
	}
}

func TestAdminUI_WithSessionCookie(t *testing.T) {
	srv := newAuthServer(t, model.Config{}, AuthConfig{Username: "admin", Password: "secret"})

	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: adminSessionCookieName, Value: srv.adminSessionToken()})
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Goku") {
		t.Fatalf("expected admin UI, body = %q", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Log out") {
		t.Fatalf("expected logout button when password auth is enabled, body = %q", w.Body.String())
	}
}

func TestAPI_WithSessionCookie(t *testing.T) {
	srv := newAuthServer(t, model.Config{
		Aliases: []model.Alias{{Alias: "gh", Destination: "https://github.com"}},
	}, AuthConfig{Username: "admin", Password: "secret", APIKey: "test-key"})

	req := httptest.NewRequest("GET", "/api/aliases", nil)
	req.AddCookie(&http.Cookie{Name: adminSessionCookieName, Value: srv.adminSessionToken()})
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

func TestLogout_ClearsSessionCookie(t *testing.T) {
	srv := newAuthServer(t, model.Config{}, AuthConfig{Username: "admin", Password: "secret"})

	req := httptest.NewRequest("POST", "/logout", nil)
	req.AddCookie(&http.Cookie{Name: adminSessionCookieName, Value: srv.adminSessionToken()})
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", w.Code)
	}
	if w.Header().Get("Location") != "/login" {
		t.Fatalf("location = %q, want /login", w.Header().Get("Location"))
	}

	var cleared *http.Cookie
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == adminSessionCookieName {
			cleared = cookie
			break
		}
	}
	if cleared == nil {
		t.Fatal("expected cleared session cookie")
	}
	if cleared.MaxAge != -1 {
		t.Fatalf("MaxAge = %d, want -1", cleared.MaxAge)
	}
}

func TestAPI_RequiresBearerWhenOnlyAPIKeyConfigured(t *testing.T) {
	srv := newAuthServer(t, model.Config{
		Aliases: []model.Alias{{Alias: "gh", Destination: "https://github.com"}},
	}, AuthConfig{APIKey: "test-key"})

	req := httptest.NewRequest("GET", "/api/aliases", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 when only API key is configured", w.Code)
	}
}

func TestMetrics_PublicWhenOnlyAPIKeyConfigured(t *testing.T) {
	srv := newAuthServer(t, model.Config{}, AuthConfig{APIKey: "test-key"})

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

func TestAPI_WithBearerTokenWhenOnlyAPIKeyConfigured(t *testing.T) {
	srv := newAuthServer(t, model.Config{
		Aliases: []model.Alias{{Alias: "gh", Destination: "https://github.com"}},
	}, AuthConfig{APIKey: "test-key"})

	req := httptest.NewRequest("GET", "/api/aliases", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestToggleAlias_WithSessionCookieWhenOnlyAPIKeyConfigured(t *testing.T) {
	srvs := newTestServers(t, model.Config{
		Aliases: []model.Alias{{Alias: "gh", Destination: "https://github.com"}},
	}, AuthConfig{APIKey: "test-key"})

	homeReq := httptest.NewRequest("GET", "/", nil)
	homeW := httptest.NewRecorder()
	srvs.admin.ServeHTTP(homeW, homeReq)

	var session *http.Cookie
	for _, cookie := range homeW.Result().Cookies() {
		if cookie.Name == adminSessionCookieName {
			session = cookie
			break
		}
	}
	if session == nil {
		t.Fatal("expected admin session cookie from admin home")
	}

	form := url.Values{"alias": {"gh"}, "enabled": {"0"}}
	toggleReq := httptest.NewRequest("POST", "/api/aliases/toggle", strings.NewReader(form.Encode()))
	toggleReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	toggleReq.AddCookie(session)
	toggleW := httptest.NewRecorder()
	srvs.admin.ServeHTTP(toggleW, toggleReq)

	if toggleW.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", toggleW.Code)
	}

	publicReq := httptest.NewRequest("GET", "/gh", nil)
	publicW := httptest.NewRecorder()
	srvs.public.ServeHTTP(publicW, publicReq)
	if publicW.Code != http.StatusNotFound {
		t.Fatalf("disabled alias should return 404, got %d", publicW.Code)
	}
}
