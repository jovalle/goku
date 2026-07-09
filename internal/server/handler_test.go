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
	store  *store.AliasStore
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
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

func newOnlineAliasStatusChecker() *aliasStatusChecker {
	return &aliasStatusChecker{
		client: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader("")),
					Request:    req,
				}, nil
			}),
		},
		timeout: time.Second,
	}
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
	if resp.ActiveAliases != 1 {
		t.Errorf("active_aliases = %d, want 1", resp.ActiveAliases)
	}
}

func TestHandleAliasStatuses(t *testing.T) {
	destinationServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.WriteHeader(http.StatusNoContent)
		case "/redirect":
			http.Redirect(w, r, "http://127.0.0.1:1/unreachable-after-redirect", http.StatusFound)
		case "/missing", "/missing/":
			http.NotFound(w, r)
		default:
			http.Error(w, "upstream error", http.StatusInternalServerError)
		}
	}))
	defer destinationServer.Close()

	srv := newAdminTestServer(t, model.Config{
		Aliases: []model.Alias{
			{Alias: "ok", Destination: destinationServer.URL + "/ok"},
			{Alias: "redirect", Destination: destinationServer.URL + "/redirect"},
			{Alias: "missing", Destination: destinationServer.URL + "/missing"},
			{Alias: "error", Destination: destinationServer.URL + "/error"},
			{Alias: "sub/{subreddit}", Destination: destinationServer.URL + "/missing/{subreddit}"},
		},
	})
	req := httptest.NewRequest("GET", "/api/aliases/status", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}

	var resp aliasStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp.PollAfterSeconds <= 0 {
		t.Fatalf("poll_after_seconds = %d, want positive", resp.PollAfterSeconds)
	}

	states := map[string]string{}
	details := map[string]string{}
	for _, item := range resp.Items {
		states[item.Alias] = item.State
		details[item.Alias] = item.Detail
	}
	if states["ok"] != "online" {
		t.Errorf("ok state = %q, want online", states["ok"])
	}
	if states["redirect"] != "online" {
		t.Errorf("redirect state = %q, want online", states["redirect"])
	}
	if states["missing"] != "offline" {
		t.Errorf("missing state = %q, want offline", states["missing"])
	}
	if states["error"] != "warning" {
		t.Errorf("error state = %q, want warning", states["error"])
	}
	if states["sub/{subreddit}"] != "warning" {
		t.Errorf("templated missing state = %q, want warning", states["sub/{subreddit}"])
	}
	if !strings.Contains(details["sub/{subreddit}"], "template destination") {
		t.Errorf("templated missing detail = %q, want template destination detail", details["sub/{subreddit}"])
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
	body := w.Body.String()
	if !strings.Contains(body, "<h1 class=\"h4 mb-0\">Goku</h1>") || !strings.Contains(body, "golinks defined") {
		t.Fatalf("expected public landing page containing brand and golink count, body = %q", body)
	}
}

func TestLogoServedFromStaticRoute(t *testing.T) {
	srvs := newTestServers(t, model.Config{}, AuthConfig{})
	for name, srv := range map[string]*Server{
		"admin":  srvs.admin,
		"public": srvs.public,
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/static/logo.png", nil)
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", w.Code)
			}
			if ct := w.Header().Get("Content-Type"); ct != "image/png" {
				t.Fatalf("content-type = %q, want image/png", ct)
			}
			if w.Body.Len() == 0 {
				t.Fatal("expected logo bytes in response body")
			}
		})
	}
}

func TestFaviconServedFromSharedRoutes(t *testing.T) {
	srvs := newTestServers(t, model.Config{}, AuthConfig{})
	for name, srv := range map[string]*Server{
		"admin":  srvs.admin,
		"public": srvs.public,
	} {
		for _, path := range []string{"/favicon.ico", "/static/favicon.png"} {
			t.Run(name+path, func(t *testing.T) {
				req := httptest.NewRequest("GET", path, nil)
				w := httptest.NewRecorder()
				srv.ServeHTTP(w, req)

				if w.Code != http.StatusOK {
					t.Fatalf("status = %d, want 200", w.Code)
				}
				if ct := w.Header().Get("Content-Type"); ct != "image/png" {
					t.Fatalf("content-type = %q, want image/png", ct)
				}
				if w.Body.Len() == 0 {
					t.Fatal("expected favicon bytes in response body")
				}
			})
		}
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
	if strings.Contains(body, "Total aliases:") || strings.Contains(body, "Total golinks:") {
		t.Fatalf("admin page should not show alias count footer, body = %q", body)
	}
	if got := strings.Count(strings.ToLower(body), "<!doctype html>"); got != 1 {
		t.Fatalf("expected a single html document, got %d doctypes", got)
	}
}

func TestAdminLandingPage_UsesConfiguredPublicPreviewBaseURL(t *testing.T) {
	srv := newAdminTestServer(t, model.Config{
		Aliases: []model.Alias{{Alias: "gh", Destination: "https://github.com"}},
	})
	srv.publicBase = "https://go.example.com"

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), `href="https://go.example.com/preview?alias=gh"`) {
		t.Fatalf("expected admin preview link to point at public base URL, body = %q", w.Body.String())
	}
}

func TestAdminLandingPage_DestinationLinksStripPlaceholders(t *testing.T) {
	srv := newAdminTestServer(t, model.Config{
		Aliases: []model.Alias{
			{Alias: "repo/{test}", Destination: "https://github.com/jovalle/{test}"},
			{Alias: "{query}", Destination: "https://{query}.techn.is"},
		},
	})

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	for _, unexpected := range []string{
		`href="https://github.com/jovalle/{test}"`,
		`href="https://{query}.techn.is"`,
		`href="https://.techn.is"`,
	} {
		if strings.Contains(body, unexpected) {
			t.Fatalf("admin destination href should not contain %q, body = %q", unexpected, body)
		}
	}
	for _, expected := range []string{
		`href="https://github.com/jovalle/"`,
		`href="https://techn.is"`,
		`https://github.com/jovalle/{test}`,
		`https://{query}.techn.is`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("expected admin page to contain %q, body = %q", expected, body)
		}
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

func TestHandleRedirect_LegacyFallbackRule(t *testing.T) {
	srv := newPublicTestServer(t, model.Config{
		Aliases: []model.Alias{{
			Alias:       "{query}",
			Destination: "https://{query}.example.invalid",
		}},
	})
	req := httptest.NewRequest("GET", "/sonarr", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "https://sonarr.example.invalid" {
		t.Errorf("Location = %q", loc)
	}
}

func TestAdminServerAliasPathsReturnNotFound(t *testing.T) {
	srv := newAdminTestServer(t, model.Config{
		Aliases: []model.Alias{{
			Alias:       "{query}",
			Destination: "https://{query}.example.invalid",
		}},
	})
	req := httptest.NewRequest("GET", "/sonarr", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
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

func TestValidateDestinationInput_DoesNotTreatSharedHostAsDuplicate(t *testing.T) {
	result := validateDestinationInput(
		httptest.NewRequest("GET", "/", nil).Context(),
		newOnlineAliasStatusChecker(),
		"",
		"",
		store.NormalizeDestination("github.com/jovalle/{test}"),
		[]model.Alias{{Alias: "gh", Destination: "https://github.com"}},
	)

	if result.Normalized != "https://github.com/jovalle/{test}" {
		t.Fatalf("normalized = %q", result.Normalized)
	}
	if len(result.Matches) != 0 {
		t.Fatalf("matches = %#v, want none", result.Matches)
	}
	if strings.Contains(result.Message, "Destination is already used") {
		t.Fatalf("unexpected duplicate warning: %q", result.Message)
	}
}

func TestValidateDestinationInput_WarnsForSameDestinationTemplate(t *testing.T) {
	result := validateDestinationInput(
		httptest.NewRequest("GET", "/", nil).Context(),
		newOnlineAliasStatusChecker(),
		"",
		"",
		store.NormalizeDestination("github.com/jovalle/{test}"),
		[]model.Alias{{Alias: "repo", Destination: "https://github.com/jovalle/{test}"}},
	)

	if result.State != "warning" {
		t.Fatalf("state = %q, want warning", result.State)
	}
	if len(result.Matches) != 1 || result.Matches[0] != "repo" {
		t.Fatalf("matches = %#v, want repo", result.Matches)
	}
	if !strings.Contains(result.Message, "Destination is already used by repo.") {
		t.Fatalf("message = %q", result.Message)
	}
}

func TestValidateDestinationInput_RejectsInvalidPlaceholderDefaultSyntax(t *testing.T) {
	result := validateDestinationInput(
		httptest.NewRequest("GET", "/", nil).Context(),
		newOnlineAliasStatusChecker(),
		"",
		"",
		store.NormalizeDestination("https://github.com/{owner:jovalle}/goku"),
		nil,
	)

	if result.State != "error" {
		t.Fatalf("state = %q, want error", result.State)
	}
	if !strings.Contains(result.Message, "must use :=") {
		t.Fatalf("message = %q, want := guidance", result.Message)
	}
}

func TestValidateDestinationInput_RejectsDestinationPlaceholderDefault(t *testing.T) {
	result := validateDestinationInput(
		httptest.NewRequest("GET", "/", nil).Context(),
		newOnlineAliasStatusChecker(),
		"",
		"gh/{owner}",
		store.NormalizeDestination("https://github.com/{owner:=jovalle}/goku"),
		nil,
	)

	if result.State != "error" {
		t.Fatalf("state = %q, want error", result.State)
	}
	if !strings.Contains(result.Message, "defined in alias") {
		t.Fatalf("message = %q, want alias-only guidance", result.Message)
	}
}

func TestHandleValidateAlias_InheritsPlaceholderDefaults(t *testing.T) {
	srv := newAdminTestServer(t, model.Config{})
	srv.statusChecker = newOnlineAliasStatusChecker()

	req := httptest.NewRequest("GET", "/api/aliases/validate?alias=gh/{owner:=jovalle}/{repo}&destination=https://github.com/{owner}/{repo}", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp aliasValidationResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode validation response: %v", err)
	}
	if !resp.CanSubmit {
		t.Fatalf("can_submit = false, response = %#v", resp)
	}
	if resp.Alias.Normalized != "gh/{owner:=jovalle}/{repo}" {
		t.Fatalf("alias normalized = %q", resp.Alias.Normalized)
	}
	if resp.Destination.Normalized != "https://github.com/{owner}/{repo}" {
		t.Fatalf("destination normalized = %q", resp.Destination.Normalized)
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

func TestHandleBulkDeleteAliases(t *testing.T) {
	srvs := newTestServers(t, model.Config{
		Aliases: []model.Alias{
			{Alias: "gh", Destination: "https://github.com"},
			{Alias: "docs", Destination: "https://docs.example.com"},
			{Alias: "wiki", Destination: "https://wikipedia.org"},
		},
	}, AuthConfig{})

	form := url.Values{
		"alias": {"gh", "wiki"},
	}
	req := httptest.NewRequest("POST", "/api/aliases/bulk-delete", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srvs.admin.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", w.Code)
	}

	if _, ok := srvs.store.Alias("gh"); ok {
		t.Fatal("expected gh alias deleted")
	}
	if _, ok := srvs.store.Alias("wiki"); ok {
		t.Fatal("expected wiki alias deleted")
	}
	if _, ok := srvs.store.Alias("docs"); !ok {
		t.Fatal("expected docs alias to remain")
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

func TestHandleEditAlias_JSONResponse(t *testing.T) {
	srvs := newTestServers(t, model.Config{
		Aliases: []model.Alias{
			{Alias: "gh", Destination: "https://github.com"},
			{Alias: "docs", Destination: "https://docs.example.com", Enabled: model.BoolPtr(false)},
		},
	}, AuthConfig{})

	form := url.Values{
		"old_alias":   {"gh"},
		"alias":       {"git"},
		"destination": {"github.com"},
		"enabled":     {"1"},
	}
	req := httptest.NewRequest("POST", "/api/aliases/edit", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	w := httptest.NewRecorder()
	srvs.admin.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %q", w.Code, w.Body.String())
	}
	var resp editAliasResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp.OldAlias != "gh" || resp.Alias != "git" || resp.Destination != "https://github.com" || !resp.Enabled {
		t.Fatalf("response = %#v", resp)
	}
	if resp.ActiveAliases != 1 || resp.TotalAliases != 2 {
		t.Fatalf("counts = %#v", resp)
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

func TestHandleToggleAlias_JSON(t *testing.T) {
	srvs := newTestServers(t, model.Config{
		Aliases: []model.Alias{
			{Alias: "gh", Destination: "https://github.com"},
			{Alias: "docs", Destination: "https://docs.example.com"},
		},
	}, AuthConfig{})

	form := url.Values{"alias": {"gh"}, "enabled": {"0"}}
	req := httptest.NewRequest("POST", "/api/aliases/toggle", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	w := httptest.NewRecorder()
	srvs.admin.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want JSON", ct)
	}
	var resp toggleAliasResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp.Alias != "gh" || resp.Enabled {
		t.Fatalf("toggle response = %#v", resp)
	}
	if resp.ActiveAliases != 1 || resp.TotalAliases != 2 {
		t.Fatalf("counts = active %d total %d, want 1 and 2", resp.ActiveAliases, resp.TotalAliases)
	}
}

func TestHandleAliasPreview(t *testing.T) {
	srv := newPublicTestServer(t, model.Config{
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
	srv := newPublicTestServer(t, model.Config{
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

func TestDestinationHref_StripsPlaceholders(t *testing.T) {
	tests := []struct {
		name        string
		alias       string
		destination string
		want        string
	}{
		{
			name:        "path placeholder",
			alias:       "repo/{test}",
			destination: "https://github.com/jovalle/{test}",
			want:        "https://github.com/jovalle/",
		},
		{
			name:        "path placeholder pair",
			alias:       "repo/{owner}/{repo}",
			destination: "https://github.com/{owner}/{repo}",
			want:        "https://github.com/",
		},
		{
			name:        "query placeholder",
			alias:       "yt/{}",
			destination: "https://www.youtube.com/results?search_query={}",
			want:        "https://www.youtube.com/results?search_query=",
		},
		{
			name:        "host placeholder",
			alias:       "{query}",
			destination: "https://{query}.techn.is",
			want:        "https://techn.is",
		},
		{
			name:        "bare destination",
			alias:       "repo/{test}",
			destination: "github.com/jovalle/{test}",
			want:        "https://github.com/jovalle/",
		},
		{
			name:        "path placeholder default",
			alias:       "repo/{owner:=jovalle}/{repo:=goku}",
			destination: "https://github.com/{owner}/{repo}",
			want:        "https://github.com/jovalle/goku",
		},
		{
			name:        "query placeholder default",
			alias:       "yt/{query:=goku}",
			destination: "https://www.youtube.com/results?search_query={query}",
			want:        "https://www.youtube.com/results?search_query=goku",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := destinationHref(tt.alias, tt.destination)
			if got != tt.want {
				t.Fatalf("destinationHref(%q) = %q, want %q", tt.destination, got, tt.want)
			}
			if strings.Contains(got, "{") || strings.Contains(got, "}") {
				t.Fatalf("destinationHref(%q) still contains placeholder braces: %q", tt.destination, got)
			}
		})
	}
}

func TestStatusProbeURL_UsesPlaceholderDefaults(t *testing.T) {
	got := statusProbeURL("repo/{owner:=jovalle}/{repo:=goku}", "https://github.com/{owner}/{repo}")
	if got != "https://github.com/jovalle/goku" {
		t.Fatalf("statusProbeURL default placeholder = %q, want %q", got, "https://github.com/jovalle/goku")
	}

	got = statusProbeURL("repo/{:=goku}", "https://github.com/jovalle/{}")
	if got != "https://github.com/jovalle/goku" {
		t.Fatalf("statusProbeURL anonymous default placeholder = %q, want %q", got, "https://github.com/jovalle/goku")
	}
}

func TestHandleAliasPreview_MissingAliasNamesAlias(t *testing.T) {
	srv := newPublicTestServer(t, model.Config{})

	req := httptest.NewRequest("GET", "/preview?alias=wiki", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "No golink matched <code>wiki</code>.") {
		t.Fatalf("expected missing alias message, body = %q", body)
	}
	if strings.Contains(body, "No golink matched <code>/preview</code>") {
		t.Fatalf("preview should not report generic /preview miss, body = %q", body)
	}
}

func TestHandleAliasPreview_DisabledAliasNamesAlias(t *testing.T) {
	srv := newPublicTestServer(t, model.Config{
		Aliases: []model.Alias{{Alias: "wiki", Destination: "https://en.wikipedia.org/wiki/{topic}", Enabled: model.BoolPtr(false)}},
	})

	req := httptest.NewRequest("GET", "/preview?alias=wiki", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	if !strings.Contains(w.Body.String(), "No golink matched <code>wiki</code>.") {
		t.Fatalf("expected disabled alias message, body = %q", w.Body.String())
	}
}

func TestAdminServerExposesPreview(t *testing.T) {
	srv := newAdminTestServer(t, model.Config{
		Aliases: []model.Alias{{Alias: "gh", Destination: "https://github.com"}},
	})

	req := httptest.NewRequest("GET", "/preview?alias=gh", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "https://github.com") {
		t.Fatalf("expected preview destination, body = %q", w.Body.String())
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

	body := "docs docs.example.com\nr/{rest...},reddit.com/r/{rest...}\n"
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
	alias, ok := srvs.store.Alias("docs")
	if !ok {
		t.Fatal("expected docs alias")
	}
	if alias.Destination != "https://docs.example.com" {
		t.Fatalf("docs destination = %q", alias.Destination)
	}
}

func TestHandleBatchImport_TextRejectsOnlyInvalidRows(t *testing.T) {
	srvs := newTestServers(t, model.Config{}, AuthConfig{})

	body := "tes a13 jaf iawefl\nok /not-a-url\n"
	req := httptest.NewRequest("POST", "/api/import", strings.NewReader(body))
	req.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	srvs.admin.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}

	var resp batchImportResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp.ImportedAliases != 0 || len(resp.Errors) != 2 {
		t.Fatalf("response = %#v", resp)
	}
	if len(srvs.store.Aliases()) != 0 {
		t.Fatalf("expected no aliases imported, got %d", len(srvs.store.Aliases()))
	}
}

func TestHandleBatchImport_YAML(t *testing.T) {
	srvs := newTestServers(t, model.Config{}, AuthConfig{})

	body := "aliases:\n  - alias: docs\n    destination: https://docs.example.com\n    enabled: false\n"
	req := httptest.NewRequest("POST", "/api/import", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/yaml")
	w := httptest.NewRecorder()
	srvs.admin.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", w.Code)
	}

	alias, ok := srvs.store.Alias("docs")
	if !ok {
		t.Fatal("expected docs alias imported")
	}
	if alias.IsEnabled() {
		t.Fatal("expected YAML import to preserve disabled state")
	}
}

func TestHandleImportPreview_Pseudo(t *testing.T) {
	srvs := newTestServers(t, model.Config{
		Aliases: []model.Alias{{Alias: "gh", Destination: "https://github.com"}},
	}, AuthConfig{})

	body := `{"format":"pseudo","content":"gh https://github.com/new\nbadline\ndocs https://docs.example.com"}`
	req := httptest.NewRequest("POST", "/api/import/preview", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srvs.admin.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp importPreviewResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp.Format != "pseudo" {
		t.Fatalf("format = %q, want pseudo", resp.Format)
	}
	if resp.ValidCount != 2 || resp.InvalidCount != 1 {
		t.Fatalf("preview counts = %#v", resp)
	}
	if resp.ReplaceCount != 1 || resp.NewCount != 1 {
		t.Fatalf("replace/new counts = %#v", resp)
	}
}

func TestHandleImportPreview_PseudoRejectsManyArgumentsAndNormalizesHTTPS(t *testing.T) {
	srv := newAdminTestServer(t, model.Config{})

	body := `{"format":"text","content":"tes a13 jaf iawefl\nok docs.example.com\nlocal localhost:3000/app"}`
	req := httptest.NewRequest("POST", "/api/import/preview", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp importPreviewResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp.Format != "pseudo" {
		t.Fatalf("format = %q, want pseudo", resp.Format)
	}
	if resp.ValidCount != 2 || resp.InvalidCount != 1 {
		t.Fatalf("preview counts = %#v", resp)
	}
	if !strings.Contains(resp.Items[0].Error, "exactly alias and destination") {
		t.Fatalf("first error = %q", resp.Items[0].Error)
	}
	if resp.Items[1].Destination != "https://docs.example.com" {
		t.Fatalf("normalized destination = %q", resp.Items[1].Destination)
	}
	if resp.Items[2].Destination != "http://localhost:3000/app" {
		t.Fatalf("localhost destination = %q", resp.Items[2].Destination)
	}
}

func TestHandleImportPreview_YAML(t *testing.T) {
	srv := newAdminTestServer(t, model.Config{})

	body := `{"format":"yaml","content":"aliases:\n  - alias: docs\n    destination: https://docs.example.com\n    enabled: false\n"}`
	req := httptest.NewRequest("POST", "/api/import/preview", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp importPreviewResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp.Format != "yaml" {
		t.Fatalf("format = %q, want yaml", resp.Format)
	}
	if len(resp.Items) != 1 || resp.Items[0].Enabled {
		t.Fatalf("expected disabled YAML import item, got %#v", resp.Items)
	}
}

func TestHandleImportPreview_YAMLSampleShape(t *testing.T) {
	srv := newAdminTestServer(t, model.Config{
		Aliases: []model.Alias{{Alias: "gh", Destination: "https://github.com"}},
	})

	content := `aliases:
- alias: npm
  destination: https://www.npmjs.com
- alias: r/{rest...}
  destination: https://www.reddit.com/r/{rest...}
- alias: gh
  destination: https://github.com
- alias: '{query}'
  destination: https://{query}.techn.is
  enabled: true
`
	bodyBytes, err := json.Marshal(importPreviewRequest{Format: "yaml", Content: content})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest("POST", "/api/import/preview", strings.NewReader(string(bodyBytes)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %q", w.Code, w.Body.String())
	}
	var resp importPreviewResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp.Format != "yaml" || resp.ValidCount != 4 || resp.ReplaceCount != 1 || resp.NewCount != 3 {
		t.Fatalf("preview response = %#v", resp)
	}
	if resp.Items[3].Alias != "{query}" || !resp.Items[3].Enabled {
		t.Fatalf("quoted alias item = %#v", resp.Items[3])
	}
}

func TestHandleImportPreview_YAMLReplacementUsesSourceLine(t *testing.T) {
	srv := newAdminTestServer(t, model.Config{
		Aliases: []model.Alias{{Alias: "g", Destination: "https://google.com"}},
	})

	content := `aliases:
- alias: npm
  destination: https://www.npmjs.com
- alias: wiki
  destination: https://www.wikipedia.org
- alias: x
  destination: https://x.com
- alias: claude
  destination: https://claude.ai
- alias: crates
  destination: https://crates.io
- alias: g
  destination: test.tecom
`
	bodyBytes, err := json.Marshal(importPreviewRequest{Format: "yaml", Content: content})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest("POST", "/api/import/preview", strings.NewReader(string(bodyBytes)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %q", w.Code, w.Body.String())
	}
	var resp importPreviewResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	var replacement, xItem importPreviewItem
	for _, item := range resp.Items {
		switch item.Alias {
		case "g":
			replacement = item
		case "x":
			xItem = item
		}
	}
	if replacement.Status != "replace" || replacement.Line != 12 {
		t.Fatalf("replacement item = %#v, want replace on line 12", replacement)
	}
	if xItem.Status == "replace" || xItem.Line != 6 {
		t.Fatalf("x item = %#v, want non-replace on line 6", xItem)
	}
}

func TestHandleImportPreview_JSONSampleShape(t *testing.T) {
	srv := newAdminTestServer(t, model.Config{})

	content := `{
  "aliases": [
    {"alias": "npm", "destination": "https://www.npmjs.com"},
    {"alias": "r/{rest...}", "destination": "https://www.reddit.com/r/{rest...}"},
    {"alias": "{query}", "destination": "https://{query}.techn.is", "enabled": true}
  ]
}`
	bodyBytes, err := json.Marshal(importPreviewRequest{Format: "json", Content: content})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest("POST", "/api/import/preview", strings.NewReader(string(bodyBytes)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %q", w.Code, w.Body.String())
	}
	var resp importPreviewResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp.Format != "json" || resp.ValidCount != 3 || resp.NewCount != 3 {
		t.Fatalf("preview response = %#v", resp)
	}
	if resp.Items[2].Alias != "{query}" || resp.Items[2].Destination != "https://{query}.techn.is" {
		t.Fatalf("placeholder item = %#v", resp.Items[2])
	}
}

func TestHandleExportAliases_JSON(t *testing.T) {
	srv := newAdminTestServer(t, model.Config{
		Aliases: []model.Alias{{Alias: "docs", Destination: "https://docs.example.com"}},
	})

	req := httptest.NewRequest("GET", "/api/export?format=json", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("content-type = %q, want application/json", ct)
	}
	if !strings.Contains(w.Body.String(), `"aliases"`) || !strings.Contains(w.Body.String(), `"docs"`) {
		t.Fatalf("unexpected export body = %q", w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"links"`) {
		t.Fatalf("export should not include links = %q", w.Body.String())
	}
}

func TestHandleExportAliases_YAML(t *testing.T) {
	srv := newAdminTestServer(t, model.Config{
		Aliases: []model.Alias{{Alias: "docs", Destination: "https://docs.example.com"}},
	})

	req := httptest.NewRequest("GET", "/api/export?format=yaml", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "yaml") {
		t.Fatalf("content-type = %q, want yaml", ct)
	}
	if !strings.Contains(w.Body.String(), "aliases:") ||
		!strings.Contains(w.Body.String(), `alias: "docs"`) ||
		!strings.Contains(w.Body.String(), `destination: "https://docs.example.com"`) {
		t.Fatalf("unexpected export body = %q", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "links:") {
		t.Fatalf("export should not include links = %q", w.Body.String())
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

	for _, path := range []string{"/api/aliases"} {
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

func TestMetrics_NotExposedOnPublicServer(t *testing.T) {
	srv := newPublicTestServer(t, model.Config{})

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
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

func TestHealthWebSocket_NotExposedOnAdminServer(t *testing.T) {
	srv := newAdminTestServer(t, model.Config{})
	httpSrv := httptest.NewServer(srv)
	defer httpSrv.Close()

	wsURL := "ws" + strings.TrimPrefix(httpSrv.URL, "http") + "/ws/health"
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		t.Fatal("expected websocket dial to fail on admin server")
	}
	if resp == nil {
		t.Fatal("expected HTTP response from failed websocket upgrade")
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
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
