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
		config:  cfg,
		podName: "test-pod",
		podHash: 12345,
	}

	// Generate multiple session IDs and check uniqueness
	ids := make(map[int64]bool)
	for i := 0; i < 10000; i++ {
		id := gw.generateSessionID()
		if ids[id] {
			t.Errorf("Duplicate session ID generated: %d", id)
		}
		ids[id] = true

		// Verify ID is positive (sign bit should be 0)
		if id <= 0 {
			t.Errorf("Generated negative session ID: %d", id)
		}
	}

	// Test high-throughput scenario: generate IDs rapidly
	t.Run("HighThroughput", func(t *testing.T) {
		start := time.Now()
		count := 100000
		ids := make(map[int64]bool, count)

		for i := 0; i < count; i++ {
			id := gw.generateSessionID()
			if ids[id] {
				t.Errorf("Duplicate session ID in high-throughput test: %d", id)
			}
			ids[id] = true
		}

		duration := time.Since(start)
		t.Logf("Generated %d unique IDs in %v (%.0f IDs/sec)", count, duration, float64(count)/duration.Seconds())
	})
}

func TestParseSessionID(t *testing.T) {
	cfg := &config.Config{
		ConnectionPool: config.ConnectionPoolConfig{
			MaxConnections: 100,
		},
	}

	gw := &Gateway{
		config:  cfg,
		podName: "test-pod-123",
		podHash: 0xABC, // 12-bit value: 2748
	}

	// Generate a session ID
	beforeGen := time.Now()
	sessionID := gw.generateSessionID()
	afterGen := time.Now()

	// Parse it back
	components := ParseSessionID(sessionID)

	// Verify pod hash matches (lower 12 bits)
	expectedPodHash := gw.podHash & 0xFFF
	if components.PodHash != expectedPodHash {
		t.Errorf("Expected pod hash %d, got %d", expectedPodHash, components.PodHash)
	}

	// Verify timestamp is within reasonable range
	if components.CreatedAt.Before(beforeGen) || components.CreatedAt.After(afterGen) {
		t.Errorf("Timestamp out of range: %v (expected between %v and %v)",
			components.CreatedAt, beforeGen, afterGen)
	}

	// Verify sequence is in valid range (0-4095)
	if components.Sequence > 4095 {
		t.Errorf("Sequence number out of range: %d (max 4095)", components.Sequence)
	}

	t.Logf("Session ID: %d", sessionID)
	t.Logf("  Pod Hash: %d (0x%03X)", components.PodHash, components.PodHash)
	t.Logf("  Timestamp: %d (%v)", components.Timestamp, components.CreatedAt)
	t.Logf("  Sequence: %d", components.Sequence)
}
