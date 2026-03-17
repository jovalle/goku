// @title       goku API
// @version     1.0
// @description Golinks URL shortener service.
//
// @host     localhost:9001
// @BasePath /

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/jovalle/goku/internal/config"
	"github.com/jovalle/goku/internal/metrics"
	"github.com/jovalle/goku/internal/server"
	"github.com/jovalle/goku/internal/store"
)

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	server.Version = version
	server.Commit = commit
	server.Date = date

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, logger); err != nil {
		logger.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger) error {
	port := getEnv("GOKU_WEB_PORT", "9001")
	addr := ":" + port
	configPath := getEnv("GOKU_CONFIG", "config/config.yaml")

	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	logger.Info("config loaded", "links", len(cfg.Links), "rules", len(cfg.Rules))

	metrics.Register()
	metrics.LinksTotal.Set(float64(len(cfg.Links)))
	metrics.RulesTotal.Set(float64(len(cfg.Rules)))

	auth := server.AuthConfig{
		Username: getEnv("GOKU_ADMIN_USERNAME", "admin"),
		Password: getEnv("GOKU_ADMIN_PASSWORD", ""),
		APIKey:   getEnv("GOKU_API_KEY", ""),
	}

	// Priority: env var > key file > generate new key
	keyPath := filepath.Join(filepath.Dir(configPath), ".api_key")
	switch {
	case auth.APIKey != "":
		logger.Info("using API key from environment")
	default:
		if data, err := os.ReadFile(keyPath); err == nil {
			if key := strings.TrimSpace(string(data)); key != "" {
				auth.APIKey = key
				logger.Info("using API key from file", "path", keyPath)
			}
		}
		if auth.APIKey == "" {
			b := make([]byte, 24)
			if _, err := rand.Read(b); err != nil {
				return fmt.Errorf("generating API key: %w", err)
			}
			auth.APIKey = hex.EncodeToString(b)
			if err := os.WriteFile(keyPath, []byte(auth.APIKey+"\n"), 0600); err != nil {
				return fmt.Errorf("saving API key file: %w", err)
			}
			logger.Info("generated and saved new API key", "path", keyPath)
		}
	}
	logger.Info("API key", "key", auth.APIKey)

	if auth.Password == "" {
		logger.Warn("GOKU_ADMIN_PASSWORD not set - UI and API are unprotected")
	}

	s := store.New(cfg)
	srv := server.New(s, logger, configPath, auth)

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           srv,
		ReadTimeout:       5 * time.Second,
		ReadHeaderTimeout: 2 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	g, gctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		logger.Info("starting goku",
			"addr", addr,
			"version", version,
			"commit", commit,
		)
		if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
			return err
		}
		return nil
	})

	g.Go(func() error {
		return config.Watch(gctx, configPath, s, logger)
	})

	g.Go(func() error {
		<-gctx.Done()
		logger.Info("shutting down...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	})

	return g.Wait()
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}
