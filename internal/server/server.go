package server

import (
	"log/slog"
	"net/http"

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
	handler    http.Handler
	mux        *http.ServeMux
}

// New creates a Server and wires all routes and middleware.
func New(s *store.LinkStore, logger *slog.Logger, configPath string, auth AuthConfig) *Server {
	srv := &Server{
		store:      s,
		logger:     logger,
		configPath: configPath,
		auth:       auth,
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
	// Public: health check (for load balancers) and redirects
	s.mux.HandleFunc("GET /healthz", s.handleHealth)

	// Protected: metrics, UI, and API
	protected := s.requireAuth
	s.mux.Handle("GET /metrics", protected(promhttp.Handler()))
	s.mux.Handle("GET /api/links", protected(http.HandlerFunc(s.handleListLinks)))
	s.mux.Handle("POST /api/links", protected(http.HandlerFunc(s.handleAddLink)))
	s.mux.Handle("POST /api/links/delete", protected(http.HandlerFunc(s.handleDeleteLink)))
	s.mux.Handle("POST /api/links/{name}/delete", protected(http.HandlerFunc(s.handleDeleteLink)))
	s.mux.Handle("POST /api/rules", protected(http.HandlerFunc(s.handleAddRule)))
	s.mux.Handle("POST /api/rules/delete", protected(http.HandlerFunc(s.handleDeleteRule)))
	s.mux.Handle("POST /api/rules/{name}/delete", protected(http.HandlerFunc(s.handleDeleteRule)))

	// Root: UI (protected) or redirect (public)
	s.mux.HandleFunc("GET /{path...}", s.handleRoot)
}

func chain(h http.Handler, mws ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}
