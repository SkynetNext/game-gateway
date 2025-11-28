package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config represents gateway configuration
type Config struct {
	// Server configuration
	Server ServerConfig `yaml:"server"`

	// Redis configuration
	Redis RedisConfig `yaml:"redis"`

	// Backend service configuration
	Backend BackendConfig `yaml:"backend"`

	// Routing configuration
	Routing RoutingConfig `yaml:"routing"`

	// Connection pool configuration
	ConnectionPool ConnectionPoolConfig `yaml:"connection_pool"`

	// Security configuration
	Security SecurityConfig `yaml:"security"`

	// Tracing configuration
	Tracing TracingConfig `yaml:"tracing"`

	// Graceful shutdown timeout
	GracefulShutdownTimeout time.Duration `yaml:"graceful_shutdown_timeout"`
}

// ServerConfig represents server configuration
type ServerConfig struct {
	// Listen address
	ListenAddr string `yaml:"listen_addr"`

	// Health check port
	HealthCheckPort int `yaml:"health_check_port"`

	// Metrics port
	MetricsPort int `yaml:"metrics_port"`

	// Gateway AppID for cluster communication
	// This ID is sent in ClusterClientRegister handshake message
	// Backend servers use this to identify the gateway
	GatewayAppID uint32 `yaml:"gateway_app_id"`

	// Use gRPC for backend communication
	UseGrpc bool `yaml:"use_grpc"`
}

// RedisConfig represents Redis configuration
type RedisConfig struct {
	Addr     string `yaml:"addr"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`

	// Key prefix for Redis keys
	KeyPrefix string `yaml:"key_prefix"`

	// Connection pool configuration
	PoolSize     int           `yaml:"pool_size"`
	MinIdleConns int           `yaml:"min_idle_conns"`
	DialTimeout  time.Duration `yaml:"dial_timeout"`
	ReadTimeout  time.Duration `yaml:"read_timeout"`
	WriteTimeout time.Duration `yaml:"write_timeout"`
}

// BackendConfig represents backend service configuration
type BackendConfig struct {
	// Account service address (default, can be overridden by routing rules)
	AccountService string `yaml:"account_service"`

	// Version service address (default, can be overridden by routing rules)
	VersionService string `yaml:"version_service"`

	// Game service address (default, can be overridden by realm_id mapping)
	GameService string `yaml:"game_service"`
}

// RoutingConfig represents routing configuration
type RoutingConfig struct {
	// Refresh interval for routing rules
	RefreshInterval time.Duration `yaml:"refresh_interval"`

	// Refresh interval for realm mapping
	RealmRefreshInterval time.Duration `yaml:"realm_refresh_interval"`
}

// ConnectionPoolConfig represents connection pool configuration
type ConnectionPoolConfig struct {
	// Maximum number of connections
	MaxConnections int `yaml:"max_connections"`

	// Maximum connections per service
	MaxConnectionsPerService int `yaml:"max_connections_per_service"`

	// Idle connection timeout
	IdleTimeout time.Duration `yaml:"idle_timeout"`

	// Connection dial timeout
	DialTimeout time.Duration `yaml:"dial_timeout"`

	// Connection read timeout
	ReadTimeout time.Duration `yaml:"read_timeout"`

	// Connection write timeout
	WriteTimeout time.Duration `yaml:"write_timeout"`

	// Retry configuration
	MaxRetries int           `yaml:"max_retries"` // Maximum retry attempts for connection
	RetryDelay time.Duration `yaml:"retry_delay"` // Delay between retries
}

// SecurityConfig represents security configuration
type SecurityConfig struct {
	// Maximum message size (in bytes) to prevent DoS attacks
	MaxMessageSize int `yaml:"max_message_size"`

	// Maximum connections per IP address
	MaxConnectionsPerIP int `yaml:"max_connections_per_ip"`

	// Connection rate limit (connections per second per IP)
	ConnectionRateLimit int `yaml:"connection_rate_limit"`
}

// TracingConfig represents tracing configuration
type TracingConfig struct {
	// Enable trace ID propagation to backend services
	// WARNING: This requires backend services to support extended GateMsgHeader format (33 bytes)
	// If disabled (default), uses original format (9 bytes) for compatibility with existing cluster
	// Default: false (disabled for compatibility)
	EnableTracePropagation bool `yaml:"enable_trace_propagation"`
}

// Load loads configuration from file
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Set default values
	setDefaults(&cfg)

	// Validate configuration
	if err := validateConfig(&cfg); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return &cfg, nil
}

// ValidateConfig validates the configuration (exported for hot reload)
func ValidateConfig(cfg *Config) error {
	return validateConfig(cfg)
}

// validateConfig validates the configuration
func validateConfig(cfg *Config) error {
	// Validate server configuration
	if cfg.Server.ListenAddr == "" {
		return fmt.Errorf("server.listen_addr is required")
	}
	if cfg.Server.HealthCheckPort <= 0 || cfg.Server.HealthCheckPort > 65535 {
		return fmt.Errorf("server.health_check_port must be between 1 and 65535")
	}

	// Validate Redis configuration
	if cfg.Redis.Addr == "" {
		return fmt.Errorf("redis.addr is required")
	}
	if cfg.Redis.PoolSize <= 0 {
		return fmt.Errorf("redis.pool_size must be greater than 0")
	}

	// Validate connection pool configuration
	if cfg.ConnectionPool.MaxConnections <= 0 {
		return fmt.Errorf("connection_pool.max_connections must be greater than 0")
	}
	if cfg.ConnectionPool.MaxConnectionsPerService <= 0 {
		return fmt.Errorf("connection_pool.max_connections_per_service must be greater than 0")
	}
	if cfg.ConnectionPool.DialTimeout <= 0 {
		return fmt.Errorf("connection_pool.dial_timeout must be greater than 0")
	}

	// Validate routing configuration
	if cfg.Routing.RefreshInterval <= 0 {
		return fmt.Errorf("routing.refresh_interval must be greater than 0")
	}

	// Validate graceful shutdown timeout
	if cfg.GracefulShutdownTimeout <= 0 {
		return fmt.Errorf("graceful_shutdown_timeout must be greater than 0")
	}

	return nil
}

// setDefaults sets default values for configuration
func setDefaults(cfg *Config) {
	if cfg.Server.ListenAddr == "" {
		cfg.Server.ListenAddr = ":8080"
	}

	if cfg.Server.HealthCheckPort == 0 {
		cfg.Server.HealthCheckPort = 9090
	}

	if cfg.Server.MetricsPort == 0 {
		cfg.Server.MetricsPort = 9091
	}

	// Redis address from config file (no environment variable override)
	if cfg.Redis.Addr == "" {
		cfg.Redis.Addr = "localhost:6379"
	}

	if cfg.Redis.KeyPrefix == "" {
		cfg.Redis.KeyPrefix = "game-gateway:"
	}

	if cfg.Redis.PoolSize == 0 {
		cfg.Redis.PoolSize = 10
	}

	if cfg.Redis.MinIdleConns == 0 {
		cfg.Redis.MinIdleConns = 5
	}

	if cfg.Redis.DialTimeout == 0 {
		cfg.Redis.DialTimeout = 5 * time.Second
	}

	if cfg.Redis.ReadTimeout == 0 {
		cfg.Redis.ReadTimeout = 3 * time.Second
	}

	if cfg.Redis.WriteTimeout == 0 {
		cfg.Redis.WriteTimeout = 3 * time.Second
	}

	if cfg.Routing.RefreshInterval == 0 {
		cfg.Routing.RefreshInterval = 10 * time.Second
	}

	if cfg.Routing.RealmRefreshInterval == 0 {
		cfg.Routing.RealmRefreshInterval = 5 * time.Second
	}

	if cfg.ConnectionPool.MaxConnections == 0 {
		cfg.ConnectionPool.MaxConnections = 1000
	}

	if cfg.ConnectionPool.MaxConnectionsPerService == 0 {
		cfg.ConnectionPool.MaxConnectionsPerService = 100
	}

	if cfg.ConnectionPool.IdleTimeout == 0 {
		cfg.ConnectionPool.IdleTimeout = 5 * time.Minute
	}

	if cfg.ConnectionPool.DialTimeout == 0 {
		cfg.ConnectionPool.DialTimeout = 5 * time.Second
	}

	if cfg.ConnectionPool.ReadTimeout == 0 {
		cfg.ConnectionPool.ReadTimeout = 30 * time.Second
	}

	if cfg.ConnectionPool.WriteTimeout == 0 {
		cfg.ConnectionPool.WriteTimeout = 30 * time.Second
	}

	if cfg.ConnectionPool.MaxRetries == 0 {
		cfg.ConnectionPool.MaxRetries = 3
	}

	if cfg.ConnectionPool.RetryDelay == 0 {
		cfg.ConnectionPool.RetryDelay = 100 * time.Millisecond
	}

	if cfg.GracefulShutdownTimeout == 0 {
		cfg.GracefulShutdownTimeout = 30 * time.Second
	}

	// Security defaults
	if cfg.Security.MaxMessageSize == 0 {
		cfg.Security.MaxMessageSize = 1024 * 1024 // 1MB default
	}
	if cfg.Security.MaxConnectionsPerIP == 0 {
		cfg.Security.MaxConnectionsPerIP = 10 // 10 connections per IP default
	}
	if cfg.Security.ConnectionRateLimit == 0 {
		cfg.Security.ConnectionRateLimit = 5 // 5 connections per second per IP
	}

	// Tracing defaults
	// Default: false (disabled) for compatibility with existing cluster services
	// Enable only if backend services support extended GateMsgHeader format (33 bytes)
	// If false, uses original format (9 bytes) which is compatible with existing cluster
}
