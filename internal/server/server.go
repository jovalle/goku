package server

import (
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strings"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/jovalle/goku/internal/store"
)

// AuthConfig holds authentication credentials.
type AuthConfig struct {
	Username string
	Password string
	APIKey   string
}

// Server is the goku HTTP server.
type Server struct {
	store      *store.LinkStore
	logger     *slog.Logger
	configPath string
	auth       AuthConfig
	publicBase string
	mode       string
	handler    http.Handler
	mux        *http.ServeMux
}

const (
	modeCombined = "combined"
	modeAdmin    = "admin"
	modePublic   = "public"

	adminSessionCookieName = "goku_admin_session"
)

// New creates a backward-compatible single server (admin + public routes).
func New(s *store.LinkStore, logger *slog.Logger, configPath string, auth AuthConfig) *Server {
	return newWithMode(s, logger, configPath, auth, modeCombined)
}

// NewAdmin creates an admin-only server (UI + CRUD API).
func NewAdmin(s *store.LinkStore, logger *slog.Logger, configPath string, auth AuthConfig) *Server {
	return newWithMode(s, logger, configPath, auth, modeAdmin)
}

// NewPublic creates a public server (landing + redirects + health).
func NewPublic(s *store.LinkStore, logger *slog.Logger) *Server {
	return newWithMode(s, logger, "", AuthConfig{}, modePublic)
}

// SetPublicBaseURL configures absolute links from admin pages to public endpoints.
func (s *Server) SetPublicBaseURL(raw string) {
	s.publicBase = strings.TrimRight(strings.TrimSpace(raw), "/")
}

func newWithMode(s *store.LinkStore, logger *slog.Logger, configPath string, auth AuthConfig, mode string) *Server {
	resetBrokenLinks()

	srv := &Server{
		store:      s,
		logger:     logger,
		configPath: configPath,
		auth:       auth,
		mode:       mode,
		mux:        http.NewServeMux(),
	}
	srv.routes()

	// Build the middleware chain once (outermost runs first)
	srv.handler = chain(srv.mux,
		RecoveryMiddleware(logger),
		LoggingMiddleware(logger),
		RequestIDMiddleware,
		MetricsMiddleware,
	)

	return srv
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

func (s *Server) routes() {
	// Shared endpoints.
	s.mux.HandleFunc("GET /healthz", s.handleHealth)
	s.mux.HandleFunc("GET /static/logo.png", s.handleLogo)

	switch s.mode {
	case modePublic:
		s.mux.HandleFunc("GET /ws/health", s.handleHealthWebSocket)
		s.mux.HandleFunc("GET /preview", s.handleAliasPreview)
		s.mux.HandleFunc("GET /{$}", s.handlePublicHome)
		s.mux.HandleFunc("GET /{path...}", s.handleRedirect)
		return

	case modeAdmin:
		s.mux.Handle("GET /metrics", promhttp.Handler())
		s.mux.HandleFunc("GET /{$}", s.handleAdminHome)
		s.mux.HandleFunc("GET /login", s.handleLoginPage)
		s.mux.HandleFunc("POST /login", s.handleLogin)
		s.mux.HandleFunc("POST /logout", s.handleLogout)
		s.mux.HandleFunc("GET /swagger", s.handleSwagger)
		s.mux.HandleFunc("GET /openapi.json", s.handleOpenAPI)
		s.mux.Handle("GET /api/aliases", s.requireAPIAuth(http.HandlerFunc(s.handleListAliases)))
		s.mux.Handle("POST /api/aliases", s.requireAPIAuth(http.HandlerFunc(s.handleAddAlias)))
		s.mux.Handle("POST /api/aliases/delete", s.requireAPIAuth(http.HandlerFunc(s.handleDeleteAlias)))
		s.mux.Handle("POST /api/aliases/{alias}/delete", s.requireAPIAuth(http.HandlerFunc(s.handleDeleteAlias)))
		s.mux.Handle("POST /api/aliases/edit", s.requireAPIAuth(http.HandlerFunc(s.handleEditAlias)))
		s.mux.Handle("POST /api/aliases/toggle", s.requireAPIAuth(http.HandlerFunc(s.handleToggleAlias)))
		s.mux.Handle("POST /api/import", s.requireAPIAuth(http.HandlerFunc(s.handleBatchImport)))
		s.mux.Handle("GET /api/broken-links", s.requireAPIAuth(http.HandlerFunc(s.handleBrokenLinks)))
		s.mux.Handle("GET /api/links", s.requireAPIAuth(http.HandlerFunc(s.handleListLinks)))
		s.mux.Handle("POST /api/links", s.requireAPIAuth(http.HandlerFunc(s.handleAddLink)))
		s.mux.Handle("POST /api/links/delete", s.requireAPIAuth(http.HandlerFunc(s.handleDeleteLink)))
		s.mux.Handle("POST /api/links/{name}/delete", s.requireAPIAuth(http.HandlerFunc(s.handleDeleteLink)))
		s.mux.Handle("POST /api/rules", s.requireAPIAuth(http.HandlerFunc(s.handleAddRule)))
		s.mux.Handle("POST /api/rules/delete", s.requireAPIAuth(http.HandlerFunc(s.handleDeleteRule)))
		s.mux.Handle("POST /api/rules/{name}/delete", s.requireAPIAuth(http.HandlerFunc(s.handleDeleteRule)))
		s.mux.HandleFunc("GET /{path...}", s.handleNotFound)
		return

	default:
		// Combined server for backward compatibility.
		s.mux.HandleFunc("GET /ws/health", s.handleHealthWebSocket)
		s.mux.HandleFunc("GET /preview", s.handleAliasPreview)
		s.mux.Handle("GET /metrics", promhttp.Handler())
		s.mux.HandleFunc("GET /{$}", s.handleAdminHome)
		s.mux.HandleFunc("GET /login", s.handleLoginPage)
		s.mux.HandleFunc("POST /login", s.handleLogin)
		s.mux.HandleFunc("POST /logout", s.handleLogout)
		s.mux.HandleFunc("GET /swagger", s.handleSwagger)
		s.mux.HandleFunc("GET /openapi.json", s.handleOpenAPI)
		s.mux.Handle("GET /api/aliases", s.requireAPIAuth(http.HandlerFunc(s.handleListAliases)))
		s.mux.Handle("POST /api/aliases", s.requireAPIAuth(http.HandlerFunc(s.handleAddAlias)))
		s.mux.Handle("POST /api/aliases/delete", s.requireAPIAuth(http.HandlerFunc(s.handleDeleteAlias)))
		s.mux.Handle("POST /api/aliases/{alias}/delete", s.requireAPIAuth(http.HandlerFunc(s.handleDeleteAlias)))
		s.mux.Handle("POST /api/aliases/edit", s.requireAPIAuth(http.HandlerFunc(s.handleEditAlias)))
		s.mux.Handle("POST /api/aliases/toggle", s.requireAPIAuth(http.HandlerFunc(s.handleToggleAlias)))
		s.mux.Handle("POST /api/import", s.requireAPIAuth(http.HandlerFunc(s.handleBatchImport)))
		s.mux.Handle("GET /api/broken-links", s.requireAPIAuth(http.HandlerFunc(s.handleBrokenLinks)))
		s.mux.Handle("GET /api/links", s.requireAPIAuth(http.HandlerFunc(s.handleListLinks)))
		s.mux.Handle("POST /api/links", s.requireAPIAuth(http.HandlerFunc(s.handleAddLink)))
		s.mux.Handle("POST /api/links/delete", s.requireAPIAuth(http.HandlerFunc(s.handleDeleteLink)))
		s.mux.Handle("POST /api/links/{name}/delete", s.requireAPIAuth(http.HandlerFunc(s.handleDeleteLink)))
		s.mux.Handle("POST /api/rules", s.requireAPIAuth(http.HandlerFunc(s.handleAddRule)))
		s.mux.Handle("POST /api/rules/delete", s.requireAPIAuth(http.HandlerFunc(s.handleDeleteRule)))
		s.mux.Handle("POST /api/rules/{name}/delete", s.requireAPIAuth(http.HandlerFunc(s.handleDeleteRule)))
		s.mux.HandleFunc("GET /{path...}", s.handleRedirectOr404)
	}
}

func (s *Server) adminSessionToken() string {
	secret := s.auth.Password
	if secret == "" {
		secret = s.auth.APIKey
	}
	b := sha256.Sum256([]byte(s.auth.Username + ":" + secret + ":goku"))
	return hex.EncodeToString(b[:])
}

func chain(h http.Handler, mws ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}
