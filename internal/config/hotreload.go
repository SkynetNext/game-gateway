package config

import (
	"context"
	"sync"
	"time"
)

// HotReloadManager manages hot reloading of configuration
type HotReloadManager struct {
	config     *Config
	mu         sync.RWMutex
	reloadFunc func(*Config) error
}

// NewHotReloadManager creates a new hot reload manager
func NewHotReloadManager(initialConfig *Config, reloadFunc func(*Config) error) *HotReloadManager {
	return &HotReloadManager{
		config:     initialConfig,
		reloadFunc: reloadFunc,
	}
}

// GetConfig returns the current configuration (thread-safe)
func (h *HotReloadManager) GetConfig() *Config {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.config
}

// UpdateConfig updates the configuration (thread-safe)
func (h *HotReloadManager) UpdateConfig(newConfig *Config) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Validate new configuration
	if err := validateConfig(newConfig); err != nil {
		return err
	}

	// Call reload function if provided
	if h.reloadFunc != nil {
		if err := h.reloadFunc(newConfig); err != nil {
			return err
		}
	}

	// Update configuration
	h.config = newConfig
	return nil
}

// WatchConfigFile watches for configuration file changes and reloads
// This is a placeholder for future implementation (e.g., using fsnotify)
func (h *HotReloadManager) WatchConfigFile(ctx context.Context, configPath string, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			// Reload configuration from file
			newConfig, err := Load(configPath)
			if err != nil {
				// Log error but continue with existing config
				continue
			}

			// Update configuration if changed
			if err := h.UpdateConfig(newConfig); err != nil {
				// Log error but continue with existing config
				continue
			}
		}
	}
}
