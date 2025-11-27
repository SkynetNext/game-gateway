# Game Gateway

A high-performance, production-ready game gateway service implemented in Go, following industry best practices.

## Features

### Core Functionality
- ✅ Multi-protocol support (HTTP/WebSocket/TCP)
- ✅ Session management (sharded in-memory storage, high performance)
- ✅ Connection pooling (TCP connection reuse with O(1) retrieval)
- ✅ Dynamic routing (Account/Version/Game service routing)
- ✅ Realm-based routing mapping (based on realm_id)
- ✅ Redis configuration management (routing rules, realm mapping)

### Production Features
- ✅ **Rate Limiting**: Connection rate limiting to prevent overload
- ✅ **Circuit Breaker**: Automatic backend service failure detection and recovery
- ✅ **Retry Mechanism**: Exponential backoff retry for transient failures
- ✅ **Context Support**: Full context propagation with timeout control
- ✅ **Connection Timeouts**: Configurable read/write timeouts
- ✅ **Graceful Shutdown**: Drain mode with connection cleanup

### Observability
- ✅ **Structured Logging**: Zap-based structured logging
- ✅ **Prometheus Metrics**: Comprehensive metrics collection
- ✅ **Health Checks**: `/health` and `/ready` endpoints
- ✅ **Distributed Tracing**: OpenTelemetry support (optional)

### Infrastructure
- ✅ Kubernetes integration (HPA support, Pod identification)
- ✅ Configuration validation
- ✅ Error handling and recovery

## Architecture

```
Client
  ↓
Load Balancer
  ↓
Game Gateway Cluster
  ├─ Session Management (in-memory)
  ├─ Connection Pool (to backend services)
  ├─ Routing Engine (service type from config)
  └─ Redis Configuration (routing rules, realm_id mapping)
  ↓
Backend Services (AccountServer/VersionServer/GameServer)
```

## Quick Start

```bash
# Build
go build -o game-gateway ./cmd/gateway

# Run
./game-gateway -config config/config.yaml
```

## Configuration

Configuration file: `config/config.yaml`

### Main Configuration Items

- **Server**: Listen address, health check port, metrics port
- **Redis**: Connection settings, pool configuration
- **Backend Services**: Default service addresses
- **Connection Pool**: Max connections, timeouts, retry settings
- **Routing**: Refresh intervals for dynamic routing rules

### Environment Variables

- `LOG_LEVEL`: Logging level (debug, info, warn, error) - defaults to "info"
- `POD_NAME`: Kubernetes Pod name (auto-injected)
- `JAEGER_ENDPOINT`: Jaeger collector endpoint for tracing (optional)

### Configuration Validation

All configurations are validated on startup. Invalid configurations will cause the service to fail fast.

## Deployment

Supports Kubernetes Deployment with HPA auto-scaling.

See `deploy/` directory for details.

## Monitoring and Alerts

### Prometheus Metrics

The gateway exposes Prometheus metrics on port 9090 at `/metrics`:

- `game_gateway_connections_active` - Current active connections
- `game_gateway_connections_total` - Total connections established
- `game_gateway_sessions_active` - Current active sessions
- `game_gateway_routing_errors_total` - Routing errors by type
- `game_gateway_rate_limit_rejected_total` - Rate limit rejections
- `game_gateway_circuit_breaker_state` - Circuit breaker state per backend
- `game_gateway_request_latency_seconds` - Request latency histogram
- `game_gateway_messages_processed_total` - Messages processed by direction and service type
- `game_gateway_config_refresh_errors_total` - Configuration refresh errors

### Grafana Dashboard

A pre-configured Grafana dashboard is available in `deploy/monitoring.yaml`.

### Prometheus Alerts

Alert rules are defined in `deploy/prometheus-alerts.yaml`:

- High error rate
- Circuit breaker open
- High connection count
- Rate limit hits
- High latency
- Configuration refresh failures
- Pod down
- High memory/CPU usage

## Performance

### Benchmarks

Run benchmarks:
```bash
go test -bench=. -benchmem ./...
```

### Performance Characteristics

- **Connection Handling**: O(1) connection pool operations
- **Session Management**: Sharded maps for reduced lock contention
- **Memory**: Buffer pool reuse for reduced allocations
- **Concurrency**: Atomic operations for high-throughput scenarios

## Security

### Rate Limiting

- Global connection limit
- Per-IP connection limit
- Per-IP connection rate limit

### Input Validation

- Message size limits (configurable, default 1MB)
- ServerID range validation
- Protocol validation

## Configuration Hot Reload

Certain configuration values can be updated without restart:

- Connection pool settings
- Rate limiting settings
- Security settings
- Timeout values

Use the `UpdateConfig()` method or implement a file watcher using `config.HotReloadManager`.

## Troubleshooting

### Common Issues

1. **High connection count**: Check rate limiter settings
2. **Circuit breaker open**: Check backend service health
3. **Configuration refresh errors**: Check Redis connectivity
4. **Memory leaks**: Check connection pool cleanup

### Logs

Logs are structured using zap. Set `LOG_LEVEL` environment variable:
- `debug` - Detailed debugging information
- `info` - General information (default)
- `warn` - Warning messages
- `error` - Error messages only

