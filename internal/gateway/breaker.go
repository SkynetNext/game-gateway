package gateway

import (
	"time"

	"github.com/SkynetNext/game-gateway/internal/circuitbreaker"
	"github.com/SkynetNext/game-gateway/internal/metrics"
)

// getOrCreateBreaker gets or creates a circuit breaker for a backend address
func (g *Gateway) getOrCreateBreaker(backendAddr string) *circuitbreaker.Breaker {
	g.breakerMu.RLock()
	breaker, ok := g.circuitBreakers[backendAddr]
	g.breakerMu.RUnlock()

	if ok {
		// Update metrics
		metrics.CircuitBreakerState.WithLabelValues(backendAddr).Set(float64(breaker.State()))
		return breaker
	}

	// Create new breaker
	g.breakerMu.Lock()
	defer g.breakerMu.Unlock()

	// Double-check
	breaker, ok = g.circuitBreakers[backendAddr]
	if ok {
		metrics.CircuitBreakerState.WithLabelValues(backendAddr).Set(float64(breaker.State()))
		return breaker
	}

	// Create with default settings: 5 failures, 30s timeout
	breaker = circuitbreaker.NewBreaker(5, 30*time.Second)
	g.circuitBreakers[backendAddr] = breaker
	metrics.CircuitBreakerState.WithLabelValues(backendAddr).Set(float64(breaker.State()))
	return breaker
}
