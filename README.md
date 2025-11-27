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

