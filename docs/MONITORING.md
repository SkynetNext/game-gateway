# Monitoring Guide

## Overview

Game Gateway provides comprehensive observability through structured logging, Prometheus metrics, and distributed tracing. This guide covers monitoring setup, key metrics, and alerting.

## Observability Stack

### Components

1. **Structured Logging**: Zap-based JSON logs with Trace ID correlation
2. **Prometheus Metrics**: Standard Prometheus metrics endpoint
3. **Distributed Tracing**: OpenTelemetry with OTLP protocol
4. **Access Logging**: Structured access logs with Trace ID

### Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                      game-gateway Pod                            │
│  ┌─────────────────────────────────────────────────────────────┐│
│  │ Zap Logger ──→ stdout (JSON + trace_id + service.instance.id)││
│  │ OTel SDK ──OTLP──→ OTel Collector:4317                       ││
│  │ Prometheus Client ←── scrape ── Prometheus                   ││
│  └─────────────────────────────────────────────────────────────┘│
└─────────────────────────────────────────────────────────────────┘
                              │
         ┌────────────────────┼────────────────────┐
         ▼                    ▼                    ▼
┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐
│   Fluent Bit    │  │  OTel Collector │  │   Prometheus    │
│  (DaemonSet)    │  │                 │  │                 │
└────────┬────────┘  └────────┬────────┘  └────────┬────────┘
         │                    │                    │
         ├─────┬──────┬───────┤                    │
         │     │      │       ▼                    ▼
         │     │      │  ┌─────────────────┐  ┌─────────────────┐
         │     │      │  │      Tempo      │  │   Prometheus    │
         │     │      │  │    (Traces)     │  │    (Metrics)    │
         │     │      │  └────────┬────────┘  └────────┬────────┘
         ▼     ▼      ▼           │                    │
    ┌────────┐ ┌────┐ ┌────┐     │                    │
    │  Loki  │ │ ES │ │ S3 │     │                    │
    │ (实时) │ │(分析)│ │(归档)│     │                    │
    └────┬───┘ └─┬──┘ └────┘     │                    │
         │       │                │                    │
         └───────┴────────────────┼────────────────────┘
                                  ▼
                          ┌─────────────────┐
                          │     Grafana     │
                          │ (Unified View)  │
                          └─────────────────┘
```

### Data Flow

1. **Logs**: Application → stdout (JSON) → Fluent Bit (DaemonSet) → Loki/ES/S3 → Grafana
2. **Traces**: Application → OTLP → OTel Collector → Tempo → Grafana
3. **Metrics**: Application → Prometheus (scrape) → Grafana

### Latency Breakdown

#### Log Pipeline Latency

| Stage | Component | Typical Latency | Notes |
|-------|-----------|----------------|-------|
| **1. Application → stdout** | Zap Logger | **< 1ms** | In-memory write, negligible |
| **2. stdout → Container Log** | Docker/containerd | **0-5ms** | Buffered write, usually instant |
| **3. Container Log → Fluent Bit** | Fluent Bit Tail Input | **5-10s** | `Refresh_Interval=10s` (configurable) |
| **4. Fluent Bit Processing** | Filters & Parsers | **1-5ms** | Kubernetes metadata enrichment |
| **5. Fluent Bit → Loki** | Network + Write | **10-50ms** | Batch flush every 10s, network RTT |
| **5. Fluent Bit → Elasticsearch** | Network + Bulk Index | **50-200ms** | Bulk API, depends on ES load |
| **6. Loki/ES → Query** | Grafana/Kibana | **100-500ms** | Query execution time |

**Total End-to-End Latency:**
- **Loki**: ~10-15 seconds (near real-time)
- **Elasticsearch**: ~10-15 seconds (near real-time)
- **Query Time**: Additional 100-500ms when searching

**Optimization Tips:**
- Reduce `Refresh_Interval` in Fluent Bit (trade-off: more CPU)
- Reduce `Flush` interval (trade-off: more network calls)
- Use `Read_from_Head=False` to avoid processing old logs
- Increase ES bulk size for better throughput

#### Trace Pipeline Latency

| Stage | Component | Typical Latency |
|-------|-----------|----------------|
| **1. Application → OTLP** | OTel SDK | **< 1ms** |
| **2. OTLP → Collector** | Network | **1-5ms** |
| **3. Collector → Tempo** | Network + Write | **10-50ms** |
| **4. Tempo → Query** | Grafana | **100-300ms** |

**Total Trace Latency**: ~110-350ms (much faster than logs)

#### Metrics Pipeline Latency

| Stage | Component | Typical Latency |
|-------|-----------|----------------|
| **1. Application → Metrics** | Prometheus Client | **< 1ms** |
| **2. Prometheus Scrape** | Polling Interval | **15-30s** (configurable) |
| **3. Prometheus → Query** | Grafana | **50-200ms** |

**Total Metrics Latency**: ~15-30 seconds (polling-based)

### Correlation

- **Logs ↔ Traces**: Logs contain `trace_id` and `span_id` fields
- **Traces ↔ Logs**: Grafana Tempo configured with "Trace to Logs" feature
- **Traces ↔ Metrics**: Tempo configured with "Trace to Metrics" feature
- **All data**: Unified by `service.name`, `service.namespace`, `service.instance.id`

### Log Collection: Fluent Bit

**Why Fluent Bit?**

Fluent Bit is chosen over Promtail for enterprise-grade log collection:

- **Multi-destination**: Simultaneously ship logs to Loki (real-time), Elasticsearch (analysis), and S3 (archive)
- **Performance**: Written in C, minimal resource footprint (~450KB memory)
- **CNCF Graduated**: Industry-standard with broad enterprise adoption
- **Rich Processing**: Built-in parsers, filters, and data enrichment
- **ELK Compatible**: Seamless integration with existing Elasticsearch infrastructure

**Key Features:**

- **Real-time**: Loki for fast querying and troubleshooting
- **Analysis**: Elasticsearch for complex queries and long-term analysis
- **Archive**: S3 for compliance and cost-effective long-term storage
- **Filtering**: Pre-process and enrich logs before shipping
- **Buffering**: Reliable delivery with disk-based buffering

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

