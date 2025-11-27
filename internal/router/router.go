package router

import (
	"context"
	"fmt"
	"hash/crc32"
	"sync"

	"github.com/SkynetNext/game-gateway/internal/config"
)

// RoutingStrategy represents routing strategy type
type RoutingStrategy string

const (
	StrategyRoundRobin     RoutingStrategy = "round_robin"
	StrategyConsistentHash RoutingStrategy = "consistent_hash"
	StrategyRealmBased     RoutingStrategy = "realm_based"
)

// RoutingRule defines routing rule for a service type
type RoutingRule struct {
	ServerType  int             `json:"server_type"`  // Primary key, serverType (integer from packet header)
	ServiceName string          `json:"service_name"` // Service name for display/config (e.g., "account", "version", "game")
	Strategy    RoutingStrategy `json:"strategy"`
	Endpoints   []string        `json:"endpoints"`
	HashKey     string          `json:"hash_key,omitempty"` // Key for consistent hashing (e.g., user_id)
}

// Router manages routing rules and routes requests to backend services
type Router struct {
	config *config.RoutingConfig

	mu    sync.RWMutex
	rules map[int]*RoutingRule // serverType (int) -> rule (primary key lookup)

	// Round-robin counters per service type
	roundRobinCounters map[int]int
}

// NewRouter creates a new router instance
func NewRouter(cfg *config.RoutingConfig) *Router {
	return &Router{
		config:             cfg,
		rules:              make(map[int]*RoutingRule),
		roundRobinCounters: make(map[int]int),
	}
}

// UpdateRule updates routing rule by serverType
func (r *Router) UpdateRule(rule *RoutingRule) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rules[rule.ServerType] = rule
}

// RemoveRule removes routing rule by serverType (ID)
func (r *Router) RemoveRule(serverType int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.rules, serverType)
}

// GetRule gets routing rule by serverType (ID)
func (r *Router) GetRule(serverType int) (*RoutingRule, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rule, ok := r.rules[serverType]
	return rule, ok
}

// RouteByServerID routes request to backend service based on serverType, worldID, and instID
// serverType is the integer ID from packet header (e.g., 10 for account, 20 for version, 30 for game)
// For account/version services, may only use serverType (instID ignored)
// For game service, may use serverType + worldID + instID
func (r *Router) RouteByServerID(ctx context.Context, serverType, worldID, instID int) (string, error) {
	rule, ok := r.GetRule(serverType)
	if !ok {
		return "", fmt.Errorf("routing rule not found for serverType: %d", serverType)
	}

	if len(rule.Endpoints) == 0 {
		return "", fmt.Errorf("no available endpoints for serverType: %d", serverType)
	}

	switch rule.Strategy {
	case StrategyRoundRobin:
		// For round-robin, ignore instID and just use serverType
		return r.routeRoundRobin(serverType, rule)
	case StrategyConsistentHash:
		// For consistent hash, can use instID as hash key if needed
		// For now, use a simple hash based on instID
		if instID > 0 {
			hash := crc32.ChecksumIEEE(fmt.Appendf(nil, "%d", instID))
			index := int(hash) % len(rule.Endpoints)
			return rule.Endpoints[index], nil
		}
		return r.routeRoundRobin(serverType, rule)
	case StrategyRealmBased:
		// For realm-based (game service), use worldID + instID
		if worldID > 0 && instID > 0 {
			hash := crc32.ChecksumIEEE(fmt.Appendf(nil, "world:%d:inst:%d", worldID, instID))
			index := int(hash) % len(rule.Endpoints)
			return rule.Endpoints[index], nil
		}
		return r.routeRoundRobin(serverType, rule)
	default:
		return "", fmt.Errorf("unknown routing strategy: %s", rule.Strategy)
	}
}

// routeRoundRobin routes using round-robin algorithm
func (r *Router) routeRoundRobin(serverType int, rule *RoutingRule) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	count := r.roundRobinCounters[serverType]
	endpoint := rule.Endpoints[count%len(rule.Endpoints)]
	r.roundRobinCounters[serverType] = (count + 1) % len(rule.Endpoints)

	return endpoint, nil
}

// GetAllRules returns all routing rules by serverType (for monitoring)
func (r *Router) GetAllRules() map[int]*RoutingRule {
	r.mu.RLock()
	defer r.mu.RUnlock()

	rules := make(map[int]*RoutingRule)
	for k, v := range r.rules {
		rules[k] = v
	}
	return rules
}
