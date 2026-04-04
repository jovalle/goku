package server

import (
	"bufio"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"log/slog"
	"net"
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

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("hijacker not supported")
	}
	return h.Hijack()
}

func (r *statusRecorder) Push(target string, opts *http.PushOptions) error {
	p, ok := r.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return p.Push(target, opts)
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

// authEnabled returns true when API protection is enabled.
// Password enables admin UI login; API key enables bearer auth for admin APIs.
func (s *Server) authEnabled() bool {
	return s.auth.Password != "" || s.auth.APIKey != ""
}

func (s *Server) uiAuthEnabled() bool {
	return s.auth.Password != ""
}

// requireAuth keeps backward compatibility and maps to API auth behavior.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return s.requireAPIAuth(next)
}

// requireAPIAuth enforces authentication for admin API endpoints.
func (s *Server) requireAPIAuth(next http.Handler) http.Handler {
	if !s.authEnabled() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.checkAPIAuth(w, r) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

// checkAuth validates UI credentials (legacy behavior for tests/helpers).
func (s *Server) checkAuth(w http.ResponseWriter, r *http.Request) bool {
	if !s.uiAuthEnabled() {
		return true
	}
	if s.validSession(r) || s.validBasic(r) || s.validBearer(r) {
		return true
	}
	w.Header().Set("WWW-Authenticate", `Basic realm="goku"`)
	http.Error(w, "unauthorized", http.StatusUnauthorized)
	return false
}

func (s *Server) checkAPIAuth(w http.ResponseWriter, r *http.Request) bool {
	if !s.authEnabled() {
		return true
	}

	if s.validBearer(r) || s.validSession(r) || s.validBasic(r) {
		return true
	}

	if s.uiAuthEnabled() {
		w.Header().Set("WWW-Authenticate", `Basic realm="goku"`)
	}
	http.Error(w, "unauthorized", http.StatusUnauthorized)
	return false
}

func (s *Server) validBearer(r *http.Request) bool {
	if s.auth.APIKey == "" {
		return false
	}
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return false
	}
	token := strings.TrimPrefix(auth, "Bearer ")
	return subtle.ConstantTimeCompare([]byte(token), []byte(s.auth.APIKey)) == 1
}

func (s *Server) validBasic(r *http.Request) bool {
	if s.auth.Password == "" {
		return false
	}
	user, pass, ok := r.BasicAuth()
	if !ok {
		return false
	}
	userOK := s.auth.Username == "" || subtle.ConstantTimeCompare([]byte(user), []byte(s.auth.Username)) == 1
	passOK := subtle.ConstantTimeCompare([]byte(pass), []byte(s.auth.Password)) == 1
	return userOK && passOK
}

func (s *Server) validSession(r *http.Request) bool {
	if !s.authEnabled() {
		return false
	}
	c, err := r.Cookie(adminSessionCookieName)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(c.Value), []byte(s.adminSessionToken())) == 1
}
