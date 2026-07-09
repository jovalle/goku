package server

import (
	"bufio"
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"gopkg.in/yaml.v3"

	"github.com/jovalle/goku/internal/config"
	"github.com/jovalle/goku/internal/metrics"
	"github.com/jovalle/goku/internal/model"
	"github.com/jovalle/goku/internal/resolve"
	"github.com/jovalle/goku/internal/store"
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
	AliasCount     int
	ActiveAliases  int
	Aliases        []model.Alias
	Health         healthResponse
	ShowLogout     bool
	PreviewBaseURL string
}

type loginPageData struct {
	Invalid bool
}

type redirectPreviewPageData struct {
	Alias       string
	Destination string
	Delay       int
}

type notFoundPageData struct {
	Path    string
	Message string
}

type healthResponse struct {
	Status        string `json:"status"`
	Version       string `json:"version"`
	Commit        string `json:"commit"`
	Aliases       int    `json:"aliases"`
	ActiveAliases int    `json:"active_aliases"`
	Uptime        string `json:"uptime"`
}

type batchImportResponse struct {
	ImportedAliases int      `json:"imported_aliases"`
	TotalAliases    int      `json:"total_aliases"`
	Errors          []string `json:"errors,omitempty"`
}

type toggleAliasResponse struct {
	Alias         string `json:"alias"`
	Enabled       bool   `json:"enabled"`
	ActiveAliases int    `json:"active_aliases"`
	TotalAliases  int    `json:"total_aliases"`
}

type editAliasResponse struct {
	OldAlias      string `json:"old_alias"`
	Alias         string `json:"alias"`
	Destination   string `json:"destination"`
	Enabled       bool   `json:"enabled"`
	ActiveAliases int    `json:"active_aliases"`
	TotalAliases  int    `json:"total_aliases"`
}

type deleteAliasResponse struct {
	Alias         string `json:"alias"`
	ActiveAliases int    `json:"active_aliases"`
	TotalAliases  int    `json:"total_aliases"`
}

type importPreviewRequest struct {
	Format  string `json:"format"`
	Content string `json:"content"`
}

type importPreviewItem struct {
	Index       int    `json:"index"`
	Line        int    `json:"line,omitempty"`
	Source      string `json:"source,omitempty"`
	Alias       string `json:"alias,omitempty"`
	Destination string `json:"destination,omitempty"`
	Enabled     bool   `json:"enabled"`
	Valid       bool   `json:"valid"`
	Status      string `json:"status"`
	Error       string `json:"error,omitempty"`
}

type importPreviewResponse struct {
	Format       string              `json:"format"`
	Items        []importPreviewItem `json:"items"`
	ValidCount   int                 `json:"valid_count"`
	InvalidCount int                 `json:"invalid_count"`
	NewCount     int                 `json:"new_count"`
	ReplaceCount int                 `json:"replace_count"`
}

type importPayload struct {
	Aliases []model.Alias     `json:"aliases" yaml:"aliases"`
	Links   map[string]string `json:"links,omitempty" yaml:"links,omitempty"`
}

type importAliasInput struct {
	Alias       string `json:"alias" yaml:"alias"`
	Destination string `json:"destination" yaml:"destination"`
	Enabled     *bool  `json:"enabled,omitempty" yaml:"enabled,omitempty"`
}

type aliasStatusResponse struct {
	CheckedAt        time.Time         `json:"checked_at"`
	PollAfterSeconds int               `json:"poll_after_seconds"`
	Items            []aliasStatusItem `json:"items"`
}

type aliasValidationResponse struct {
	Alias       validationFieldResult `json:"alias"`
	Destination validationFieldResult `json:"destination"`
	CanSubmit   bool                  `json:"can_submit"`
}

type validationFieldResult struct {
	State      string   `json:"state"`
	Message    string   `json:"message"`
	Normalized string   `json:"normalized,omitempty"`
	Matches    []string `json:"matches,omitempty"`
}

type aliasStatusItem struct {
	Alias       string    `json:"alias"`
	Destination string    `json:"destination"`
	ProbeURL    string    `json:"probe_url"`
	State       string    `json:"state"`
	Detail      string    `json:"detail"`
	StatusCode  int       `json:"status_code,omitempty"`
	CheckedAt   time.Time `json:"checked_at"`
	Cached      bool      `json:"cached"`
}

type aliasStatusProbe struct {
	ProbeURL   string
	State      string
	Detail     string
	StatusCode int
	CheckedAt  time.Time
}

type aliasStatusChecker struct {
	mu            sync.Mutex
	cache         map[string]aliasStatusProbe
	client        *http.Client
	ttl           time.Duration
	timeout       time.Duration
	maxConcurrent int
}

var healthUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

var placeholderTokenPattern = regexp.MustCompile(`\{[^{}]*\}`)

func newAliasStatusChecker() *aliasStatusChecker {
	return &aliasStatusChecker{
		cache: map[string]aliasStatusProbe{},
		client: &http.Client{
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		ttl:           time.Minute,
		timeout:       2500 * time.Millisecond,
		maxConcurrent: 4,
	}
}

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

	data := adminPageData{
		AliasCount:     len(aliases),
		ActiveAliases:  activeAliasCount(aliases),
		Aliases:        aliases,
		Health:         s.currentHealth(),
		ShowLogout:     s.uiAuthEnabled(),
		PreviewBaseURL: s.publicBase,
	}
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

func (s *Server) handleBatchImport(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(string(body)) == "" {
		http.Error(w, "empty request body", http.StatusBadRequest)
		return
	}

	preview, err := s.buildImportPreview(body, detectImportFormat(r))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	resp := batchImportResponse{}
	for _, item := range preview.Items {
		if !item.Valid {
			resp.Errors = append(resp.Errors, importItemError(item))
			continue
		}
		cfg, err := s.store.UpsertAlias(model.Alias{
			Alias:       item.Alias,
			Destination: item.Destination,
			Enabled:     model.BoolPtr(item.Enabled),
		})
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
	if resp.ImportedAliases == 0 && len(resp.Errors) > 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(resp)
		return
	}
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleImportPreview(w http.ResponseWriter, r *http.Request) {
	var req importPreviewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON payload", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		http.Error(w, "content is required", http.StatusBadRequest)
		return
	}

	preview, err := s.buildImportPreview([]byte(req.Content), req.Format)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(preview)
}

func (s *Server) handleExportAliases(w http.ResponseWriter, r *http.Request) {
	format := normalizeImportFormat(r.URL.Query().Get("format"))
	if format == "" || format == "auto" {
		format = "yaml"
	}

	payload := importPayload{Aliases: s.store.Aliases()}
	var (
		body        []byte
		contentType string
		filename    string
		err         error
	)

	switch format {
	case "json":
		body, err = json.MarshalIndent(payload, "", "  ")
		contentType = "application/json"
		filename = "goku-aliases.json"
	case "yaml":
		body, err = yaml.Marshal(payload)
		contentType = "application/yaml"
		filename = "goku-aliases.yaml"
	default:
		http.Error(w, "format must be yaml or json", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, "failed to encode export", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	_, _ = w.Write(body)
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

func (s *Server) handleAliasStatuses(w http.ResponseWriter, r *http.Request) {
	resp := aliasStatusResponse{
		CheckedAt:        time.Now().UTC(),
		PollAfterSeconds: int(s.statusChecker.ttl / time.Second),
		Items:            s.statusChecker.statuses(r.Context(), s.store.Aliases()),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleValidateAlias(w http.ResponseWriter, r *http.Request) {
	alias := strings.Trim(strings.TrimSpace(r.URL.Query().Get("alias")), "/")
	destination := strings.TrimSpace(r.URL.Query().Get("destination"))
	oldAlias := strings.Trim(strings.TrimSpace(r.URL.Query().Get("old_alias")), "/")
	normalizedDestination := store.NormalizeDestination(destination)

	resp := aliasValidationResponse{
		Alias: validationFieldResult{
			State:   "empty",
			Message: "Alias is required.",
		},
		Destination: validationFieldResult{
			State:   "empty",
			Message: "Destination is required.",
		},
	}

	if alias != "" {
		resp.Alias = validateAliasInput(alias, oldAlias, normalizedDestination, s.store.Aliases())
	}
	if destination != "" {
		resp.Destination = validateDestinationInput(r.Context(), s.statusChecker, oldAlias, alias, normalizedDestination, s.store.Aliases())
	}
	if alias != "" && destination != "" {
		mergedAlias, mergedDestination, err := store.NormalizeAliasAndDestination(alias, destination)
		if err != nil {
			resp.Alias = validationFieldResult{State: "error", Message: err.Error(), Normalized: alias}
			resp.Destination = validationFieldResult{State: "error", Message: err.Error(), Normalized: normalizedDestination}
		} else {
			resp.Alias = validateAliasInput(mergedAlias, oldAlias, mergedDestination, s.store.Aliases())
			resp.Destination = validateDestinationInput(r.Context(), s.statusChecker, oldAlias, mergedAlias, mergedDestination, s.store.Aliases())
		}
	}

	resp.CanSubmit = resp.Alias.State != "error" &&
		resp.Destination.State != "error" &&
		resp.Alias.State != "empty" &&
		resp.Destination.State != "empty"

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
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
	if acceptsJSON(r) {
		savedAlias, savedDestination, err := store.NormalizeAliasAndDestination(alias, destination)
		if err != nil {
			savedAlias = alias
			savedDestination = store.NormalizeDestination(destination)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(editAliasResponse{
			OldAlias:      oldAlias,
			Alias:         savedAlias,
			Destination:   savedDestination,
			Enabled:       enabled,
			ActiveAliases: activeAliasCount(cfg.Aliases),
			TotalAliases:  len(cfg.Aliases),
		})
		return
	}
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
	if acceptsJSON(r) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(toggleAliasResponse{
			Alias:         alias,
			Enabled:       enabled,
			ActiveAliases: activeAliasCount(cfg.Aliases),
			TotalAliases:  len(cfg.Aliases),
		})
		return
	}
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
	if acceptsJSON(r) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(deleteAliasResponse{
			Alias:         alias,
			ActiveAliases: activeAliasCount(cfg.Aliases),
			TotalAliases:  len(cfg.Aliases),
		})
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleDeleteAliases(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	aliases := make([]string, 0, len(r.Form["alias"]))
	aliases = append(aliases, r.Form["alias"]...)
	aliases = append(aliases, r.Form["alias[]"]...)
	if len(aliases) == 0 {
		http.Error(w, "at least one alias is required", http.StatusBadRequest)
		return
	}

	cfg := s.store.DeleteAliases(aliases)
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
	s.renderTemplate(w, http.StatusNotFound, "templates/404.html", notFoundPageData{Path: requested})
}

func (s *Server) renderAliasPreviewNotFound(w http.ResponseWriter, alias string, disabled bool) {
	message := "Alias " + strconv.Quote(alias) + " does not exist."
	if disabled {
		message = "Alias " + strconv.Quote(alias) + " exists but is disabled."
	}
	s.renderTemplate(w, http.StatusNotFound, "templates/404.html", notFoundPageData{
		Path:    alias,
		Message: message,
	})
}

func (s *Server) handleAliasPreview(w http.ResponseWriter, r *http.Request) {
	aliasPattern := strings.Trim(r.URL.Query().Get("alias"), "/")
	if aliasPattern != "" {
		if alias, ok := s.store.Alias(aliasPattern); ok {
			if !alias.IsEnabled() {
				s.renderAliasPreviewNotFound(w, aliasPattern, true)
				return
			}
			data := redirectPreviewPageData{
				Alias:       aliasPattern,
				Destination: stripPlaceholderValues(alias.Alias, alias.Destination),
				Delay:       5,
			}
			s.renderTemplate(w, http.StatusOK, "templates/redirect.html", data)
			return
		}
		s.renderAliasPreviewNotFound(w, aliasPattern, false)
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
			if !alias.IsEnabled() {
				s.renderAliasPreviewNotFound(w, aliasPath, true)
				return
			}
			data := redirectPreviewPageData{
				Alias:       aliasPath,
				Destination: stripPlaceholderValues(alias.Alias, alias.Destination),
				Delay:       5,
			}
			s.renderTemplate(w, http.StatusOK, "templates/redirect.html", data)
			return
		}
	}

	destination, err := s.store.Resolve(aliasPath)
	if err != nil {
		if errors.Is(err, resolve.ErrNotFound) {
			if alias, ok := s.store.Alias(aliasPath); ok && !alias.IsEnabled() {
				s.renderAliasPreviewNotFound(w, aliasPath, true)
				return
			}
			s.renderAliasPreviewNotFound(w, aliasPath, false)
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

func (s *Server) handleFavicon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(ui.Favicon)
}

func (s *Server) renderTemplate(w http.ResponseWriter, status int, templateName string, data any) {
	tmpl, err := template.New(path.Base(templateName)).Funcs(template.FuncMap{
		"destinationHref":    destinationHref,
		"displayPlaceholder": store.StripPlaceholderDefaults,
	}).ParseFS(ui.Templates, templateName)
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
	aliases := s.store.Aliases()
	return healthResponse{
		Status:        "ok",
		Version:       Version,
		Commit:        Commit,
		Aliases:       len(aliases),
		ActiveAliases: activeAliasCount(aliases),
		Uptime:        time.Since(startTime).Round(time.Second).String(),
	}
}

func activeAliasCount(aliases []model.Alias) int {
	count := 0
	for _, alias := range aliases {
		if alias.IsEnabled() {
			count++
		}
	}
	return count
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

func acceptsJSON(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.Header.Get("Accept")), "application/json")
}

func detectImportFormat(r *http.Request) string {
	ct := strings.ToLower(r.Header.Get("Content-Type"))
	switch {
	case strings.Contains(ct, "json"):
		return "json"
	case strings.Contains(ct, "yaml"), strings.Contains(ct, "yml"):
		return "yaml"
	case strings.Contains(ct, "text/plain"):
		return "text"
	default:
		return "auto"
	}
}

func normalizeImportFormat(raw string) string {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "", "auto":
		return "auto"
	case "pseudo", "text", "plain", "txt":
		return "pseudo"
	case "yaml", "yml":
		return "yaml"
	case "json":
		return "json"
	default:
		return ""
	}
}

func (s *Server) buildImportPreview(body []byte, format string) (importPreviewResponse, error) {
	format = normalizeImportFormat(format)
	if format == "" {
		return importPreviewResponse{}, errors.New("format must be auto, pseudo, yaml, or json")
	}

	items, detected, err := parseImportItems(body, format)
	if err != nil {
		return importPreviewResponse{}, err
	}

	resp := importPreviewResponse{
		Format: detected,
		Items:  items,
	}
	existing := make(map[string]struct{}, len(s.store.Aliases()))
	for _, alias := range s.store.Aliases() {
		existing[alias.Alias] = struct{}{}
	}

	seen := make(map[string]int)
	for i := range resp.Items {
		item := &resp.Items[i]
		item.Index = i
		if item.Error != "" {
			item.Status = "invalid"
			resp.InvalidCount++
			continue
		}

		item.Alias = strings.Trim(item.Alias, "/")
		item.Destination = store.NormalizeDestination(item.Destination)
		if err := store.ValidateAlias(item.Alias, item.Destination); err != nil {
			item.Error = err.Error()
			item.Status = "invalid"
			resp.InvalidCount++
			continue
		}
		if firstLine, dup := seen[item.Alias]; dup {
			item.Error = "duplicate alias in import payload; first defined on line " + strconv.Itoa(firstLine)
			item.Status = "invalid"
			resp.InvalidCount++
			continue
		}
		seen[item.Alias] = item.Line
		item.Valid = true
		if _, ok := existing[item.Alias]; ok {
			item.Status = "replace"
			resp.ReplaceCount++
		} else {
			item.Status = "new"
			resp.NewCount++
		}
		resp.ValidCount++
	}

	return resp, nil
}

func parseImportItems(body []byte, format string) ([]importPreviewItem, string, error) {
	text := strings.TrimSpace(string(body))
	switch format {
	case "pseudo":
		return parseTextImportItems(text), "pseudo", nil
	case "json":
		items, err := parseStructuredImportItems(body, "json")
		return items, "json", err
	case "yaml":
		items, err := parseStructuredImportItems(body, "yaml")
		return items, "yaml", err
	case "auto":
		if json.Valid(body) {
			items, err := parseStructuredImportItems(body, "json")
			if err == nil {
				return items, "json", nil
			}
		}
		if items, err := parseStructuredImportItems(body, "yaml"); err == nil && len(items) > 0 {
			return items, "yaml", nil
		}
		return parseTextImportItems(text), "pseudo", nil
	default:
		return nil, "", errors.New("unsupported import format")
	}
}

func parseTextImportItems(body string) []importPreviewItem {
	items := make([]importPreviewItem, 0)
	scanner := bufio.NewScanner(strings.NewReader(body))
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		raw := scanner.Text()
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		item := importPreviewItem{
			Line:    lineNo,
			Source:  raw,
			Enabled: true,
		}
		if strings.Contains(line, ",") {
			parts := strings.SplitN(line, ",", 2)
			item.Alias = strings.TrimSpace(parts[0])
			item.Destination = strings.TrimSpace(parts[1])
		} else {
			parts := strings.Fields(line)
			if len(parts) != 2 {
				item.Error = "line must contain exactly alias and destination"
				items = append(items, item)
				continue
			}
			item.Alias = parts[0]
			item.Destination = parts[1]
		}
		items = append(items, item)
	}
	return items
}

func parseStructuredImportItems(body []byte, format string) ([]importPreviewItem, error) {
	aliases, err := parseStructuredAliases(body, format)
	if err != nil {
		if format == "json" {
			return nil, errors.New("invalid JSON payload")
		}
		return nil, errors.New("invalid YAML payload")
	}

	lineNumbers := structuredAliasLineNumbers(body, format)
	items := make([]importPreviewItem, 0, len(aliases))
	for i, alias := range aliases {
		enabled := alias.IsEnabled()
		items = append(items, importPreviewItem{
			Line:        nextStructuredAliasLine(lineNumbers, alias.Alias, i+1),
			Alias:       alias.Alias,
			Destination: alias.Destination,
			Enabled:     enabled,
			Source:      alias.Alias + " " + alias.Destination,
		})
	}
	return items, nil
}

func structuredAliasLineNumbers(body []byte, format string) map[string][]int {
	lines := map[string][]int{}
	patterns := []*regexp.Regexp{}
	if format == "json" {
		patterns = append(patterns, regexp.MustCompile(`"alias"\s*:\s*"([^"]*)"`))
	} else {
		patterns = append(patterns,
			regexp.MustCompile(`^\s*-\s*alias\s*:\s*(.+?)\s*$`),
			regexp.MustCompile(`^\s*alias\s*:\s*(.+?)\s*$`),
		)
	}

	scanner := bufio.NewScanner(bytes.NewReader(body))
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		for _, pattern := range patterns {
			matches := pattern.FindStringSubmatch(line)
			if len(matches) < 2 {
				continue
			}
			alias := cleanStructuredAliasScalar(matches[1])
			if alias == "" {
				continue
			}
			lines[alias] = append(lines[alias], lineNo)
			break
		}
	}
	return lines
}

func nextStructuredAliasLine(lines map[string][]int, alias string, fallback int) int {
	lineNumbers := lines[alias]
	if len(lineNumbers) == 0 {
		return fallback
	}
	line := lineNumbers[0]
	lines[alias] = lineNumbers[1:]
	return line
}

func cleanStructuredAliasScalar(value string) string {
	value = strings.TrimSpace(value)
	if comment := strings.Index(value, " #"); comment >= 0 {
		value = strings.TrimSpace(value[:comment])
	}
	value = strings.TrimSuffix(value, ",")
	if len(value) >= 2 {
		first := value[0]
		last := value[len(value)-1]
		if (first == '\'' && last == '\'') || (first == '"' && last == '"') {
			value = value[1 : len(value)-1]
		}
	}
	return strings.TrimSpace(value)
}

func parseStructuredAliases(body []byte, format string) ([]model.Alias, error) {
	var direct []importAliasInput
	if format == "json" {
		if err := json.Unmarshal(body, &direct); err == nil && len(direct) > 0 {
			return aliasInputsToAliases(direct), nil
		}
	} else {
		if err := yaml.Unmarshal(body, &direct); err == nil && len(direct) > 0 {
			return aliasInputsToAliases(direct), nil
		}
	}

	var directAliases []model.Alias
	if format == "json" {
		if err := json.Unmarshal(body, &directAliases); err == nil && len(directAliases) > 0 {
			return normalizeStructuredAliases(directAliases), nil
		}
	} else {
		if err := yaml.Unmarshal(body, &directAliases); err == nil && len(directAliases) > 0 {
			return normalizeStructuredAliases(directAliases), nil
		}
	}

	var payload importPayload
	if format == "json" {
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil, err
		}
	} else {
		if err := yaml.Unmarshal(body, &payload); err != nil {
			return nil, err
		}
	}

	aliases := make([]model.Alias, 0, len(payload.Aliases)+len(payload.Links))
	aliases = append(aliases, normalizeStructuredAliases(payload.Aliases)...)
	for alias, destination := range payload.Links {
		aliases = append(aliases, model.Alias{
			Alias:       alias,
			Destination: destination,
			Enabled:     model.BoolPtr(true),
		})
	}
	if len(aliases) == 0 {
		var linkMap map[string]string
		if format == "json" {
			if err := json.Unmarshal(body, &linkMap); err == nil && len(linkMap) > 0 {
				return linksMapToAliases(linkMap), nil
			}
		} else {
			if err := yaml.Unmarshal(body, &linkMap); err == nil && len(linkMap) > 0 {
				return linksMapToAliases(linkMap), nil
			}
		}
		return nil, errors.New("no aliases found in payload")
	}
	return aliases, nil
}

func aliasInputsToAliases(inputs []importAliasInput) []model.Alias {
	aliases := make([]model.Alias, 0, len(inputs))
	for _, input := range inputs {
		alias := model.Alias{
			Alias:       input.Alias,
			Destination: input.Destination,
			Enabled:     input.Enabled,
		}
		if alias.Enabled == nil {
			alias.Enabled = model.BoolPtr(true)
		}
		aliases = append(aliases, alias)
	}
	return aliases
}

func normalizeStructuredAliases(inputs []model.Alias) []model.Alias {
	aliases := make([]model.Alias, 0, len(inputs))
	for _, alias := range inputs {
		if alias.Enabled == nil {
			alias.Enabled = model.BoolPtr(true)
		}
		aliases = append(aliases, alias)
	}
	return aliases
}

func linksMapToAliases(links map[string]string) []model.Alias {
	aliases := make([]model.Alias, 0, len(links))
	for alias, destination := range links {
		aliases = append(aliases, model.Alias{
			Alias:       alias,
			Destination: destination,
			Enabled:     model.BoolPtr(true),
		})
	}
	sort.Slice(aliases, func(i, j int) bool {
		return aliases[i].Alias < aliases[j].Alias
	})
	return aliases
}

func importItemError(item importPreviewItem) string {
	if item.Line > 0 && item.Error != "" {
		return "line " + strconv.Itoa(item.Line) + ": " + item.Error
	}
	if item.Error != "" {
		return item.Error
	}
	return "invalid import item"
}

func (c *aliasStatusChecker) statuses(ctx context.Context, aliases []model.Alias) []aliasStatusItem {
	now := time.Now().UTC()
	items := make([]aliasStatusItem, len(aliases))
	type probeRequest struct {
		probeURL string
	}

	requests := map[string]probeRequest{}
	results := map[string]aliasStatusProbe{}
	c.mu.Lock()
	for _, alias := range aliases {
		probeURL := statusProbeURL(alias.Alias, alias.Destination)
		if cached, ok := c.cache[probeURL]; ok && now.Sub(cached.CheckedAt) < c.ttl {
			results[probeURL] = cached
			continue
		}
		requests[probeURL] = probeRequest{probeURL: probeURL}
	}
	c.mu.Unlock()

	if len(requests) > 0 {
		sem := make(chan struct{}, c.maxConcurrent)
		var wg sync.WaitGroup
		var mu sync.Mutex
		for _, req := range requests {
			req := req
			wg.Add(1)
			go func() {
				defer wg.Done()
				select {
				case sem <- struct{}{}:
					defer func() { <-sem }()
				case <-ctx.Done():
					return
				}

				probe := c.check(ctx, req.probeURL)
				mu.Lock()
				results[req.probeURL] = probe
				mu.Unlock()

				c.mu.Lock()
				c.cache[req.probeURL] = probe
				c.mu.Unlock()
			}()
		}
		wg.Wait()
	}

	for i, alias := range aliases {
		effectiveDestination := store.DestinationWithAliasDefaults(alias.Alias, alias.Destination)
		probeURL := statusProbeURL(alias.Alias, alias.Destination)
		probe, ok := results[probeURL]
		if !ok {
			probe = aliasStatusProbe{
				ProbeURL:  probeURL,
				State:     "offline",
				Detail:    "not checked",
				CheckedAt: now,
			}
		}
		if placeholderTokenPattern.MatchString(effectiveDestination) {
			probe = placeholderAwareProbe(effectiveDestination, probe)
		}
		_, refreshed := requests[probeURL]
		items[i] = aliasStatusItem{
			Alias:       alias.Alias,
			Destination: alias.Destination,
			ProbeURL:    probe.ProbeURL,
			State:       probe.State,
			Detail:      probe.Detail,
			StatusCode:  probe.StatusCode,
			CheckedAt:   probe.CheckedAt,
			Cached:      !refreshed,
		}
	}
	return items
}

func placeholderAwareProbe(destination string, probe aliasStatusProbe) aliasStatusProbe {
	if !placeholderTokenPattern.MatchString(destination) || probe.State != "offline" {
		return probe
	}
	probe.State = "warning"
	if probe.StatusCode > 0 {
		probe.Detail = fmt.Sprintf("template destination probe returned HTTP %d", probe.StatusCode)
	} else {
		probe.Detail = "template destination needs sample values"
	}
	return probe
}

func (c *aliasStatusChecker) check(parent context.Context, probeURL string) aliasStatusProbe {
	checkedAt := time.Now().UTC()
	if !isHTTPProbeURL(probeURL) {
		return aliasStatusProbe{
			ProbeURL:  probeURL,
			State:     "warning",
			Detail:    "unsupported destination scheme",
			CheckedAt: checkedAt,
		}
	}

	statusCode, err := c.request(parent, http.MethodHead, probeURL)
	if err != nil {
		statusCode, err = c.request(parent, http.MethodGet, probeURL)
		if err != nil {
			return aliasStatusProbe{
				ProbeURL:  probeURL,
				State:     "offline",
				Detail:    "not reachable",
				CheckedAt: checkedAt,
			}
		}
	}
	if statusCode == http.StatusMethodNotAllowed {
		statusCode, err = c.request(parent, http.MethodGet, probeURL)
		if err != nil {
			return aliasStatusProbe{
				ProbeURL:  probeURL,
				State:     "offline",
				Detail:    "not reachable",
				CheckedAt: checkedAt,
			}
		}
	}

	state, detail := classifyAliasStatus(statusCode)
	return aliasStatusProbe{
		ProbeURL:   probeURL,
		State:      state,
		Detail:     detail,
		StatusCode: statusCode,
		CheckedAt:  checkedAt,
	}
}

func (c *aliasStatusChecker) request(parent context.Context, method string, probeURL string) (int, error) {
	ctx, cancel := context.WithTimeout(parent, c.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, method, probeURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "goku-link-status/1.0")
	if method == http.MethodGet {
		req.Header.Set("Range", "bytes=0-0")
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

func classifyAliasStatus(statusCode int) (string, string) {
	detail := fmt.Sprintf("HTTP %d", statusCode)
	switch {
	case statusCode >= 200 && statusCode < 400:
		return "online", detail
	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		return "online", detail
	case statusCode == http.StatusNotFound:
		return "offline", detail
	case statusCode >= 400:
		return "warning", detail
	default:
		return "warning", detail
	}
}

func statusProbeURL(alias string, destination string) string {
	effectiveDestination := store.DestinationWithAliasDefaults(alias, destination)
	probeURL := strings.TrimSpace(replacePlaceholderTokens(effectiveDestination, ""))
	if parsed, err := url.Parse(probeURL); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		return probeURL
	}
	return store.NormalizeDestination(probeURL)
}

func validateAliasInput(alias string, oldAlias string, destination string, aliases []model.Alias) validationFieldResult {
	result := validationFieldResult{
		State:      "success",
		Message:    "Alias is unique.",
		Normalized: alias,
	}

	if alias != "" {
		if err := store.ValidatePlaceholderSyntax(alias); err != nil {
			result.State = "error"
			result.Message = err.Error()
			return result
		}
	}

	if destination != "" {
		if err := store.ValidateAlias(alias, destination); err != nil {
			result.State = "error"
			result.Message = err.Error()
			return result
		}
	}

	for _, existing := range aliases {
		if existing.Alias == oldAlias {
			continue
		}
		if existing.Alias == alias {
			return validationFieldResult{
				State:      "error",
				Message:    fmt.Sprintf("Alias already exists as %s.", existing.Alias),
				Normalized: alias,
				Matches:    []string{existing.Alias},
			}
		}
		if aliasSameShape(alias, existing.Alias) {
			return validationFieldResult{
				State:      "error",
				Message:    fmt.Sprintf("Alias conflicts with %s.", existing.Alias),
				Normalized: alias,
				Matches:    []string{existing.Alias},
			}
		}
	}

	overlaps := make([]string, 0)
	for _, existing := range aliases {
		if existing.Alias == oldAlias || existing.Alias == alias {
			continue
		}
		if aliasesOverlap(alias, existing.Alias) {
			overlaps = append(overlaps, existing.Alias)
		}
	}
	if len(overlaps) > 0 {
		result.State = "warning"
		result.Message = "Alias may overlap with " + strings.Join(overlaps, ", ") + "."
		result.Matches = overlaps
	}

	return result
}

func validateDestinationInput(ctx context.Context, checker *aliasStatusChecker, oldAlias string, alias string, destination string, aliases []model.Alias) validationFieldResult {
	result := validationFieldResult{
		State:      "success",
		Message:    "Destination is unique and reachable.",
		Normalized: destination,
	}

	if err := validateDestinationFormat(destination); err != nil {
		result.State = "error"
		result.Message = err.Error()
		return result
	}

	matches := make([]string, 0)
	destinationIdentity := normalizedDestinationIdentity(destination)
	for _, existing := range aliases {
		if existing.Alias == oldAlias {
			continue
		}
		existingDestination := store.NormalizeDestination(existing.Destination)
		if destinationIdentity == normalizedDestinationIdentity(existingDestination) {
			matches = append(matches, existing.Alias)
		}
	}
	if len(matches) > 0 {
		result.State = "warning"
		result.Message = "Destination is already used by " + strings.Join(matches, ", ") + "."
		result.Matches = matches
	}

	effectiveDestination := store.DestinationWithAliasDefaults(alias, destination)
	probe := checker.check(ctx, statusProbeURL(alias, destination))
	if placeholderTokenPattern.MatchString(effectiveDestination) {
		probe = placeholderAwareProbe(effectiveDestination, probe)
	}
	if probe.State != "online" {
		result.State = "warning"
		if len(matches) > 0 {
			result.Message += " "
		} else {
			result.Message = ""
		}
		result.Message += "Reachability: " + probe.Detail + "."
		return result
	}
	if len(matches) > 0 {
		return result
	}

	result.Message = "Destination is reachable."
	return result
}

func normalizedDestinationIdentity(destination string) string {
	normalized := store.NormalizeDestination(destination)
	parsed, err := url.Parse(normalized)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return normalized
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	if parsed.Path == "/" {
		parsed.Path = ""
	}
	return parsed.String()
}

func validateDestinationFormat(destination string) error {
	if strings.ContainsAny(destination, " \t\r\n") {
		return fmt.Errorf("destination must be a URL")
	}
	if err := store.ValidateDestinationPlaceholderSyntax(destination); err != nil {
		return err
	}

	candidate := replacePlaceholderTokens(destination, "value")

	parsed, err := url.Parse(candidate)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("destination must be a URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("destination URL scheme must be http or https")
	}
	return nil
}

type aliasSegment struct {
	value       string
	wildcard    bool
	greedy      bool
	description string
}

func aliasSameShape(a string, b string) bool {
	aSegs := parseAliasSegments(a)
	bSegs := parseAliasSegments(b)
	if len(aSegs) != len(bSegs) {
		return false
	}
	for i := range aSegs {
		if aSegs[i].wildcard || bSegs[i].wildcard {
			if !aSegs[i].wildcard || !bSegs[i].wildcard || aSegs[i].greedy != bSegs[i].greedy {
				return false
			}
			continue
		}
		if aSegs[i].value != bSegs[i].value {
			return false
		}
	}
	return true
}

func aliasesOverlap(a string, b string) bool {
	aSegs := parseAliasSegments(a)
	bSegs := parseAliasSegments(b)
	seen := map[string]bool{}
	var walk func(int, int) bool
	walk = func(i int, j int) bool {
		key := fmt.Sprintf("%d:%d", i, j)
		if seen[key] {
			return false
		}
		seen[key] = true

		if i == len(aSegs) && j == len(bSegs) {
			return true
		}
		if i < len(aSegs) && aSegs[i].greedy {
			if walk(i+1, j) {
				return true
			}
			if j < len(bSegs) && walk(i, j+1) {
				return true
			}
		}
		if j < len(bSegs) && bSegs[j].greedy {
			if walk(i, j+1) {
				return true
			}
			if i < len(aSegs) && walk(i+1, j) {
				return true
			}
		}
		if i == len(aSegs) || j == len(bSegs) {
			return false
		}
		if !aliasSegmentsCompatible(aSegs[i], bSegs[j]) {
			return false
		}
		return walk(i+1, j+1)
	}
	return walk(0, 0)
}

func parseAliasSegments(alias string) []aliasSegment {
	parts := strings.Split(strings.Trim(alias, "/"), "/")
	if len(parts) == 1 && parts[0] == "" {
		return nil
	}
	segments := make([]aliasSegment, 0, len(parts))
	for _, part := range parts {
		body := strings.TrimSuffix(strings.TrimPrefix(part, "{"), "}")
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			segments = append(segments, aliasSegment{
				value:       body,
				wildcard:    true,
				greedy:      strings.HasSuffix(body, "..."),
				description: part,
			})
			continue
		}
		segments = append(segments, aliasSegment{value: part, description: part})
	}
	return segments
}

func aliasSegmentsCompatible(a aliasSegment, b aliasSegment) bool {
	if a.wildcard || b.wildcard {
		return true
	}
	return a.value == b.value
}

func isHTTPProbeURL(probeURL string) bool {
	parsed, err := url.Parse(probeURL)
	if err != nil {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

func stripPlaceholderValues(alias string, destination string) string {
	effectiveDestination := store.DestinationWithAliasDefaults(alias, destination)
	return replacePlaceholderTokens(effectiveDestination, "")
}

func destinationHref(alias string, destination string) string {
	effectiveDestination := store.DestinationWithAliasDefaults(alias, destination)
	href := store.NormalizeDestination(replacePlaceholderTokens(effectiveDestination, ""))
	parsed, err := url.Parse(href)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return href
	}

	host := strings.Trim(parsed.Hostname(), ".")
	for strings.Contains(host, "..") {
		host = strings.ReplaceAll(host, "..", ".")
	}
	if host == "" {
		return href
	}
	if port := parsed.Port(); port != "" {
		host = net.JoinHostPort(host, port)
	}
	parsed.Host = host

	if parsed.Path != "" {
		trailingSlash := strings.HasSuffix(parsed.Path, "/")
		parsed.Path = path.Clean(parsed.Path)
		if parsed.Path == "." {
			parsed.Path = ""
		}
		if trailingSlash && parsed.Path != "/" {
			parsed.Path += "/"
		}
	}

	return parsed.String()
}

func replacePlaceholderTokens(destination string, fallback string) string {
	return placeholderTokenPattern.ReplaceAllStringFunc(destination, func(token string) string {
		if value, ok := placeholderDefaultValue(token); ok {
			return value
		}
		return fallback
	})
}

func placeholderDefaultValue(token string) (string, bool) {
	if len(token) < 2 || !strings.HasPrefix(token, "{") || !strings.HasSuffix(token, "}") {
		return "", false
	}
	body := strings.TrimSuffix(strings.TrimPrefix(token, "{"), "}")
	_, value, ok := strings.Cut(body, ":=")
	if !ok {
		return "", false
	}
	return strings.TrimSpace(value), true
}
