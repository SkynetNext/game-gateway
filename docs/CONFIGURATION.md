# Configuration Guide

## Overview

Game Gateway uses YAML configuration files for all settings. Configuration is validated on startup and supports hot reload for certain settings.

## Configuration File Structure

### Complete Example

```yaml
# Server Configuration
server:
  listen_addr: ":8080"              # Business port
  health_check_port: 9090           # Health check endpoint
  metrics_port: 9091                # Prometheus metrics endpoint

# Redis Configuration
redis:
  addr: "10.1.0.8:6379"             # Redis address
  password: ""                       # Redis password (if required)
  db: 0                              # Redis database number
  key_prefix: "game-gateway:"        # Key prefix for Redis keys
  pool_size: 10                      # Connection pool size
  min_idle_conns: 5                  # Minimum idle connections
  dial_timeout: 5s                   # Connection timeout
  read_timeout: 3s                   # Read operation timeout
  write_timeout: 3s                  # Write operation timeout

# Backend Service Configuration
backend:
  account_service: "account-server.hgame.svc.cluster.local:8080"
  version_service: "version-server.hgame.svc.cluster.local:8080"
  game_service: "game-server.hgame.svc.cluster.local:8080"

# Routing Configuration
routing:
  refresh_interval: 10s              # Routing rules refresh interval
  realm_refresh_interval: 5s         # Realm mapping refresh interval

# Connection Pool Configuration
connection_pool:
  max_connections: 1000              # Maximum total connections
  max_connections_per_service: 100   # Maximum connections per backend service
  idle_timeout: 5m                   # Idle connection timeout
  dial_timeout: 5s                   # Connection establishment timeout
  read_timeout: 30s                  # Read operation timeout
  write_timeout: 30s                 # Write operation timeout
  max_retries: 3                     # Maximum retry attempts
  retry_delay: 100ms                 # Delay between retries

# Security Configuration
security:
  max_message_size: 1048576          # Maximum message size (1MB)
  max_connections_per_ip: 10        # Maximum connections per IP
  connection_rate_limit: 5           # Connections per second per IP

# Tracing Configuration
tracing:
  # Enable trace ID propagation to backend services
  # WARNING: Requires backend services to support extended GateMsgHeader format (33 bytes)
  # Default: false (disabled for compatibility with existing cluster)
  enable_trace_propagation: false

# Graceful Shutdown
graceful_shutdown_timeout: 30s       # Timeout for graceful shutdown
```

## Configuration Sections

### Server Configuration

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `listen_addr` | string | `:8080` | TCP address to listen on for business traffic |
| `health_check_port` | int | `9090` | HTTP port for health check endpoints |
| `metrics_port` | int | `9091` | HTTP port for Prometheus metrics |

### Redis Configuration

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `addr` | string | Required | Redis server address (host:port) |
| `password` | string | `""` | Redis password (empty if not required) |
| `db` | int | `0` | Redis database number |
| `key_prefix` | string | `"game-gateway:"` | Prefix for all Redis keys |
| `pool_size` | int | `10` | Maximum number of connections in pool |
| `min_idle_conns` | int | `5` | Minimum number of idle connections |
| `dial_timeout` | duration | `5s` | Connection establishment timeout |
| `read_timeout` | duration | `3s` | Read operation timeout |
| `write_timeout` | duration | `3s` | Write operation timeout |

**Redis Keys Used**:
- `game-gateway:routing:rules` - Routing rules (JSON)
- `game-gateway:realm:mapping` - Realm ID to GameServer mapping (Hash)

### Backend Configuration

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `account_service` | string | Required | Account service address |
| `version_service` | string | Required | Version service address |
| `game_service` | string | Required | Game service address (default, can be overridden by realm mapping) |

**Note**: These are default addresses. Actual routing is determined by Redis configuration.

### Routing Configuration

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `refresh_interval` | duration | `10s` | How often to refresh routing rules from Redis |
| `realm_refresh_interval` | duration | `5s` | How often to refresh realm mapping from Redis |

### Connection Pool Configuration

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `max_connections` | int | `1000` | Maximum total connections across all services |
| `max_connections_per_service` | int | `100` | Maximum connections per backend service |
| `idle_timeout` | duration | `5m` | Close idle connections after this duration |
| `dial_timeout` | duration | `5s` | Connection establishment timeout |
| `read_timeout` | duration | `30s` | Read operation timeout |
| `write_timeout` | duration | `30s` | Write operation timeout |
| `max_retries` | int | `3` | Maximum retry attempts for connection failures |
| `retry_delay` | duration | `100ms` | Delay between retry attempts |

### Security Configuration

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `max_message_size` | int | `1048576` | Maximum message size in bytes (1MB default) |
| `max_connections_per_ip` | int | `10` | Maximum concurrent connections per IP address |
| `connection_rate_limit` | int | `5` | Maximum connections per second per IP |

### Tracing Configuration

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enable_trace_propagation` | bool | `false` | Enable Trace ID propagation to backend services |

**⚠️ Important**: 
- Default: `false` (disabled) for compatibility with existing cluster
- When enabled, requires backend services to support extended GateMsgHeader format (33 bytes)
- See [Trace Propagation](./TRACE_PROPAGATION.md) for details

## Environment Variables

Some settings can be overridden via environment variables:

| Variable | Description | Default |
|----------|-------------|---------|
| `LOG_LEVEL` | Logging level (debug, info, warn, error) | `info` |
| `POD_NAME` | Kubernetes Pod name (auto-injected) | Hostname |
| `JAEGER_ENDPOINT` | Jaeger collector endpoint for tracing | (disabled) |

## Configuration Validation

All configurations are validated on startup:

- **Required Fields**: Missing required fields cause startup failure
- **Value Ranges**: Invalid values (e.g., negative timeouts) are rejected
- **Format Validation**: Invalid formats (e.g., invalid address) are rejected

### Validation Rules

1. **Server**:
   - `listen_addr` must be a valid TCP address
   - Ports must be between 1 and 65535

2. **Redis**:
   - `addr` must be in format `host:port`
   - `pool_size` must be > 0
   - Timeouts must be > 0

3. **Connection Pool**:
   - `max_connections` must be > 0
   - `max_connections_per_service` must be > 0
   - Timeouts must be > 0

4. **Security**:
   - `max_message_size` must be > 0
   - `max_connections_per_ip` must be > 0
   - `connection_rate_limit` must be > 0

## Configuration Hot Reload

Certain configuration values can be updated without restart:

### Hot Reloadable Settings

- Connection pool settings (limits, timeouts)
- Rate limiting settings
- Security settings (message size, connection limits)
- Tracing settings (trace propagation)

### Non-Reloadable Settings

- Server addresses (listen_addr, ports)
- Redis connection settings
- Backend service addresses

### Hot Reload API

```go
// Update configuration programmatically
newConfig := gateway.GetConfig()
newConfig.Security.MaxMessageSize = 2 * 1024 * 1024  // 2MB
gateway.UpdateConfig(newConfig)
```

## Configuration Best Practices

### 1. Production Settings

```yaml
connection_pool:
  max_connections: 2000              # Higher for production
  max_connections_per_service: 200
  idle_timeout: 10m                  # Longer for connection reuse

security:
  max_message_size: 2097152          # 2MB for production
  max_connections_per_ip: 20        # Higher limit
  connection_rate_limit: 10
```

### 2. Development Settings

```yaml
connection_pool:
  max_connections: 100               # Lower for dev
  idle_timeout: 1m                   # Shorter for faster cleanup

security:
  max_message_size: 1048576           # 1MB default
```

### 3. High-Load Settings

```yaml
connection_pool:
  max_connections: 5000
  max_connections_per_service: 500
  read_timeout: 60s                  # Longer for slow backends
  write_timeout: 60s

routing:
  refresh_interval: 5s                # More frequent updates
```

## Troubleshooting Configuration

### Common Issues

1. **Configuration File Not Found**
   ```
   Error: failed to read config file: open config/config.yaml: no such file or directory
   ```
   **Solution**: Ensure config file exists at specified path

2. **Invalid Configuration**
   ```
   Error: invalid configuration: connection_pool.max_connections must be greater than 0
   ```
   **Solution**: Check configuration values are valid

3. **Redis Connection Failed**
   ```
   Error: failed to connect to Redis: dial tcp: connection refused
   ```
   **Solution**: Check Redis address and network connectivity

### Configuration Validation

Test configuration without starting service:

```bash
# Validate config file
go run ./cmd/gateway -config config/config.yaml -validate-only
```

## Configuration Examples

### Minimal Configuration

```yaml
server:
  listen_addr: ":8080"
  health_check_port: 9090
  metrics_port: 9091

redis:
  addr: "localhost:6379"

backend:
  account_service: "localhost:8081"
  version_service: "localhost:8082"
  game_service: "localhost:8083"
```

### Kubernetes Configuration

```yaml
server:
  listen_addr: ":8080"
  health_check_port: 9090
  metrics_port: 9091

redis:
  addr: "redis-service.hgame.svc.cluster.local:6379"

backend:
  account_service: "account-server.hgame.svc.cluster.local:8080"
  version_service: "version-server.hgame.svc.cluster.local:8080"
  game_service: "game-server.hgame.svc.cluster.local:8080"
```

---

**Last Updated**: 2024-01-15

