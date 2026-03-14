package config

import (
	"context"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/jovalle/goku/internal/model"
)

// Updater is called when the config file changes.
type Updater interface {
	Update(cfg model.Config)
}

// Watch starts watching the config file for changes.
// It reloads the config and calls updater.Update() on change.
// It blocks until ctx is cancelled.
func Watch(ctx context.Context, path string, updater Updater, logger *slog.Logger) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	// Watch the directory to catch editor rename-based saves (Vim, VS Code)
	dir := filepath.Dir(path)
	if err := watcher.Add(dir); err != nil {
		return err
	}

	logger.Info("watching config", "path", path)

	absPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	var debounceTimer *time.Timer

	for {
		select {
		case <-ctx.Done():
			logger.Info("config watcher stopped")
			return nil

		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}

			eventAbs, _ := filepath.Abs(event.Name)
			if eventAbs != absPath {
				continue
			}

			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
				if debounceTimer != nil {
					debounceTimer.Stop()
				}
				debounceTimer = time.AfterFunc(100*time.Millisecond, func() {
					reload(path, updater, logger)
				})
			}

		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			logger.Error("watcher error", "error", err)
		}
	}
}

func reload(path string, updater Updater, logger *slog.Logger) {
	cfg, err := Load(path)
	if err != nil {
		logger.Error("config reload failed - keeping old config",
			"path", path,
			"error", err,
		)
		return
	}

	updater.Update(cfg)
	logger.Info("config reloaded",
		"links", len(cfg.Links),
		"rules", len(cfg.Rules),
	)
}
