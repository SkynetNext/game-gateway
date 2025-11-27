package gateway

import (
	"context"

	"github.com/SkynetNext/game-gateway/internal/config"
)

// Gateway represents the game gateway service
type Gateway struct {
	config  *config.Config
	podName string
}

// New creates a new gateway instance
func New(cfg *config.Config, podName string) (*Gateway, error) {
	return &Gateway{
		config:  cfg,
		podName: podName,
	}, nil
}

// Start starts the gateway service
func (g *Gateway) Start(ctx context.Context) error {
	// TODO: Implement startup logic
	return nil
}

// Shutdown gracefully shuts down the gateway
func (g *Gateway) Shutdown(ctx context.Context) error {
	// TODO: Implement shutdown logic
	return nil
}
