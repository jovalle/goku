package server

import (
	"cmp"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"net/http"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"gopkg.in/yaml.v3"

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

const maxImportBodyBytes int64 = 1 << 20

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
	Line        int    `json:"line,omitzero"`
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
	StatusCode  int       `json:"status_code,omitzero"`
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
	slices.SortFunc(aliases, func(a, b model.Alias) int {
		return cmp.Compare(a.Alias, b.Alias)
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
	r.Body = http.MaxBytesReader(w, r.Body, maxImportBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
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
	aliases := make([]model.Alias, 0, preview.ValidCount)
	for _, item := range preview.Items {
		if !item.Valid {
			resp.Errors = append(resp.Errors, importItemError(item))
			continue
		}
		aliases = append(aliases, model.Alias{
			Alias:       item.Alias,
			Destination: item.Destination,
			Enabled:     new(item.Enabled),
		})
	}

	if len(aliases) > 0 {
		cfg, err := s.store.UpsertAliases(aliases)
		if err != nil {
			if errors.Is(err, store.ErrPersistence) {
				s.writeMutationError(w, err)
				return
			}
			resp.Errors = append(resp.Errors, err.Error())
		} else {
			resp.ImportedAliases = len(aliases)
			resp.TotalAliases = len(cfg.Aliases)
		}
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

func (s *Server) writeMutationError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrPersistence) {
		s.logger.Error("failed to save config", "error", err)
		http.Error(w, "failed to save", http.StatusInternalServerError)
		return
	}
	http.Error(w, err.Error(), http.StatusBadRequest)
}

func (s *Server) handleImportPreview(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxImportBodyBytes)
	var req importPreviewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
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

	_, err := s.store.AddAlias(alias, destination)
	if err != nil {
		s.writeMutationError(w, err)
		return
	}

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
		s.writeMutationError(w, err)
		return
	}

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
		s.writeMutationError(w, err)
		return
	}

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

	cfg, err := s.store.DeleteAlias(alias)
	if err != nil {
		s.writeMutationError(w, err)
		return
	}

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

	_, err := s.store.DeleteAliases(aliases)
	if err != nil {
		s.writeMutationError(w, err)
		return
	}

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

func (s *Server) handleLogoDark(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(ui.LogoDark)
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
