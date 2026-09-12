// @title       goku API
// @version     1.0
// @description Alias URL shortener service.
//
// @host     localhost:9001
// @BasePath /

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
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
	apiPort := getEnv("GOKU_API_PORT", "9000")
	adminPort := getEnv("GOKU_ADMIN_PORT", getEnv("GOKU_WEB_PORT", "9001"))
	apiAddr := ":" + apiPort
	adminAddr := ":" + adminPort
	if apiAddr == adminAddr {
		return fmt.Errorf("GOKU_API_PORT and GOKU_ADMIN_PORT must be different")
	}
	configPath := getEnv("GOKU_CONFIG", "config/config.yaml")

	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	logger.Info("config loaded", "aliases", len(cfg.Aliases))

	s := store.New(cfg)
	metrics.Register(func() float64 { return float64(len(s.Aliases())) })

	auth := server.AuthConfig{
		Username: getEnv("GOKU_ADMIN_USERNAME", "admin"),
		Password: getEnv("GOKU_ADMIN_PASSWORD", ""),
		APIKey:   getEnv("GOKU_API_KEY", ""),
	}

	auth.APIKey, err = loadAPIKey(configPath, auth.APIKey, logger)
	if err != nil {
		return err
	}

	if auth.Password == "" {
		logger.Warn("GOKU_ADMIN_PASSWORD not set - admin UI login is disabled")
	}

	publicSrv := server.NewPublic(s, logger)
	adminSrv := server.NewAdmin(s, logger, configPath, auth)
	adminSrv.SetPublicBaseURL(getEnv("GOKU_PUBLIC_BASE_URL", ""))

	apiHTTPServer := &http.Server{
		Addr:              apiAddr,
		Handler:           publicSrv,
		ReadTimeout:       5 * time.Second,
		ReadHeaderTimeout: 2 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	adminHTTPServer := &http.Server{
		Addr:              adminAddr,
		Handler:           adminSrv,
		ReadTimeout:       5 * time.Second,
		ReadHeaderTimeout: 2 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	g, gctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		logger.Info("starting goku public endpoint",
			"addr", apiAddr,
			"version", version,
			"commit", commit,
		)
		if err := apiHTTPServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	})

	g.Go(func() error {
		logger.Info("starting goku admin endpoint",
			"addr", adminAddr,
			"version", version,
			"commit", commit,
		)
		if err := adminHTTPServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
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
		return shutdownAll(shutdownCtx, apiHTTPServer.Shutdown, adminHTTPServer.Shutdown)
	})

	return g.Wait()
}

func loadAPIKey(configPath, configured string, logger *slog.Logger) (string, error) {
	if configured != "" {
		logger.Info("using API key from environment")
		return configured, nil
	}

	keyPath := filepath.Join(filepath.Dir(configPath), ".api_key")
	data, err := os.ReadFile(keyPath)
	if err == nil {
		if key := strings.TrimSpace(string(data)); key != "" {
			logger.Info("using API key from file", "path", keyPath)
			return key, nil
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("reading API key file: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(keyPath), 0755); err != nil {
		return "", fmt.Errorf("creating config directory: %w", err)
	}
	keyBytes := make([]byte, 24)
	if _, err := rand.Read(keyBytes); err != nil {
		return "", fmt.Errorf("generating API key: %w", err)
	}
	key := hex.EncodeToString(keyBytes)
	if err := os.WriteFile(keyPath, []byte(key+"\n"), 0600); err != nil {
		return "", fmt.Errorf("saving API key file: %w", err)
	}
	if err := os.Chmod(keyPath, 0600); err != nil {
		return "", fmt.Errorf("securing API key file: %w", err)
	}
	logger.Info("generated API key; read it from the key file",
		"path", keyPath,
		"permissions", "0600",
	)
	return key, nil
}

func shutdownAll(ctx context.Context, shutdowns ...func(context.Context) error) error {
	errs := make([]error, len(shutdowns))
	var wg sync.WaitGroup
	for i, shutdown := range shutdowns {
		wg.Go(func() {
			errs[i] = shutdown(ctx)
		})
	}
	wg.Wait()
	return errors.Join(errs...)
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}
