package gateway

import (
	"testing"
	"time"

	"github.com/SkynetNext/game-gateway/internal/config"
)

func TestGateway_New(t *testing.T) {
	cfg := &config.Config{
		Server: config.ServerConfig{
			ListenAddr:      ":0", // Use random port for testing
			HealthCheckPort: 0,
			MetricsPort:     0,
		},
		Redis: config.RedisConfig{
			Addr:     "localhost:6379",
			Password: "",
			DB:       0,
		},
		ConnectionPool: config.ConnectionPoolConfig{
			MaxConnections:           100,
			MaxConnectionsPerService: 10,
			DialTimeout:              5 * time.Second,
			IdleTimeout:              5 * time.Minute,
		},
		Routing: config.RoutingConfig{
			RefreshInterval: 10 * time.Second,
		},
		GracefulShutdownTimeout: 30 * time.Second,
	}

	// This test requires Redis, so we'll skip if Redis is not available
	gw, err := New(cfg, "test-pod")
	if err != nil {
		t.Skipf("Skipping test: Redis not available: %v", err)
	}

	if gw == nil {
		t.Fatal("Expected gateway instance, got nil")
	}

	if gw.config != cfg {
		t.Error("Config not set correctly")
	}
}

func TestGateway_generateSessionID(t *testing.T) {
	cfg := &config.Config{
		ConnectionPool: config.ConnectionPoolConfig{
			MaxConnections: 100,
		},
	}

	gw := &Gateway{
		config:    cfg,
		podName:   "test-pod",
		startTime: time.Now().Unix(),
		podHash:   12345,
	}

	// Generate multiple session IDs and check uniqueness
	ids := make(map[int64]bool)
	for i := 0; i < 1000; i++ {
		id := gw.generateSessionID()
		if ids[id] {
			t.Errorf("Duplicate session ID generated: %d", id)
		}
		ids[id] = true
	}
}
