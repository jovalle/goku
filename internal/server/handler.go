package server

import (
	"bufio"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/jovalle/goku/internal/config"
	"github.com/jovalle/goku/internal/metrics"
	"github.com/jovalle/goku/internal/model"
	"github.com/jovalle/goku/internal/resolve"
	"github.com/jovalle/goku/internal/ui"
)

// Version info set via ldflags.
var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

var startTime = time.Now()

type publicPageData struct {
	AliasCount int
	Health     healthResponse
}

type adminPageData struct {
	AliasCount int
	Aliases    []model.Alias
	ShowLogout  bool
}

type loginPageData struct {
	Invalid bool
}

type redirectPreviewPageData struct {
	Alias       string
	Destination string
	Delay       int
}

type healthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Aliases int    `json:"aliases"`
	Links   int    `json:"links,omitempty"`
	Rules   int    `json:"rules,omitempty"`
	Uptime  string `json:"uptime"`
}

type batchImportResponse struct {
	ImportedAliases int      `json:"imported_aliases"`
	TotalAliases    int      `json:"total_aliases"`
	Errors          []string `json:"errors,omitempty"`
}

type brokenLinkItem struct {
	Path  string `json:"path"`
	Count int    `json:"count"`
}

type brokenLinksResponse struct {
	Count int              `json:"count"`
	Items []brokenLinkItem `json:"items"`
}

var brokenLinks = struct {
	mu    sync.Mutex
	items map[string]int
}{items: map[string]int{}}

var healthUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

var placeholderTokenPattern = regexp.MustCompile(`\{[^{}]*\}`)

func (s *Server) handlePublicHome(w http.ResponseWriter, r *http.Request) {
	data := publicPageData{
		AliasCount: len(s.store.Aliases()),
		Health:     s.currentHealth(),
	}
	s.renderTemplate(w, http.StatusOK, "templates/public.html", data)
}

func (s *Server) handleAdminHome(w http.ResponseWriter, r *http.Request) {
	if s.uiAuthEnabled() && !(s.validSession(r) || s.validBasic(r)) {
		s.renderTemplate(w, http.StatusOK, "templates/login.html", loginPageData{})
		return
	}

	if !s.uiAuthEnabled() && s.auth.APIKey != "" && !s.validSession(r) {
		http.SetCookie(w, &http.Cookie{
			Name:     adminSessionCookieName,
			Value:    s.adminSessionToken(),
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
	}

	aliases := s.store.Aliases()
	sort.Slice(aliases, func(i, j int) bool {
		return aliases[i].Alias < aliases[j].Alias
	})

	data := adminPageData{AliasCount: len(aliases), Aliases: aliases, ShowLogout: s.uiAuthEnabled()}
	s.renderTemplate(w, http.StatusOK, "templates/admin.html", data)
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if !s.uiAuthEnabled() {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if s.validSession(r) || s.validBasic(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.renderTemplate(w, http.StatusOK, "templates/login.html", loginPageData{})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.uiAuthEnabled() {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	password := r.FormValue("password")
	if subtle.ConstantTimeCompare([]byte(password), []byte(s.auth.Password)) != 1 {
		s.renderTemplate(w, http.StatusUnauthorized, "templates/login.html", loginPageData{Invalid: true})
		return
	}

	cookie := &http.Cookie{
		Name:     adminSessionCookieName,
		Value:    s.adminSessionToken(),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
	if r.FormValue("remember") == "on" {
		cookie.MaxAge = 60 * 60 * 24 * 30
		cookie.Expires = time.Now().Add(30 * 24 * time.Hour)
	}
	http.SetCookie(w, cookie)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     adminSessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) handleRedirectOr404(w http.ResponseWriter, r *http.Request) {
	path := pathFromRequest(r)
	if path == "" {
		s.handleNotFound(w, r)
		return
	}
	s.handleRedirect(w, r)
}

func (s *Server) handleRedirect(w http.ResponseWriter, r *http.Request) {
	path := pathFromRequest(r)
	if path == "" {
		s.handleNotFound(w, r)
		return
	}

	url, err := s.store.Resolve(path)
	if err != nil {
		if errors.Is(err, resolve.ErrNotFound) {
			recordBroken(path)
			s.handleNotFound(w, r)
			return
		}
		s.logger.Error("resolve failed", "path", path, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	shortName := strings.SplitN(path, "/", 2)[0]
	metrics.RedirectsTotal.WithLabelValues(shortName).Inc()

	http.Redirect(w, r, url, http.StatusFound)
}

func (s *Server) handleListLinks(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.store.Links())
}

func (s *Server) handleAddLink(w http.ResponseWriter, r *http.Request) {
	alias := strings.TrimSpace(r.FormValue("name"))
	destination := strings.TrimSpace(r.FormValue("url"))
	if alias == "" || destination == "" {
		http.Error(w, "name and url required", http.StatusBadRequest)
		return
	}

	cfg, err := s.store.AddAlias(alias, destination)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := config.Save(s.configPath, cfg); err != nil {
		http.Error(w, "failed to save", http.StatusInternalServerError)
		return
	}
	metrics.AliasesTotal.Set(float64(len(cfg.Aliases)))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleDeleteLink(w http.ResponseWriter, r *http.Request) {
	alias := r.PathValue("name")
	if alias == "" {
		alias = r.FormValue("name")
	}
	if alias == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}

	cfg := s.store.DeleteAlias(alias)
	if err := config.Save(s.configPath, cfg); err != nil {
		http.Error(w, "failed to save", http.StatusInternalServerError)
		return
	}
	metrics.AliasesTotal.Set(float64(len(cfg.Aliases)))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleAddRule(w http.ResponseWriter, r *http.Request) {
	rule := model.Rule{
		Name:     r.FormValue("name"),
		Type:     r.FormValue("type"),
		Pattern:  r.FormValue("pattern"),
		Redirect: r.FormValue("redirect"),
	}
	if rule.Name == "" || rule.Type == "" || rule.Pattern == "" || rule.Redirect == "" {
		http.Error(w, "all rule fields required", http.StatusBadRequest)
		return
	}

	cfg := s.store.AddRule(rule)
	if err := config.Save(s.configPath, cfg); err != nil {
		s.logger.Error("failed to save config", "error", err)
		http.Error(w, "failed to save", http.StatusInternalServerError)
		return
	}

	metrics.AliasesTotal.Set(float64(len(cfg.Aliases)))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleDeleteRule(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		name = r.FormValue("name")
	}
	if name == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}

	cfg := s.store.DeleteRule(name)
	if err := config.Save(s.configPath, cfg); err != nil {
		s.logger.Error("failed to save config", "error", err)
		http.Error(w, "failed to save", http.StatusInternalServerError)
		return
	}
	metrics.AliasesTotal.Set(float64(len(cfg.Aliases)))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleBatchImport(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	bodyText := strings.TrimSpace(string(body))
	if bodyText == "" {
		http.Error(w, "empty request body", http.StatusBadRequest)
		return
	}

	toImport := make([]model.Alias, 0)
	if strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		if err := json.Unmarshal(body, &toImport); err != nil {
			http.Error(w, "invalid JSON payload", http.StatusBadRequest)
			return
		}
	} else {
		s := bufio.NewScanner(strings.NewReader(bodyText))
		for s.Scan() {
			line := strings.TrimSpace(s.Text())
			if line == "" {
				continue
			}
			if strings.Contains(line, ",") {
				parts := strings.SplitN(line, ",", 2)
				toImport = append(toImport, model.Alias{Alias: strings.TrimSpace(parts[0]), Destination: strings.TrimSpace(parts[1])})
				continue
			}
			parts := strings.Fields(line)
			if len(parts) < 2 {
				continue
			}
			toImport = append(toImport, model.Alias{Alias: parts[0], Destination: strings.Join(parts[1:], " ")})
		}
	}

	resp := batchImportResponse{}
	for _, a := range toImport {
		cfg, err := s.store.AddAlias(strings.TrimSpace(a.Alias), strings.TrimSpace(a.Destination))
		if err != nil {
			resp.Errors = append(resp.Errors, err.Error())
			continue
		}
		resp.ImportedAliases++
		resp.TotalAliases = len(cfg.Aliases)
	}

	if resp.ImportedAliases > 0 {
		cfg := s.store.Config()
		if err := config.Save(s.configPath, cfg); err != nil {
			http.Error(w, "failed to save", http.StatusInternalServerError)
			return
		}
		metrics.AliasesTotal.Set(float64(len(cfg.Aliases)))
	}

	if resp.TotalAliases == 0 {
		resp.TotalAliases = len(s.store.Aliases())
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleBrokenLinks(w http.ResponseWriter, r *http.Request) {
	resp := brokenLinksResponse{}
	brokenLinks.mu.Lock()
	for path, count := range brokenLinks.items {
		resp.Items = append(resp.Items, brokenLinkItem{Path: path, Count: count})
	}
	brokenLinks.mu.Unlock()

	sort.Slice(resp.Items, func(i, j int) bool {
		return resp.Items[i].Path < resp.Items[j].Path
	})
	resp.Count = len(resp.Items)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	resp := s.currentHealth()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleHealthWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := healthUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	send := func() bool {
		_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if err := conn.WriteJSON(s.currentHealth()); err != nil {
			return false
		}
		return true
	}

	if !send() {
		return
	}

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if !send() {
				return
			}
		}
	}
}

func (s *Server) handleListAliases(w http.ResponseWriter, r *http.Request) {
	aliases := s.store.Aliases()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(aliases)
}

func (s *Server) handleAddAlias(w http.ResponseWriter, r *http.Request) {
	alias := strings.TrimSpace(r.FormValue("alias"))
	if alias == "" {
		alias = strings.TrimSpace(r.FormValue("name"))
	}
	destination := strings.TrimSpace(r.FormValue("destination"))
	if destination == "" {
		destination = strings.TrimSpace(r.FormValue("url"))
	}

	if alias == "" || destination == "" {
		http.Error(w, "alias and destination required", http.StatusBadRequest)
		return
	}

	cfg, err := s.store.AddAlias(alias, destination)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := config.Save(s.configPath, cfg); err != nil {
		s.logger.Error("failed to save config", "error", err)
		http.Error(w, "failed to save", http.StatusInternalServerError)
		return
	}

	metrics.AliasesTotal.Set(float64(len(cfg.Aliases)))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleEditAlias(w http.ResponseWriter, r *http.Request) {
	oldAlias := strings.TrimSpace(r.FormValue("old_alias"))
	if oldAlias == "" {
		oldAlias = strings.TrimSpace(r.FormValue("original_alias"))
	}
	alias := strings.TrimSpace(r.FormValue("alias"))
	destination := strings.TrimSpace(r.FormValue("destination"))
	enabled := parseEnabledFormValue(r.FormValue("enabled"))

	if alias == "" || destination == "" {
		http.Error(w, "alias and destination required", http.StatusBadRequest)
		return
	}
	if oldAlias == "" {
		oldAlias = alias
	}

	cfg, err := s.store.UpdateAlias(oldAlias, alias, destination, enabled)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := config.Save(s.configPath, cfg); err != nil {
		s.logger.Error("failed to save config", "error", err)
		http.Error(w, "failed to save", http.StatusInternalServerError)
		return
	}

	metrics.AliasesTotal.Set(float64(len(cfg.Aliases)))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleToggleAlias(w http.ResponseWriter, r *http.Request) {
	alias := strings.TrimSpace(r.FormValue("alias"))
	if alias == "" {
		http.Error(w, "alias required", http.StatusBadRequest)
		return
	}

	enabled := parseEnabledFormValue(r.FormValue("enabled"))
	cfg, err := s.store.SetAliasEnabled(alias, enabled)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := config.Save(s.configPath, cfg); err != nil {
		s.logger.Error("failed to save config", "error", err)
		http.Error(w, "failed to save", http.StatusInternalServerError)
		return
	}

	metrics.AliasesTotal.Set(float64(len(cfg.Aliases)))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleDeleteAlias(w http.ResponseWriter, r *http.Request) {
	alias := r.PathValue("alias")
	if alias == "" {
		alias = r.FormValue("alias")
	}
	if alias == "" {
		alias = r.FormValue("name")
	}
	if alias == "" {
		http.Error(w, "alias required", http.StatusBadRequest)
		return
	}

	cfg := s.store.DeleteAlias(alias)
	if err := config.Save(s.configPath, cfg); err != nil {
		s.logger.Error("failed to save config", "error", err)
		http.Error(w, "failed to save", http.StatusInternalServerError)
		return
	}

	metrics.AliasesTotal.Set(float64(len(cfg.Aliases)))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleOpenAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"openapi":"3.0.0","info":{"title":"goku API","version":"1.0.0"},"paths":{"/api/aliases":{"get":{},"post":{}},"/api/aliases/delete":{"post":{}}}}`))
}

func (s *Server) handleSwagger(w http.ResponseWriter, r *http.Request) {
	if s.uiAuthEnabled() && !(s.validSession(r) || s.validBasic(r)) {
		s.renderTemplate(w, http.StatusOK, "templates/login.html", loginPageData{})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte("<!doctype html><html><head><meta charset=\"utf-8\"><title>goku swagger</title></head><body><h1>goku swagger</h1><p>OpenAPI available at <a href=\"/openapi.json\">/openapi.json</a>.</p></body></html>"))
}

func (s *Server) handleNotFound(w http.ResponseWriter, r *http.Request) {
	requested := r.URL.Path
	if requested == "" {
		requested = "/"
	}
	s.renderTemplate(w, http.StatusNotFound, "templates/404.html", map[string]string{"Path": requested})
}

func (s *Server) handleAliasPreview(w http.ResponseWriter, r *http.Request) {
	aliasPattern := strings.Trim(r.URL.Query().Get("alias"), "/")
	if aliasPattern != "" {
		if alias, ok := s.store.Alias(aliasPattern); ok {
			data := redirectPreviewPageData{
				Alias:       aliasPattern,
				Destination: stripPlaceholderValues(alias.Destination),
				Delay:       5,
			}
			s.renderTemplate(w, http.StatusOK, "templates/redirect.html", data)
			return
		}
		s.handleNotFound(w, r)
		return
	}

	aliasPath := strings.Trim(r.URL.Query().Get("path"), "/")
	if aliasPath == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}

	// When a placeholder pattern is passed directly, preview using empty substitutions.
	if strings.Contains(aliasPath, "{") {
		if alias, ok := s.store.Alias(aliasPath); ok {
			data := redirectPreviewPageData{
				Alias:       aliasPath,
				Destination: stripPlaceholderValues(alias.Destination),
				Delay:       5,
			}
			s.renderTemplate(w, http.StatusOK, "templates/redirect.html", data)
			return
		}
	}

	destination, err := s.store.Resolve(aliasPath)
	if err != nil {
		if errors.Is(err, resolve.ErrNotFound) {
			s.handleNotFound(w, r)
			return
		}
		s.logger.Error("resolve failed", "path", aliasPath, "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	data := redirectPreviewPageData{
		Alias:       aliasPath,
		Destination: destination,
		Delay:       5,
	}
	s.renderTemplate(w, http.StatusOK, "templates/redirect.html", data)
}

func (s *Server) handleLogo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(ui.Logo)
}

func (s *Server) renderTemplate(w http.ResponseWriter, status int, templateName string, data any) {
	tmpl, err := template.ParseFS(ui.Templates, templateName)
	if err != nil {
		s.logger.Error("template parse", "template", templateName, "error", err)
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := tmpl.Execute(w, data); err != nil {
		s.logger.Error("template render", "error", err)
	}
}

func (s *Server) currentHealth() healthResponse {
	return healthResponse{
		Status:  "ok",
		Version: Version,
		Commit:  Commit,
		Aliases: len(s.store.Aliases()),
		Links:   len(s.store.Links()),
		Rules:   len(s.store.Rules()),
		Uptime:  time.Since(startTime).Round(time.Second).String(),
	}
}

func pathFromRequest(r *http.Request) string {
	path := r.PathValue("path")
	if path != "" {
		return strings.Trim(path, "/")
	}
	return strings.Trim(strings.TrimPrefix(r.URL.Path, "/"), "/")
}

func parseEnabledFormValue(raw string) bool {
	v := strings.TrimSpace(strings.ToLower(raw))
	if v == "" {
		return false
	}
	if n, err := strconv.Atoi(v); err == nil {
		return n != 0
	}
	return v == "1" || v == "true" || v == "yes" || v == "on" || v == "enabled"
}

func stripPlaceholderValues(destination string) string {
	return placeholderTokenPattern.ReplaceAllString(destination, "")
}

func recordBroken(path string) {
	brokenLinks.mu.Lock()
	defer brokenLinks.mu.Unlock()
	brokenLinks.items[path]++
}

func resetBrokenLinks() {
	brokenLinks.mu.Lock()
	defer brokenLinks.mu.Unlock()
	brokenLinks.items = map[string]int{}
}
