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
	ServiceType string          `json:"service_type"` // Service type from config (e.g., "account", "version", "game")
	Strategy    RoutingStrategy `json:"strategy"`
	Endpoints   []string        `json:"endpoints"`
	HashKey     string          `json:"hash_key,omitempty"` // Key for consistent hashing (e.g., user_id)
}

// Router manages routing rules and routes requests to backend services
type Router struct {
	config *config.RoutingConfig

	mu    sync.RWMutex
	rules map[string]*RoutingRule // service_type -> rule

	// Round-robin counters per service type
	roundRobinCounters map[string]int
}

// NewRouter creates a new router instance
func NewRouter(cfg *config.RoutingConfig) *Router {
	return &Router{
		config:             cfg,
		rules:              make(map[string]*RoutingRule),
		roundRobinCounters: make(map[string]int),
	}
}

// UpdateRule updates routing rule for a service type
func (r *Router) UpdateRule(rule *RoutingRule) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rules[rule.ServiceType] = rule
}

// GetRule gets routing rule for a service type
func (r *Router) GetRule(serviceType string) (*RoutingRule, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rule, ok := r.rules[serviceType]
	return rule, ok
}

// Route routes request to backend service based on service type, realm ID, and user ID
func (r *Router) Route(ctx context.Context, serviceType string, realmID int32, userID int64) (string, error) {
	rule, ok := r.GetRule(serviceType)
	if !ok {
		return "", fmt.Errorf("routing rule not found for service type: %s", serviceType)
	}

	if len(rule.Endpoints) == 0 {
		return "", fmt.Errorf("no available endpoints for service type: %s", serviceType)
	}

	switch rule.Strategy {
	case StrategyRoundRobin:
		return r.routeRoundRobin(serviceType, rule)
	case StrategyConsistentHash:
		return r.routeConsistentHash(rule, userID)
	case StrategyRealmBased:
		return r.routeRealmBased(rule, realmID)
	default:
		return "", fmt.Errorf("unknown routing strategy: %s", rule.Strategy)
	}
}

// routeRoundRobin routes using round-robin algorithm
func (r *Router) routeRoundRobin(serviceType string, rule *RoutingRule) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	count := r.roundRobinCounters[serviceType]
	endpoint := rule.Endpoints[count%len(rule.Endpoints)]
	r.roundRobinCounters[serviceType] = (count + 1) % len(rule.Endpoints)

	return endpoint, nil
}

// routeConsistentHash routes using consistent hashing based on user ID
func (r *Router) routeConsistentHash(rule *RoutingRule, userID int64) (string, error) {
	if len(rule.Endpoints) == 0 {
		return "", fmt.Errorf("no available endpoints")
	}

	// Use CRC32 for hashing
	hash := crc32.ChecksumIEEE([]byte(fmt.Sprintf("%d", userID)))
	index := int(hash) % len(rule.Endpoints)
	return rule.Endpoints[index], nil
}

// routeRealmBased routes based on realm ID
func (r *Router) routeRealmBased(rule *RoutingRule, realmID int32) (string, error) {
	if realmID <= 0 {
		return "", fmt.Errorf("invalid realm ID: %d", realmID)
	}

	// Select endpoint based on realm_id (can use hash or direct mapping)
	hash := crc32.ChecksumIEEE([]byte(fmt.Sprintf("realm:%d", realmID)))
	index := int(hash) % len(rule.Endpoints)
	return rule.Endpoints[index], nil
}

// GetAllRules returns all routing rules (for monitoring)
func (r *Router) GetAllRules() map[string]*RoutingRule {
	r.mu.RLock()
	defer r.mu.RUnlock()

	rules := make(map[string]*RoutingRule)
	for k, v := range r.rules {
		rules[k] = v
	}
	return rules
}
