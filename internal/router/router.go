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
	WorldID     int             `json:"world_id,omitempty"` // World ID for game service (e.g., 1 for world 1)
}

// Router manages routing rules and routes requests to backend services
type Router struct {
	config *config.RoutingConfig

	mu    sync.RWMutex
	rules map[string]*RoutingRule // key format: "serverType:worldID" (worldID=0 means wildcard)

	// Round-robin counters per service type (key format: "serverType:worldID")
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

// makeRuleKey creates a key for routing rule: "serverType:worldID"
// If worldID is 0 or not set, use 0 (wildcard for all worlds)
func makeRuleKey(serverType, worldID int) string {
	if worldID <= 0 {
		return fmt.Sprintf("%d:0", serverType)
	}
	return fmt.Sprintf("%d:%d", serverType, worldID)
}

// UpdateRule updates routing rule by serverType and worldID
// If worldID is 0 or not set, the rule applies to all worlds (wildcard)
func (r *Router) UpdateRule(rule *RoutingRule) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := makeRuleKey(rule.ServerType, rule.WorldID)
	r.rules[key] = rule
}

// RemoveRule removes routing rule by serverType and worldID
// If worldID is 0, removes wildcard rule for the serverType
func (r *Router) RemoveRule(serverType, worldID int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := makeRuleKey(serverType, worldID)
	delete(r.rules, key)
}

// GetRule gets routing rule by serverType and worldID
// If exact match not found, tries to find wildcard rule (worldID=0)
// Returns the rule and whether it was found
func (r *Router) GetRule(serverType, worldID int) (*RoutingRule, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// First try exact match
	if worldID > 0 {
		key := makeRuleKey(serverType, worldID)
		if rule, ok := r.rules[key]; ok {
			return rule, true
		}
	}

	// Fallback to wildcard (worldID=0)
	wildcardKey := makeRuleKey(serverType, 0)
	rule, ok := r.rules[wildcardKey]
	return rule, ok
}

// RouteByServerID routes request to backend service based on serverType, worldID, and instID
// serverType is the integer ID from packet header (e.g., 10 for account, 15 for version, 200 for game)
// worldID: 0 means wildcard (match all worlds), >0 means specific world
// For account/version services, worldID is typically 0 (wildcard)
// For game service, worldID can be specific (e.g., 1 for world 1)
func (r *Router) RouteByServerID(ctx context.Context, serverType, worldID, instID int) (string, error) {
	rule, ok := r.GetRule(serverType, worldID)
	if !ok {
		return "", fmt.Errorf("routing rule not found for serverType: %d, worldID: %d", serverType, worldID)
	}

	if len(rule.Endpoints) == 0 {
		return "", fmt.Errorf("no available endpoints for serverType: %d", serverType)
	}

	switch rule.Strategy {
	case StrategyRoundRobin:
		// For round-robin, use serverType:worldID as key for counter
		return r.routeRoundRobin(serverType, worldID, rule)
	case StrategyConsistentHash:
		// For consistent hash, can use instID as hash key if needed
		// For now, use a simple hash based on instID
		if instID > 0 {
			hash := crc32.ChecksumIEEE(fmt.Appendf(nil, "%d", instID))
			index := int(hash) % len(rule.Endpoints)
			return rule.Endpoints[index], nil
		}
		return r.routeRoundRobin(serverType, worldID, rule)
	case StrategyRealmBased:
		// For realm-based (game service), use worldID + instID
		if worldID > 0 && instID > 0 {
			hash := crc32.ChecksumIEEE(fmt.Appendf(nil, "world:%d:inst:%d", worldID, instID))
			index := int(hash) % len(rule.Endpoints)
			return rule.Endpoints[index], nil
		}
		return r.routeRoundRobin(serverType, worldID, rule)
	default:
		return "", fmt.Errorf("unknown routing strategy: %s", rule.Strategy)
	}
}

// routeRoundRobin routes using round-robin algorithm
func (r *Router) routeRoundRobin(serverType, worldID int, rule *RoutingRule) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := makeRuleKey(serverType, worldID)
	count := r.roundRobinCounters[key]
	endpoint := rule.Endpoints[count%len(rule.Endpoints)]
	r.roundRobinCounters[key] = (count + 1) % len(rule.Endpoints)

	return endpoint, nil
}

// GetAllRules returns all routing rules (for monitoring)
// Returns a map where key is "serverType:worldID" and value is the rule
func (r *Router) GetAllRules() map[string]*RoutingRule {
	r.mu.RLock()
	defer r.mu.RUnlock()

	rules := make(map[string]*RoutingRule)
	for k, v := range r.rules {
		rules[k] = v
	}
	return rules
}
