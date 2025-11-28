# Game Gateway

[![Go Version](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go)](https://golang.org/)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Kubernetes](https://img.shields.io/badge/Kubernetes-1.20+-326CE5?style=flat&logo=kubernetes&logoColor=white)](https://kubernetes.io/)

A high-performance, production-ready game gateway service implemented in Go, following industry best practices from Google, Netflix, and Uber. Designed for cloud-native environments with full observability, resilience, and scalability.

## 🚀 Quick Start

### Prerequisites

- Go 1.21 or later
- Kubernetes 1.20+ (for deployment)
- Redis (for dynamic routing configuration)

### Installation

```bash
# Clone the repository
git clone https://github.com/SkynetNext/game-gateway.git
cd game-gateway

# Build
go build -o game-gateway ./cmd/gateway

# Run
./game-gateway -config config/config.yaml
```

### Docker

```bash
docker build -t game-gateway:latest .
docker run -p 8080:8080 -p 9090:9090 game-gateway:latest
```

### Kubernetes

```bash
kubectl apply -f deploy/deployment.yaml
kubectl apply -f deploy/monitoring.yaml
```

## ✨ Features

### Core Functionality

- **Multi-Protocol Support**: HTTP, WebSocket, and custom binary TCP protocol
- **Session Management**: High-performance sharded in-memory storage with O(1) access
- **Connection Pooling**: TCP connection reuse with configurable limits and idle timeout
- **Dynamic Routing**: Redis-based routing rules with multiple strategies (round-robin, consistent hash, realm-based)
- **Protocol Forwarding**: Bidirectional message forwarding with protocol translation

### Production-Ready Features

- **🛡️ Resilience**
  - Circuit Breaker: Automatic backend failure detection and recovery
  - Retry Mechanism: Exponential backoff with configurable retries
  - Graceful Shutdown: Drain mode with connection cleanup
  - Health Checks: `/health` and `/ready` endpoints

- **🔒 Security**
  - Rate Limiting: Global and per-IP connection limits
  - Input Validation: Message size limits, protocol validation
  - DoS Protection: Configurable message size limits

- **📊 Observability**
  - Structured Logging: Zap-based JSON logging with Trace ID correlation
  - Prometheus Metrics: Comprehensive metrics collection
  - Distributed Tracing: OpenTelemetry support (Jaeger)
  - Access Logging: Batched access logs with Trace ID

- **⚙️ Operations**
  - Configuration Hot Reload: Update settings without restart
  - Kubernetes Integration: HPA support, Pod identification
  - Configuration Validation: Fail-fast on invalid config

## 📖 Documentation

- **[Architecture](./ARCHITECTURE.md)**: System architecture, components, and design decisions
- **[Performance](./PERFORMANCE.md)**: Performance characteristics, benchmarks, and tuning guide
- **[Logging Improvements](./docs/LOGGING_IMPROVEMENTS.md)**: Distributed tracing and log correlation
- **[Trace Propagation](./docs/TRACE_PROPAGATION.md)**: Trace ID propagation to backend services

## 🏗️ Architecture

```
┌─────────────┐
│   Clients   │
└──────┬──────┘
       │
       ▼
┌─────────────────────────────────────┐
│      Load Balancer (HAProxy)        │
└──────┬──────────────────────────────┘
       │
       ▼
┌─────────────────────────────────────┐
│    Game Gateway Cluster (HPA)       │
│  ┌──────────────────────────────┐  │
│  │  Session Management (Memory)  │  │
│  │  Connection Pool (TCP)       │  │
│  │  Routing Engine (Redis)       │  │
│  │  Protocol Handler             │  │
│  └──────────────────────────────┘  │
└──────┬──────────────────────────────┘
       │
       ▼
┌─────────────────────────────────────┐
│   Backend Services (Cluster)        │
│  • AccountServer                    │
│  • VersionServer                    │
│  • GameServer                       │
└─────────────────────────────────────┘
```

## 📊 Key Metrics

### Performance Targets (SLA)

- **Latency**: P95 < 100ms, P99 < 200ms
- **Throughput**: 10,000+ connections per pod
- **Availability**: 99.9% uptime
- **Error Rate**: < 0.1%

### Prometheus Metrics

- `game_gateway_connections_active` - Active connections
- `game_gateway_sessions_active` - Active sessions
- `game_gateway_request_latency_seconds` - Request latency histogram
- `game_gateway_routing_errors_total` - Routing errors by type
- `game_gateway_rate_limit_rejected_total` - Rate limit rejections
- `game_gateway_circuit_breaker_state` - Circuit breaker state

See [Monitoring](./docs/MONITORING.md) for complete metrics list.

## ⚙️ Configuration

### Configuration File

```yaml
server:
  listen_addr: ":8080"
  health_check_port: 9090
  metrics_port: 9091

redis:
  addr: "10.1.0.8:6379"
  key_prefix: "game-gateway:"

routing:
  refresh_interval: 10s

connection_pool:
  max_connections: 1000
  max_connections_per_service: 100
  idle_timeout: 5m

security:
  max_message_size: 1048576  # 1MB
  max_connections_per_ip: 10
  connection_rate_limit: 5

tracing:
  enable_trace_propagation: false  # Default: disabled for compatibility
```

### Environment Variables

- `LOG_LEVEL`: Logging level (debug, info, warn, error) - default: "info"
- `POD_NAME`: Kubernetes Pod name (auto-injected)
- `JAEGER_ENDPOINT`: Jaeger collector endpoint (optional)

See [Configuration Guide](./docs/CONFIGURATION.md) for details.

## 🔍 Monitoring

### Grafana Dashboard

Pre-configured dashboard available in `deploy/monitoring.yaml`:

- Request rate and latency
- Connection and session metrics
- Error rates and circuit breaker status
- Resource utilization

### Prometheus Alerts

Alert rules defined in `deploy/prometheus-alerts.yaml`:

- High error rate (> 1%)
- Circuit breaker open
- High connection count
- Rate limit hits
- High latency (P95 > 100ms)
- Pod failures

## 🧪 Testing

### Unit Tests

```bash
go test ./...
```

### Benchmarks

```bash
go test -bench=. -benchmem ./...
```

### Load Testing

```bash
cd tools/loadtest
go build -o loadtest main.go
./loadtest -host localhost -port 8080 -connections 100 -duration 60s
```

## 🚢 Deployment

### Kubernetes

```bash
# Deploy gateway
kubectl apply -f deploy/deployment.yaml

# Deploy monitoring
kubectl apply -f deploy/monitoring.yaml

# Deploy alerts
kubectl apply -f deploy/prometheus-alerts.yaml
```

### CI/CD

Integrated with Jenkins pipeline. See `hgame_pipeline/Jenkinsfile` for details.

## 🔧 Development

### Project Structure

```
game-gateway/
├── cmd/
│   └── gateway/          # Main application entry point
├── internal/
│   ├── gateway/          # Core gateway logic
│   ├── protocol/        # Protocol parsing and forwarding
│   ├── router/          # Routing engine
│   ├── pool/            # Connection pool
│   ├── session/         # Session management
│   ├── logger/          # Structured logging
│   ├── metrics/         # Prometheus metrics
│   ├── middleware/      # Access logging
│   └── tracing/        # OpenTelemetry tracing
├── config/              # Configuration files
├── deploy/              # Kubernetes manifests
├── tools/               # Utility tools
└── docs/                # Documentation
```

### Code Standards

- Follow Go best practices and idioms
- Use structured logging with context
- Include unit tests for new features
- Document exported functions
- Run `gofmt` and `golint` before commit

## 📈 Performance

### Benchmarks

- **Connection Pool**: O(1) operations, < 2μs per operation
- **Session Management**: O(1) access, < 1μs per operation
- **Rate Limiter**: < 200ns per check
- **Message Forwarding**: < 1ms latency (P95)

See [Performance Guide](./PERFORMANCE.md) for detailed benchmarks and tuning.

## 🛠️ Troubleshooting

### Common Issues

1. **High connection count**: Check rate limiter settings, consider scaling
2. **Circuit breaker open**: Check backend service health, review error logs
3. **Configuration refresh errors**: Check Redis connectivity and network
4. **Memory leaks**: Check connection pool cleanup, review session lifecycle
5. **High latency**: Check backend response times, network conditions
6. **Rate limit rejections**: Review rate limit settings, consider increasing limits

### Debug Mode

```bash
# Enable debug logging
LOG_LEVEL=debug ./game-gateway -config config/config.yaml

# View debug logs
kubectl logs -n hgame -l app=game-gateway --tail=100 -f
```

### Log Analysis

**Query by Trace ID** (Loki):
```logql
{app="game-gateway"} | json | trace_id="4bf92f3577b34da6a3ce929d0e0e4736"
```

**Query access logs**:
```logql
{app="game-gateway"} | json | msg="access_log" | status="error"
```

**Query errors**:
```logql
{app="game-gateway"} | json | level="error"
```

### Performance Troubleshooting

See [Performance Guide - Troubleshooting](../PERFORMANCE.md#troubleshooting-performance-issues) for detailed troubleshooting steps.

### Getting Help

- **Documentation**: See [Documentation Index](./docs/INDEX.md)
- **Issues**: [GitHub Issues](https://github.com/SkynetNext/game-gateway/issues)
- **Logs**: Check application logs with Trace ID correlation
- **Metrics**: Review Prometheus metrics and Grafana dashboards

## 🤝 Contributing

Contributions are welcome! Please:

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests
5. Submit a pull request

## 📄 License

MIT License - see [LICENSE](LICENSE) file for details.

## 🙏 Acknowledgments

- Inspired by industry best practices from Google, Netflix, and Uber
- Built with [zap](https://github.com/uber-go/zap) for logging
- Uses [OpenTelemetry](https://opentelemetry.io/) for distributed tracing

## 📞 Support

- **Issues**: [GitHub Issues](https://github.com/SkynetNext/game-gateway/issues)
- **Documentation**: See `docs/` directory
- **Performance**: See [PERFORMANCE.md](./PERFORMANCE.md)

---

**Built with ❤️ for high-performance game services**
