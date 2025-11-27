package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/SkynetNext/game-gateway/internal/config"
	"github.com/SkynetNext/game-gateway/internal/router"
	"github.com/redis/go-redis/v9"
)

// Client is a Redis client wrapper
type Client struct {
	rdb    *redis.Client
	prefix string
}

// NewClient creates a new Redis client
func NewClient(cfg *config.RedisConfig) *Client {
	rdb := redis.NewClient(&redis.Options{
		Addr:         cfg.Addr,
		Password:     cfg.Password,
		DB:           cfg.DB,
		PoolSize:     cfg.PoolSize,
		MinIdleConns: cfg.MinIdleConns,
		DialTimeout:  cfg.DialTimeout,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
	})

	return &Client{
		rdb:    rdb,
		prefix: cfg.KeyPrefix,
	}
}

// Close closes the Redis connection
func (c *Client) Close() error {
	return c.rdb.Close()
}

// Ping checks Redis connection
func (c *Client) Ping(ctx context.Context) error {
	return c.rdb.Ping(ctx).Err()
}

// key generates full key with prefix
func (c *Client) key(suffix string) string {
	return c.prefix + suffix
}

// LoadRoutingRules loads routing rules from Redis
// Returns a map where key is the serverType (integer ID) and value is the RoutingRule
func (c *Client) LoadRoutingRules(ctx context.Context) (map[int]*router.RoutingRule, error) {
	key := c.key("routing:rules")
	data, err := c.rdb.Get(ctx, key).Result()
	if err == redis.Nil {
		return nil, nil // No config, return empty
	}
	if err != nil {
		return nil, fmt.Errorf("failed to load routing rules: %w", err)
	}

	var rules map[int]*router.RoutingRule
	if err := json.Unmarshal([]byte(data), &rules); err != nil {
		return nil, fmt.Errorf("failed to parse routing rules: %w", err)
	}

	return rules, nil
}

// LoadRealmMapping loads realm ID to GameServer address mapping from Redis
func (c *Client) LoadRealmMapping(ctx context.Context) (map[int32]string, error) {
	key := c.key("realm:mapping")
	data, err := c.rdb.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to load realm mapping: %w", err)
	}

	mapping := make(map[int32]string)
	for realmIDStr, address := range data {
		var realmID int32
		if _, err := fmt.Sscanf(realmIDStr, "%d", &realmID); err != nil {
			continue // Skip invalid key
		}
		mapping[realmID] = address
	}

	return mapping, nil
}

// WatchRoutingRules watches for routing rules changes via Redis pub/sub
func (c *Client) WatchRoutingRules(ctx context.Context, callback func(map[int]*router.RoutingRule)) error {
	key := c.key("routing:rules")
	pubsub := c.rdb.Subscribe(ctx, key+":notify")
	defer pubsub.Close()

	ch := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg := <-ch:
			if msg != nil {
				rules, err := c.LoadRoutingRules(ctx)
				if err == nil {
					callback(rules)
				}
			}
		}
	}
}

// WatchRealmMapping watches for realm mapping changes via Redis pub/sub
func (c *Client) WatchRealmMapping(ctx context.Context, callback func(map[int32]string)) error {
	key := c.key("realm:mapping")
	pubsub := c.rdb.Subscribe(ctx, key+":notify")
	defer pubsub.Close()

	ch := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg := <-ch:
			if msg != nil {
				mapping, err := c.LoadRealmMapping(ctx)
				if err == nil {
					callback(mapping)
				}
			}
		}
	}
}

// RefreshLoop periodically refreshes configuration from Redis
func (c *Client) RefreshLoop(ctx context.Context, interval time.Duration, onRoutingRules func(map[int]*router.RoutingRule), onRealmMapping func(map[int32]string)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Refresh routing rules
			if rules, err := c.LoadRoutingRules(ctx); err == nil && onRoutingRules != nil {
				onRoutingRules(rules)
			}

			// Refresh realm mapping
			if mapping, err := c.LoadRealmMapping(ctx); err == nil && onRealmMapping != nil {
				onRealmMapping(mapping)
			}
		}
	}
}
