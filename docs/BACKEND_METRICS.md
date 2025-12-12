# Backend/Upstream Metrics Guide

## Overview

This document describes the backend/upstream metrics that follow industry-standard gateway monitoring practices (similar to Envoy, Kong, Traefik).

## Backend Metrics

### 1. Backend Requests Total
**Metric**: `game_gateway_backend_requests_total`  
**Type**: Counter  
**Labels**: `backend`, `status`  
**Description**: Total number of requests sent to backend services, categorized by status (success, error, timeout, etc.)

**Usage Example**:
```go
metrics.BackendRequestsTotal.WithLabelValues(backendAddr, "success").Inc()
metrics.BackendRequestsTotal.WithLabelValues(backendAddr, "error").Inc()
```

### 2. Backend Request Latency
**Metric**: `game_gateway_backend_request_latency_seconds`  
**Type**: Histogram  
**Labels**: `backend`  
**Description**: End-to-end latency from gateway to backend service (including connection, request, and response time)

**Buckets**: 1ms to 4s (exponential, 12 buckets)

**Usage Example**:
```go
startTime := time.Now()
// ... make request to backend ...
latency := time.Since(startTime)
metrics.BackendRequestLatency.WithLabelValues(backendAddr).Observe(latency.Seconds())
```

### 3. Backend Connection Latency
**Metric**: `game_gateway_backend_connection_latency_seconds`  
**Type**: Histogram  
**Labels**: `backend`  
**Description**: Time taken to establish connection to backend service

**Buckets**: 1ms to 1s (exponential, 10 buckets)

**Usage Example**:
```go
startTime := time.Now()
conn, err := dialer.DialContext(ctx, "tcp", backendAddr)
if err == nil {
    latency := time.Since(startTime)
    metrics.BackendConnectionLatency.WithLabelValues(backendAddr).Observe(latency.Seconds())
}
```

### 4. Backend Errors Total
**Metric**: `game_gateway_backend_errors_total`  
**Type**: Counter  
**Labels**: `backend`, `error_type`  
**Description**: Total number of errors when communicating with backend services

**Error Types**:
- `connection_failed`: Failed to establish connection
- `timeout`: Request timeout
- `read_error`: Error reading response
- `write_error`: Error writing request
- `protocol_error`: Protocol-level error
- `unexpected_response`: Unexpected response format

**Usage Example**:
```go
metrics.BackendErrorsTotal.WithLabelValues(backendAddr, "connection_failed").Inc()
metrics.BackendErrorsTotal.WithLabelValues(backendAddr, "timeout").Inc()
```

### 5. Backend Timeouts Total
**Metric**: `game_gateway_backend_timeouts_total`  
**Type**: Counter  
**Labels**: `backend`  
**Description**: Total number of backend request timeouts

**Usage Example**:
```go
if err == context.DeadlineExceeded {
    metrics.BackendTimeoutsTotal.WithLabelValues(backendAddr).Inc()
}
```

### 6. Backend Retries Total
**Metric**: `game_gateway_backend_retries_total`  
**Type**: Counter  
**Labels**: `backend`  
**Description**: Total number of retry attempts for backend requests

**Usage Example**:
```go
// In retry logic
metrics.BackendRetriesTotal.WithLabelValues(backendAddr).Inc()
```

### 7. Backend Health Status
**Metric**: `game_gateway_backend_health_status`  
**Type**: Gauge  
**Labels**: `backend`  
**Description**: Health status of backend service (1=healthy, 0=unhealthy)

**Usage Example**:
```go
if isHealthy {
    metrics.BackendHealthStatus.WithLabelValues(backendAddr).Set(1)
} else {
    metrics.BackendHealthStatus.WithLabelValues(backendAddr).Set(0)
}
```

### 8. Backend Bytes Transmitted
**Metric**: `game_gateway_backend_bytes_transmitted_total`  
**Type**: Counter  
**Labels**: `backend`, `direction`  
**Description**: Total bytes transmitted to/from backend services

**Directions**:
- `request`: Bytes sent to backend
- `response`: Bytes received from backend

**Usage Example**:
```go
metrics.BackendBytesTransmitted.WithLabelValues(backendAddr, "request").Add(float64(requestSize))
metrics.BackendBytesTransmitted.WithLabelValues(backendAddr, "response").Add(float64(responseSize))
```

## Comparison with Industry Standards

### Envoy Proxy
- `envoy_cluster_upstream_rq_total` → `game_gateway_backend_requests_total`
- `envoy_cluster_upstream_rq_time` → `game_gateway_backend_request_latency_seconds`
- `envoy_cluster_upstream_cx_connect_ms` → `game_gateway_backend_connection_latency_seconds`
- `envoy_cluster_upstream_rq_xx` → `game_gateway_backend_errors_total` (by error type)

### Kong Gateway
- `kong_http_requests_total` → `game_gateway_backend_requests_total`
- `kong_latency` → `game_gateway_backend_request_latency_seconds`
- `kong_upstream_latency` → Backend-specific latency

### Traefik
- `traefik_service_requests_total` → `game_gateway_backend_requests_total`
- `traefik_service_request_duration_seconds` → `game_gateway_backend_request_latency_seconds`

## Prometheus Queries

### Backend Success Rate
```promql
sum(rate(game_gateway_backend_requests_total{status="success"}[5m])) by (backend) /
sum(rate(game_gateway_backend_requests_total[5m])) by (backend) * 100
```

### Backend Error Rate
```promql
sum(rate(game_gateway_backend_errors_total[5m])) by (backend) /
sum(rate(game_gateway_backend_requests_total[5m])) by (backend) * 100
```

### Backend P95 Latency
```promql
histogram_quantile(0.95, 
  sum(rate(game_gateway_backend_request_latency_seconds_bucket[5m])) by (le, backend)
)
```

### Backend Connection Time P99
```promql
histogram_quantile(0.99, 
  sum(rate(game_gateway_backend_connection_latency_seconds_bucket[5m])) by (le, backend)
)
```

### Backend Timeout Rate
```promql
sum(rate(game_gateway_backend_timeouts_total[5m])) by (backend) /
sum(rate(game_gateway_backend_requests_total[5m])) by (backend) * 100
```

### Backend Retry Rate
```promql
sum(rate(game_gateway_backend_retries_total[5m])) by (backend) /
sum(rate(game_gateway_backend_requests_total[5m])) by (backend) * 100
```

### Unhealthy Backends
```promql
game_gateway_backend_health_status == 0
```

### Backend Throughput (Bytes/sec)
```promql
sum(rate(game_gateway_backend_bytes_transmitted_total[1m])) by (backend, direction)
```

## Implementation Checklist

- [ ] Record `BackendRequestsTotal` when routing to backend
- [ ] Record `BackendRequestLatency` for end-to-end request time
- [ ] Record `BackendConnectionLatency` when establishing connections
- [ ] Record `BackendErrorsTotal` for all error types
- [ ] Record `BackendTimeoutsTotal` on timeout
- [ ] Record `BackendRetriesTotal` in retry logic
- [ ] Update `BackendHealthStatus` based on health checks
- [ ] Record `BackendBytesTransmitted` for request/response sizes

## Best Practices

1. **Always label by backend address**: This allows per-backend analysis
2. **Use appropriate error types**: Categorize errors for better debugging
3. **Record latency at key points**: Connection time vs total request time
4. **Track retries separately**: Helps identify flaky backends
5. **Monitor health status**: Proactive detection of unhealthy backends
6. **Track bytes for capacity planning**: Understand bandwidth usage

