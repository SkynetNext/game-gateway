# Monitoring Guide

## Overview

Game Gateway provides comprehensive observability through structured logging, Prometheus metrics, and distributed tracing. This guide covers monitoring setup, key metrics, and alerting.

## Observability Stack

### Components

1. **Structured Logging**: Zap-based JSON logs with Trace ID correlation
2. **Prometheus Metrics**: Standard Prometheus metrics endpoint
3. **Distributed Tracing**: OpenTelemetry with Jaeger integration
4. **Access Logging**: Batched access logs with Trace ID

### Architecture

```
Game Gateway Pods
  │
  ├─► Prometheus (scrapes /metrics)
  │   └─► Grafana (dashboards)
  │
  ├─► Loki (collects logs)
  │   └─► Grafana (log queries)
  │
  └─► Jaeger (traces)
      └─► Jaeger UI (trace visualization)
```

## Prometheus Metrics

### Endpoint

- **Path**: `/metrics`
- **Port**: `9091` (configurable)
- **Format**: Prometheus text format

### Key Metrics

#### Connection Metrics

| Metric | Type | Description | Labels |
|--------|------|-------------|--------|
| `game_gateway_connections_active` | Gauge | Current active connections | - |
| `game_gateway_connections_total` | Counter | Total connections established | - |
| `game_gateway_rate_limit_rejected_total` | Counter | Connections rejected by rate limiter | - |

#### Session Metrics

| Metric | Type | Description | Labels |
|--------|------|-------------|--------|
| `game_gateway_sessions_active` | Gauge | Current active sessions | - |

#### Routing Metrics

| Metric | Type | Description | Labels |
|--------|------|-------------|--------|
| `game_gateway_routing_errors_total` | Counter | Routing errors | `error_type` |
| `game_gateway_request_latency_seconds` | Histogram | Request latency | `server_type` |

#### Backend Pool Metrics

| Metric | Type | Description | Labels |
|--------|------|-------------|--------|
| `game_gateway_backend_pool_active` | Gauge | Active connections in pool | `backend` |
| `game_gateway_backend_pool_idle` | Gauge | Idle connections in pool | `backend` |

#### Message Metrics

| Metric | Type | Description | Labels |
|--------|------|-------------|--------|
| `game_gateway_messages_processed_total` | Counter | Messages processed | `direction`, `service_type` |

#### Circuit Breaker Metrics

| Metric | Type | Description | Labels |
|--------|------|-------------|--------|
| `game_gateway_circuit_breaker_state` | Gauge | Circuit breaker state (0=closed, 1=open) | `backend` |

#### Configuration Metrics

| Metric | Type | Description | Labels |
|--------|------|-------------|--------|
| `game_gateway_config_refresh_errors_total` | Counter | Configuration refresh errors | - |
| `game_gateway_pool_cleanup_errors_total` | Counter | Connection pool cleanup errors | - |

### Example Queries

#### Request Rate

```promql
sum(rate(game_gateway_connections_total[5m]))
```

#### Error Rate

```promql
sum(rate(game_gateway_routing_errors_total[5m])) / 
sum(rate(game_gateway_connections_total[5m])) * 100
```

#### P95 Latency

```promql
histogram_quantile(0.95, 
  sum(rate(game_gateway_request_latency_seconds_bucket[5m])) by (le)
)
```

#### Connection Pool Utilization

```promql
sum(game_gateway_backend_pool_active) / 
sum(game_gateway_backend_pool_active + game_gateway_backend_pool_idle) * 100
```

#### Circuit Breaker Status

```promql
sum(game_gateway_circuit_breaker_state) > 0
```

## Grafana Dashboard

### Pre-configured Dashboard

Location: `deploy/monitoring.yaml`

### Dashboard Panels

1. **Request Rate**: Connections per second
2. **Error Rate**: Percentage of failed requests
3. **Active Connections**: Current active connections
4. **Pod Count**: Number of running pods
5. **Request Rate by Protocol**: HTTP vs TCP
6. **Request Latency**: P50/P95/P99 percentiles
7. **Status Code Distribution**: Success vs error rates
8. **Throughput**: Bytes in/out per second
9. **Upstream Health**: Backend service health status
10. **Upstream Latency**: Backend response times
11. **Security Blocks**: Rate limit and security rejections
12. **Connection Statistics**: Connection rate and active count
13. **Rate Limit Hits**: Number of rate limit rejections

### Import Dashboard

```bash
# Apply monitoring resources
kubectl apply -f deploy/monitoring.yaml

# Dashboard will be auto-imported if Grafana sidecar is configured
# Otherwise, manually import from ConfigMap
kubectl get configmap game-gateway-grafana-dashboard -n monitoring -o jsonpath='{.data.dashboard\.json}' > dashboard.json
```

## Logging

### Log Format

Structured JSON logs with Trace ID correlation:

```json
{
  "level": "info",
  "timestamp": "2024-01-15T10:30:45.123Z",
  "msg": "session established",
  "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
  "span_id": "00f067aa0ba902b7",
  "session_id": 12345,
  "remote_addr": "192.168.1.100:54321",
  "backend_addr": "10.1.0.8:8080",
  "server_type": 200,
  "world_id": 1,
  "latency": "45ms"
}
```

### Access Logs

Batched access logs with Trace ID:

```json
{
  "level": "info",
  "timestamp": "2024-01-15T10:30:45.123Z",
  "msg": "access_log",
  "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
  "span_id": "00f067aa0ba902b7",
  "remote_addr": "192.168.1.100:54321",
  "session_id": 12345,
  "server_type": 200,
  "world_id": 1,
  "backend_addr": "10.1.0.8:8080",
  "duration_ms": 45,
  "status": "success"
}
```

### Log Levels

- **debug**: Detailed debugging information
- **info**: General information (default)
- **warn**: Warning messages
- **error**: Error messages only

### Querying Logs in Loki

#### By Trace ID

```logql
{app="game-gateway"} | json | trace_id="4bf92f3577b34da6a3ce929d0e0e4736"
```

#### Access Logs

```logql
{app="game-gateway"} | json | msg="access_log"
```

#### Error Logs

```logql
{app="game-gateway"} | json | level="error"
```

#### By Session ID

```logql
{app="game-gateway"} | json | session_id="12345"
```

## Distributed Tracing

### Setup

1. **Enable Jaeger** (optional):
   ```bash
   export JAEGER_ENDPOINT=http://jaeger:14268/api/traces
   ```

2. **View Traces**:
   - Open Jaeger UI
   - Search by service: `game-gateway`
   - Filter by Trace ID or time range

### Trace Context

- **Trace ID**: Unique identifier for entire request
- **Span ID**: Identifier for individual operation
- **Propagation**: Optional (disabled by default for compatibility)

### Trace Spans

- `gateway.handle_connection`: Connection handling
- `gateway.handle_tcp_connection`: TCP protocol handling
- `gateway.route_to_backend`: Backend routing
- `gateway.forward_connection`: Message forwarding

## Alerting

### Prometheus Alerts

Location: `deploy/prometheus-alerts.yaml`

### Alert Rules

1. **High Error Rate**
   - Condition: Error rate > 1% for 5 minutes
   - Severity: Critical

2. **Circuit Breaker Open**
   - Condition: Any circuit breaker open for 1 minute
   - Severity: Warning

3. **High Connection Count**
   - Condition: Active connections > 80% of max for 5 minutes
   - Severity: Warning

4. **Rate Limit Hits**
   - Condition: Rate limit rejections > 10% for 5 minutes
   - Severity: Warning

5. **High Latency**
   - Condition: P95 latency > 100ms for 5 minutes
   - Severity: Warning

6. **Config Refresh Failures**
   - Condition: Config refresh errors > 0 for 5 minutes
   - Severity: Warning

7. **Pod Down**
   - Condition: Pod not ready for 2 minutes
   - Severity: Critical

8. **High Resource Usage**
   - Condition: CPU > 80% or Memory > 80% for 5 minutes
   - Severity: Warning

### Alert Configuration

```yaml
groups:
  - name: game-gateway
    interval: 30s
    rules:
      - alert: HighErrorRate
        expr: |
          sum(rate(game_gateway_routing_errors_total[5m])) / 
          sum(rate(game_gateway_connections_total[5m])) > 0.01
        for: 5m
        labels:
          severity: critical
        annotations:
          summary: "High error rate detected"
          description: "Error rate is {{ $value | humanizePercentage }}"
```

## Health Checks

### Endpoints

- **Liveness**: `GET /health`
  - Returns: `200 OK` if service is running
  - Used by: Kubernetes liveness probe

- **Readiness**: `GET /ready`
  - Returns: `200 OK` if service is ready to accept traffic
  - Used by: Kubernetes readiness probe

### Health Check Configuration

```yaml
livenessProbe:
  httpGet:
    path: /health
    port: 9090
  initialDelaySeconds: 15
  periodSeconds: 20
  timeoutSeconds: 5
  failureThreshold: 3

readinessProbe:
  httpGet:
    path: /ready
    port: 9090
  initialDelaySeconds: 5
  periodSeconds: 5
  timeoutSeconds: 3
  failureThreshold: 3
```

## Monitoring Best Practices

### 1. Set Up Dashboards

- Import pre-configured Grafana dashboard
- Customize based on your needs
- Set up alerting rules

### 2. Monitor Key Metrics

- Request rate and latency
- Error rates
- Connection pool utilization
- Circuit breaker status

### 3. Log Analysis

- Use Trace ID for log correlation
- Query access logs for traffic analysis
- Monitor error logs for issues

### 4. Alerting

- Set up alerts for critical metrics
- Use appropriate severity levels
- Configure notification channels

### 5. Capacity Planning

- Monitor resource utilization
- Track connection and session counts
- Plan scaling based on trends

## Troubleshooting

### Metrics Not Appearing

1. Check ServiceMonitor is configured:
   ```bash
   kubectl get servicemonitor game-gateway -n monitoring
   ```

2. Check Prometheus is scraping:
   ```bash
   kubectl port-forward -n monitoring svc/prometheus 9090:9090
   # Open http://localhost:9090/targets
   ```

### Logs Not Appearing in Loki

1. Check log collection:
   ```bash
   kubectl logs -n hgame -l app=game-gateway | head -20
   ```

2. Check Loki configuration:
   ```bash
   kubectl get configmap -n monitoring | grep loki
   ```

### Traces Not Appearing

1. Check Jaeger endpoint:
   ```bash
   kubectl get pods -n monitoring | grep jaeger
   ```

2. Verify environment variable:
   ```bash
   kubectl get deployment game-gateway -n hgame -o yaml | grep JAEGER
   ```

---

**Last Updated**: 2024-01-15

