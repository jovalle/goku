package server

import (
	"log/slog"
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/jovalle/goku/internal/store"
)

// Server is the goku HTTP server.
type Server struct {
	store      *store.LinkStore
	logger     *slog.Logger
	configPath string
	handler    http.Handler
	mux        *http.ServeMux
}

// New creates a Server and wires all routes and middleware.
func New(s *store.LinkStore, logger *slog.Logger, configPath string) *Server {
	srv := &Server{
		store:      s,
		logger:     logger,
		configPath: configPath,
		mux:        http.NewServeMux(),
	}
	srv.routes()

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
	s.mux.HandleFunc("GET /healthz", s.handleHealth)
	s.mux.Handle("GET /metrics", promhttp.Handler())
	s.mux.HandleFunc("GET /api/links", s.handleListLinks)
	s.mux.HandleFunc("POST /api/links", s.handleAddLink)
	s.mux.HandleFunc("POST /api/links/{name}/delete", s.handleDeleteLink)
	s.mux.HandleFunc("POST /api/rules", s.handleAddRule)
	s.mux.HandleFunc("POST /api/rules/{name}/delete", s.handleDeleteRule)
	s.mux.HandleFunc("GET /{path...}", s.handleRoot)
}

func chain(h http.Handler, mws ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}
