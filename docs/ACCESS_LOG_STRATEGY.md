# Access Log Strategy

## Overview

Game Gateway follows **industry best practices** (Envoy, Kong, Istio) for access logging:

- **1 access log per connection** (not per request)
- Logged at connection close with complete lifecycle statistics
- All operations tracked via distributed tracing (spans)
- **Reduces log volume by 50-70%** while maintaining full observability

## Design Principles

### Logs vs Traces

| Event Type | Access Log | Trace Span | Reason |
|-----------|-----------|-----------|---------|
| Connection established | ❌ No | ✅ Yes | Span tracks timing |
| Request processing | ❌ No | ✅ Yes | Span provides details |
| Backend connection | ❌ No | ✅ Yes | Span shows latency |
| Connection closed | ✅ **YES** | ✅ Yes | **Single summary log** |

### Why This Approach?

**Following Envoy/Kong/Istio**:
- **Envoy**: 1 access log per request (HTTP) or connection (TCP)
- **Kong**: 1 access log per request, configurable sampling
- **Istio**: Based on Envoy, same strategy
- **NGINX**: 1 access log per request

**For TCP/long-lived connections** (like game sessions):
- 1 access log per connection is the standard
- Trace spans provide detailed operation timeline
- Access log provides business metrics (SLA, billing)

## Implementation

### Connection Statistics Tracking

```go
type connectionStats struct {
    sessionID   int64   // Game session ID
    backendAddr string  // Backend server address
    serverType  int     // Server type (game/chat/etc)
    worldID     int     // World ID
    bytesIn     int64   // Total bytes received
    bytesOut    int64   // Total bytes sent
    status      string  // Final status
    errorMsg    string  // Error message if any
}
```

### Access Log Structure

Logged once per connection at connection close:

```json
{
  "@timestamp": "2025-11-30T02:00:00.000Z",
  "timestamp": 1764439135838694416,
  "level": "INFO",
  "message": "access_log",
  "trace_id": "198b9595f4fc021850e0259c5e1d40cc",
  "span_id": "1036076d2c285778",
  "service.name": "game-gateway",
  "service.namespace": "hgame",
  "service.instance.id": "game-gateway-5b499f88bf-62zdw",
  "service.version": "dev",
  "client.address": "119.123.71.9:50452",
  "server.address": "hgame-game-0.hgame-game.hgame.svc.cluster.local:19806",
  "session.id": 6899911738378616833,
  "server.type": 200,
  "world.id": 1,
  "duration_ns": 40123456789,
  "duration_ms": 40123.456,
  "network.io.receive": 8192,
  "network.io.send": 16384,
  "status": "success",
  "k8s.namespace.name": "hgame",
  "k8s.pod.name": "game-gateway-5b499f88bf-62zdw",
  "k8s.labels.app": "game-gateway"
}
```

### Status Values

| Status | Description | Example |
|--------|-------------|---------|
| `success` | Connection completed successfully | Normal session |
| `closed` | Connection closed normally | Client disconnect |
| `rejected` | Connection rejected | Rate limit, invalid protocol |
| `error` | Connection failed | Backend error, timeout |

## Trace Structure

Distributed tracing provides detailed operation timeline:

**TCP Mode**:
```
Trace: 198b9595f4fc021850e0259c5e1d40cc (40.02s total)
├─ gateway.handle_connection (40.02s)           ← Root span
│  ├─ gateway.handle_tcp_connection (10.02s)    ← TCP processing
│  │  ├─ gateway.route_to_backend (1ms)         ← Routing & connection
│  │  └─ gateway.forward_connection (10.01s)    ← Data forwarding
│  └─ Access Log ✅                              ← Single log at end
```

**gRPC Mode**:
```
Trace: 198b9595f4fc021850e0259c5e1d40cc (40.02s total)
├─ gateway.handle_connection (40.02s)           ← Root span
│  ├─ gateway.handle_tcp_connection (10.02s)    ← TCP processing
│  │  ├─ gateway.route_to_backend (1ms)         ← Routing
│  │  │  └─ gateway.grpc_connect (50ms)         ← gRPC connection (first time)
│  │  └─ gateway.grpc_forward (10.01s)          ← gRPC forwarding
│  └─ Access Log ✅                              ← Single log at end
```

**Key Points**:
- Root span: Entire connection lifecycle
- Child spans: Detailed operations (routing, forwarding, gRPC)
- Access log: Business metrics summary
- Error recording: All errors recorded to spans for debugging

## Benefits

### 1. Reduced Log Volume

**Before** (logging per operation):
```
01:58:19 - Connection established
01:58:20 - Request processed
01:58:25 - Request processed
01:58:30 - Request processed
01:58:59 - Connection closed
```
**5 logs per connection** 

**After** (single log):
```
01:58:59 - access_log (duration: 40s, bytes_in: 8192, bytes_out: 16384)
```
**1 log per connection** = **80% reduction**

### 2. Complete Lifecycle View

Single log contains:
- Total connection duration
- Total bytes transferred
- Session information
- Final status
- Error (if any)

### 3. Query Simplification

**Loki/ES Query**:
```logql
# Find all connections from specific client
{job="fluent-bit"} | json | client_address="119.123.71.9"

# Find slow connections (> 30s)
{job="fluent-bit"} | json | body="access_log" | duration_ms > 30000

# Find failed connections
{job="fluent-bit"} | json | status="error"
```

**Single log = simpler queries**

### 4. Better for High Concurrency

- Less disk I/O
- Faster indexing
- Reduced storage cost
- Better query performance

## Migration Notes

### What Changed

**Removed**:
- `LogSessionEstablished()` - No longer needed
- `LogSessionClosed()` - Merged into connection log
- Individual operation logs - Replaced by trace spans

**Added**:
- `connectionStats` struct - Shared statistics
- Single access log in `handleConnection` defer
- Comprehensive error tracking

### Compatibility

**Trace ID Correlation**:
- All logs still have `trace_id` and `span_id`
- Can correlate single access log with all trace spans
- No loss of observability

**Metrics**:
- All Prometheus metrics unchanged
- `gateway_connections_total`, `gateway_active_sessions`, etc. still tracked

## Querying Examples

### Loki (LogQL)

```logql
# All access logs
{job="fluent-bit"} | json | message="access_log"

# Successful connections
{job="fluent-bit"} | json | message="access_log" | status="success"

# Failed connections with errors
{job="fluent-bit"} | json | message="access_log" | status="error"

# Slow connections (> 10s)
{job="fluent-bit"} | json | message="access_log" | duration_ms > 10000

# High data transfer (> 1MB)
{job="fluent-bit"} | json | message="access_log" | network_io_receive > 1048576

# Specific trace correlation
{job="fluent-bit"} | json | trace_id="198b9595f4fc021850e0259c5e1d40cc"
```

### Elasticsearch (KQL)

```kql
# All access logs
message:"access_log"

# Successful connections
message:"access_log" AND status:"success"

# Failed connections
message:"access_log" AND status:"error"

# Slow connections
message:"access_log" AND duration_ms > 10000

# Specific client
message:"access_log" AND client.address:"119.123.71.9"

# Time range + trace correlation
message:"access_log" AND trace_id:"198b9595f4fc021850e0259c5e1d40cc" AND @timestamp >= "2025-11-30T00:00:00"
```

## Best Practices

### 1. Use Trace ID for Debugging

When debugging a specific connection:
1. Find the connection's trace_id from access log
2. Query Tempo/Grafana for detailed span timeline
3. See exact timing of each operation

### 2. Access Logs for Business Metrics

Access logs are for:
- SLA monitoring (duration_ms)
- Billing/usage tracking (bytes_in, bytes_out)
- Error rate monitoring (status)
- Client analysis (client.address)

### 3. Trace Spans for Performance Analysis

Trace spans are for:
- Performance bottleneck identification
- Service dependency mapping
- Request flow visualization
- Detailed error diagnosis

### 4. Metrics for Real-time Monitoring

Prometheus metrics are for:
- Real-time dashboards
- Alerting rules
- Capacity planning
- SLI/SLO tracking

## References

- [Envoy Access Logging](https://www.envoyproxy.io/docs/envoy/latest/configuration/observability/access_log/usage)
- [Kong Logging](https://docs.konghq.com/gateway/latest/reference/configuration/#logging)
- [Istio Telemetry](https://istio.io/latest/docs/tasks/observability/logs/access-log/)
- [OpenTelemetry Logging](https://opentelemetry.io/docs/specs/otel/logs/)

