package pool

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/SkynetNext/game-gateway/internal/config"
)

// Manager manages connection pools for multiple backend services
type Manager struct {
	config *config.ConnectionPoolConfig

	mu    sync.RWMutex
	pools map[string]*Pool // address -> pool
}

// NewManager creates a new connection pool manager
func NewManager(cfg *config.ConnectionPoolConfig) *Manager {
	return &Manager{
		config: cfg,
		pools:  make(map[string]*Pool),
	}
}

// GetPool gets or creates a connection pool for an address
func (m *Manager) GetPool(address string) *Pool {
	m.mu.RLock()
	pool, ok := m.pools[address]
	m.mu.RUnlock()

	if ok {
		return pool
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Double-check
	pool, ok = m.pools[address]
	if ok {
		return pool
	}

	// Create new connection pool
	pool = NewPool(
		address,
		m.config.DialTimeout,
		m.config.IdleTimeout,
		m.config.MaxConnections,
		m.config.MaxConnectionsPerService,
	)

	m.pools[address] = pool
	return pool
}

// GetConnection gets a connection from the pool for an address
func (m *Manager) GetConnection(ctx context.Context, address string) (*Connection, error) {
	pool := m.GetPool(address)
	return pool.Get(ctx)
}

// PutConnection returns a connection to the pool
func (m *Manager) PutConnection(address string, conn *Connection) {
	pool := m.GetPool(address)
	pool.Put(conn)
}

// RemoveConnection removes a connection from the pool
func (m *Manager) RemoveConnection(address string, conn *Connection) {
	pool := m.GetPool(address)
	pool.Remove(conn)
}

// Cleanup cleans up all connection pools
func (m *Manager) Cleanup() int {
	m.mu.RLock()
	pools := make([]*Pool, 0, len(m.pools))
	for _, pool := range m.pools {
		pools = append(pools, pool)
	}
	m.mu.RUnlock()

	total := 0
	for _, pool := range pools {
		total += pool.Cleanup()
	}
	return total
}

// StartCleanup starts periodic cleanup of connection pools
func (m *Manager) StartCleanup(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cleaned := m.Cleanup()
			if cleaned > 0 {
				// Log cleanup activity (can be useful for monitoring)
				// Note: In production, this could be a debug-level log
			}
		}
	}
}

// Close closes all connection pools
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for address, pool := range m.pools {
		if err := pool.Close(); err != nil {
			return fmt.Errorf("failed to close connection pool %s: %w", address, err)
		}
		delete(m.pools, address)
	}
	return nil
}
