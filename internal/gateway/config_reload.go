package gateway

import (
	"fmt"

	"github.com/SkynetNext/game-gateway/internal/config"
	"github.com/SkynetNext/game-gateway/internal/logger"
	"github.com/SkynetNext/game-gateway/internal/ratelimit"
	"go.uber.org/zap"
)

// UpdateConfig updates the gateway configuration (hot reload)
// This allows updating certain configuration values without restarting the gateway
func (g *Gateway) UpdateConfig(newConfig *config.Config) error {
	// Validate new configuration
	if err := config.ValidateConfig(newConfig); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	g.configMu.Lock()
	defer g.configMu.Unlock()

	// Update rate limiter if max connections changed
	if newConfig.ConnectionPool.MaxConnections != g.config.ConnectionPool.MaxConnections {
		g.rateLimiter = ratelimit.NewLimiter(int64(newConfig.ConnectionPool.MaxConnections))
		logger.L.Info("rate limiter updated",
			zap.Int("old_max", g.config.ConnectionPool.MaxConnections),
			zap.Int("new_max", newConfig.ConnectionPool.MaxConnections),
		)
	}

	// Update IP limiter if security settings changed
	if newConfig.Security.MaxConnectionsPerIP != g.config.Security.MaxConnectionsPerIP ||
		newConfig.Security.ConnectionRateLimit != g.config.Security.ConnectionRateLimit {
		g.ipLimiter = ratelimit.NewIPLimiter(
			newConfig.Security.MaxConnectionsPerIP,
			newConfig.Security.ConnectionRateLimit,
		)
		logger.L.Info("IP limiter updated",
			zap.Int("old_max_per_ip", g.config.Security.MaxConnectionsPerIP),
			zap.Int("new_max_per_ip", newConfig.Security.MaxConnectionsPerIP),
			zap.Int("old_rate_limit", g.config.Security.ConnectionRateLimit),
			zap.Int("new_rate_limit", newConfig.Security.ConnectionRateLimit),
		)
	}

	// Update configuration
	g.config = newConfig

	logger.L.Info("configuration updated successfully")
	return nil
}

// GetConfig returns the current configuration (thread-safe)
func (g *Gateway) GetConfig() *config.Config {
	g.configMu.RLock()
	defer g.configMu.RUnlock()
	return g.config
}
