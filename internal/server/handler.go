package server

import (
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"sort"
	"strings"
	"time"

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

// uiData is the data passed to the HTML template.
type uiData struct {
	LinkCount int
	RuleCount int
	Links     []linkEntry
	Rules     []model.Rule
}

type linkEntry struct {
	Name string
	URL  string
}

type healthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Links   int    `json:"links"`
	Rules   int    `json:"rules"`
	Uptime  string `json:"uptime"`
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	path := r.PathValue("path")
	if path == "" {
		// UI requires auth; redirects do not
		if !s.checkAuth(w, r) {
			return
		}
		s.handleUI(w, r)
		return
	}
	s.handleRedirect(w, r)
}

func (s *Server) handleRedirect(w http.ResponseWriter, r *http.Request) {
	path := r.PathValue("path")

	url, err := s.store.Resolve(path)
	if err != nil {
		if errors.Is(err, resolve.ErrNotFound) {
			http.NotFound(w, r)
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

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	resp := healthResponse{
		Status:  "ok",
		Version: Version,
		Commit:  Commit,
		Links:   len(s.store.Links()),
		Rules:   len(s.store.Rules()),
		Uptime:  time.Since(startTime).Round(time.Second).String(),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleListLinks(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.store.Links())
}

func (s *Server) handleAddLink(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	urlVal := r.FormValue("url")

	if name == "" || urlVal == "" {
		http.Error(w, "name and url required", http.StatusBadRequest)
		return
	}

	cfg := s.store.AddLink(name, urlVal)
	if err := config.Save(s.configPath, cfg); err != nil {
		s.logger.Error("failed to save config", "error", err)
		http.Error(w, "failed to save", http.StatusInternalServerError)
		return
	}

	metrics.LinksTotal.Set(float64(len(cfg.Links)))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleDeleteLink(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	cfg := s.store.DeleteLink(name)
	if err := config.Save(s.configPath, cfg); err != nil {
		s.logger.Error("failed to save config", "error", err)
		http.Error(w, "failed to save", http.StatusInternalServerError)
		return
	}

	metrics.LinksTotal.Set(float64(len(cfg.Links)))
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

	if rule.Type != "prefix" && rule.Type != "template" {
		http.Error(w, "type must be prefix or template", http.StatusBadRequest)
		return
	}

	cfg := s.store.AddRule(rule)
	if err := config.Save(s.configPath, cfg); err != nil {
		s.logger.Error("failed to save config", "error", err)
		http.Error(w, "failed to save", http.StatusInternalServerError)
		return
	}

	metrics.RulesTotal.Set(float64(len(cfg.Rules)))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleDeleteRule(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	cfg := s.store.DeleteRule(name)
	if err := config.Save(s.configPath, cfg); err != nil {
		s.logger.Error("failed to save config", "error", err)
		http.Error(w, "failed to save", http.StatusInternalServerError)
		return
	}

	metrics.RulesTotal.Set(float64(len(cfg.Rules)))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleUI(w http.ResponseWriter, r *http.Request) {
	links := s.store.Links()
	rules := s.store.Rules()

	entries := make([]linkEntry, 0, len(links))
	for name, url := range links {
		entries = append(entries, linkEntry{Name: name, URL: url})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name < entries[j].Name
	})

	data := uiData{
		LinkCount: len(links),
		RuleCount: len(rules),
		Links:     entries,
		Rules:     rules,
	}

	tmpl, err := template.ParseFS(ui.Templates, "templates/base.html")
	if err != nil {
		s.logger.Error("template parse", "error", err)
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.Execute(w, data); err != nil {
		s.logger.Error("template render", "error", err)
	}
}
