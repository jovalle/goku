package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jovalle/goku/internal/metrics"
)

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// LoggingMiddleware logs every request with method, path, status, and duration.
func LoggingMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec := &statusRecorder{ResponseWriter: w, status: 200}
			start := time.Now()

			next.ServeHTTP(rec, r)

			logger.Info("request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"duration", time.Since(start),
				"remote", r.RemoteAddr,
			)
		})
	}
}

// RecoveryMiddleware catches panics and returns 500.
func RecoveryMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					logger.Error("panic",
						"error", err,
						"path", r.URL.Path,
					)
					http.Error(w, "internal server error", http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// RequestIDMiddleware attaches a unique request ID.
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			b := make([]byte, 8)
			_, _ = rand.Read(b)
			id = hex.EncodeToString(b)
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r)
	})
}

// MetricsMiddleware records request duration in Prometheus.
func MetricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: 200}

		next.ServeHTTP(rec, r)

		duration := time.Since(start).Seconds()
		status := strconv.Itoa(rec.status)
		metrics.RequestDuration.WithLabelValues(r.Method, status).Observe(duration)
	})
}

// authEnabled returns true if any auth credentials are configured.
func (s *Server) authEnabled() bool {
	return s.auth.APIKey != "" || s.auth.Password != ""
}

// requireAuth wraps a handler so it requires authentication.
// If no credentials are configured, the handler is returned unwrapped.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	if !s.authEnabled() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.checkAuth(w, r) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

// checkAuth validates the request has proper credentials.
// Returns true if authenticated (or auth is disabled).
// If not authenticated, it writes an appropriate response and returns false.
//
// Supports:
//   - Bearer token:  Authorization: Bearer <api-key>
//   - Basic auth:    Authorization: Basic <base64(username:password)>
func (s *Server) checkAuth(w http.ResponseWriter, r *http.Request) bool {
	if !s.authEnabled() {
		return true
	}

	// Try Bearer token (matches API key)
	if auth := r.Header.Get("Authorization"); auth != "" {
		if strings.HasPrefix(auth, "Bearer ") {
			token := strings.TrimPrefix(auth, "Bearer ")
			if s.auth.APIKey != "" && subtle.ConstantTimeCompare([]byte(token), []byte(s.auth.APIKey)) == 1 {
				return true
			}
		}
	}

	// Try Basic auth (matches admin username + password)
	if s.auth.Password != "" {
		user, pass, ok := r.BasicAuth()
		userOK := s.auth.Username == "" || subtle.ConstantTimeCompare([]byte(user), []byte(s.auth.Username)) == 1
		passOK := subtle.ConstantTimeCompare([]byte(pass), []byte(s.auth.Password)) == 1
		if ok && userOK && passOK {
			return true
		}
	}

	// No valid credentials — prompt Basic Auth for browsers, 401 for API
	w.Header().Set("WWW-Authenticate", `Basic realm="goku"`)
	http.Error(w, "unauthorized", http.StatusUnauthorized)
	return false
}
