# Game Gateway Architecture

## Table of Contents

1. [Overview](#overview)
2. [System Architecture](#system-architecture)
3. [Component Design](#component-design)
4. [Data Flow](#data-flow)
5. [Protocol Design](#protocol-design)
6. [Design Decisions](#design-decisions)
7. [Technology Stack](#technology-stack)
8. [Deployment Architecture](#deployment-architecture)
9. [Scalability](#scalability)
10. [Reliability](#reliability)

## Overview

Game Gateway is a cloud-native, high-performance gateway service designed to handle client connections and route them to appropriate backend services. It follows industry best practices from Google SRE, Netflix, and Uber for building production-ready microservices.

### Design Principles

1. **High Performance**: O(1) operations, minimal allocations, efficient I/O
2. **Resilience**: Circuit breakers, retries, graceful degradation
3. **Observability**: Structured logging, metrics, distributed tracing
4. **Scalability**: Horizontal scaling with HPA, stateless design
5. **Security**: Rate limiting, input validation, DoS protection

## System Architecture

### High-Level Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    External Load Balancer                    │
│                    (HAProxy / Nginx)                        │
└────────────────────────┬────────────────────────────────────┘
                         │
         ┌───────────────┼───────────────┐
         │               │               │
         ▼               ▼               ▼
┌──────────────┐ ┌──────────────┐ ┌──────────────┐
│ Gateway Pod  │ │ Gateway Pod  │ │ Gateway Pod  │
│  (HPA)       │ │  (HPA)       │ │  (HPA)       │
└──────┬───────┘ └──────┬───────┘ └──────┬───────┘
       │                │                │
       └────────────────┼────────────────┘
                        │
        ┌───────────────┼───────────────┐
        │               │               │
        ▼               ▼               ▼
┌──────────────┐ ┌──────────────┐ ┌──────────────┐
│ AccountServer│ │VersionServer │ │  GameServer  │
│  (Cluster)   │ │  (Cluster)   │ │  (Cluster)   │
└──────────────┘ └──────────────┘ └──────────────┘
```

### Component Interaction

```
┌─────────────┐
│   Client    │
└──────┬──────┘
       │ TCP Connection
       ▼
┌─────────────────────────────────────────────────┐
│              Game Gateway Pod                   │
│  ┌──────────────────────────────────────────┐  │
│  │  Protocol Sniffer                        │  │
│  │  • HTTP/WebSocket/TCP detection          │  │
│  └──────────────┬───────────────────────────┘  │
│                 │                               │
│  ┌──────────────▼───────────────────────────┐  │
│  │  Connection Handler                      │  │
│  │  • Rate Limiting (Global + Per-IP)     │  │
│  │  • IP-based Connection Limits           │  │
│  └──────────────┬───────────────────────────┘  │
│                 │                               │
│  ┌──────────────▼───────────────────────────┐  │
│  │  Protocol Parser                        │  │
│  │  • Extract ServerID (serverType/worldID)│  │
│  │  • Parse Message Header                 │  │
│  └──────────────┬───────────────────────────┘  │
│                 │                               │
│  ┌──────────────▼───────────────────────────┐  │
│  │  Router                                  │  │
│  │  • Query Redis for routing rules        │  │
│  │  • Select backend endpoint              │  │
│  │  • Round-robin / Consistent Hash        │  │
│  └──────────────┬───────────────────────────┘  │
│                 │                               │
│  ┌──────────────▼───────────────────────────┐  │
│  │  Connection Pool                        │  │
│  │  • Get/Reuse TCP connection            │  │
│  │  • Circuit Breaker check               │  │
│  └──────────────┬───────────────────────────┘  │
│                 │                               │
│  ┌──────────────▼───────────────────────────┐  │
│  │  Session Manager                        │  │
│  │  • Create session (sharded map)         │  │
│  │  • Track client ↔ backend mapping      │  │
│  └──────────────┬───────────────────────────┘  │
│                 │                               │
│  ┌──────────────▼───────────────────────────┐  │
│  │  Message Forwarder                      │  │
│  │  • Bidirectional forwarding             │  │
│  │  • Protocol translation                │  │
│  └──────────────────────────────────────────┘  │
└─────────────────────────────────────────────────┘
       │
       │ TCP Connection (Pooled)
       ▼
┌──────────────┐
│ Backend      │
│ Service      │
└──────────────┘
```

## Component Design

### 1. Protocol Handler (`internal/protocol`)

**Responsibility**: Protocol detection, parsing, and message forwarding

**Key Features**:
- Multi-protocol support (HTTP/WebSocket/TCP)
- Binary protocol parsing with buffer pool reuse
- Message size validation (DoS protection)
- Protocol translation (client ↔ backend)

**Design Decisions**:
- **Buffer Pool**: Use `sync.Pool` for messages ≤ 8KB to reduce GC pressure
- **Batch I/O**: Use `io.ReadFull` to read complete headers in one call
- **Protocol Extension**: Support Trace ID propagation (optional, configurable)

### 2. Session Management (`internal/session`)

**Responsibility**: In-memory session storage and lifecycle management

**Key Features**:
- Sharded maps (16 shards) for reduced lock contention
- O(1) session lookup and update
- Automatic cleanup of idle sessions
- Session state tracking (connected, disconnected)

**Design Decisions**:
- **Sharding**: Use low 4 bits of sessionID for shard selection
- **No External Storage**: In-memory only for performance (sessions are ephemeral)
- **Global Uniqueness**: SessionID = CRC32(PodName) + Timestamp + Sequence

### 3. Connection Pool (`internal/pool`)

**Responsibility**: TCP connection reuse to backend services

**Key Features**:
- Per-service connection pools with limits
- Idle connection timeout and cleanup
- O(1) connection retrieval using channels
- Connection health monitoring

**Design Decisions**:
- **Channel-based**: Use channels for O(1) get/put operations
- **Per-Service Pools**: Isolate connections by backend service
- **Idle Timeout**: Close idle connections to free resources

### 4. Router (`internal/router`)

**Responsibility**: Dynamic routing based on service type and world ID

**Key Features**:
- Redis-based routing rules (hot reload)
- Multiple routing strategies:
  - Round-robin (default)
  - Consistent hash (future)
  - Realm-based (worldID mapping)
- Wildcard support (worldID=0)

**Design Decisions**:
- **Redis Storage**: Dynamic configuration without code changes
- **Hot Reload**: Pub/sub for real-time updates
- **Fallback**: Support worldID=0 as wildcard

### 5. Redis Client (`internal/redis`)

**Responsibility**: Configuration management and hot reload

**Key Features**:
- Load routing rules and realm mappings
- Periodic refresh with configurable interval
- Pub/sub for real-time updates
- Connection pooling

**Design Decisions**:
- **Single Source of Truth**: Redis as configuration store
- **Hot Reload**: Update routing without restart
- **Resilience**: Retry on connection failures

### 6. Gateway (`internal/gateway`)

**Responsibility**: Main service orchestration

**Key Features**:
- Network listener with graceful shutdown
- Health check endpoints (`/health`, `/ready`)
- Metrics server (Prometheus)
- Context propagation and timeout control

**Design Decisions**:
- **Graceful Shutdown**: Drain mode with connection cleanup
- **Health Checks**: Separate `/health` and `/ready` endpoints
- **Context Support**: Full context propagation for cancellation

### 7. Observability Components

#### Logger (`internal/logger`)
- **Structured Logging**: Zap-based JSON logging
- **Trace ID Correlation**: Automatic Trace ID injection from context
- **Performance**: Zero-allocation for disabled log levels

#### Metrics (`internal/metrics`)
- **Prometheus Integration**: Standard Prometheus metrics
- **Comprehensive Coverage**: Connections, sessions, latency, errors
- **Labels**: Service type, error type, direction

#### Tracing (`internal/tracing`)
- **OpenTelemetry**: Standard distributed tracing
- **Jaeger Integration**: Optional Jaeger exporter
- **Context Propagation**: Automatic Trace ID propagation

#### Access Logging (`internal/middleware`)
- **Structured Access Logs**: JSON format with Trace ID
- **Batching**: Batch processing for performance
- **Non-blocking**: Drop logs if buffer full (prevent blocking)

## Data Flow

### Request Flow

```
1. Client Connection
   │
   ├─► Protocol Sniffing (HTTP/WebSocket/TCP)
   │
   ├─► Rate Limiting Check
   │   ├─► Global Rate Limiter
   │   └─► IP-based Rate Limiter
   │
   ├─► Protocol Parsing
   │   ├─► Read Client Message Header (8 bytes)
   │   ├─► Extract ServerID (serverType, worldID, instID)
   │   └─► Validate Message Size
   │
   ├─► Routing
   │   ├─► Query Redis for routing rule
   │   ├─► Select backend endpoint
   │   └─► Check Circuit Breaker
   │
   ├─► Connection Pool
   │   ├─► Get connection from pool (or create new)
   │   └─► Retry on failure (exponential backoff)
   │
   ├─► Session Creation
   │   ├─► Generate Session ID
   │   ├─► Create session in sharded map
   │   └─► Record access log
   │
   └─► Message Forwarding
       ├─► Send initial message to backend
       └─► Start bidirectional forwarding
```

### Message Forwarding

```
Client → Gateway → Backend
   │        │         │
   │        │    [ServerMessageHeader (16 bytes)]
   │        │    [GateMsgHeader (9 or 33 bytes)]
   │        │    [Message Data]
   │        │
   │        │
   │        │    [ServerMessageHeader (16 bytes)]
   │        │    [GateMsgHeader (9 bytes)]
   │        │    [Message Data]
   │        │
   │        │
   │        │    [ClientMessageHeader (8 bytes)]
   │        │    [Message Data]
   │        │
   ▼        ▼         ▼
```

## Protocol Design

### Client Message Format

```
┌─────────────────────────────────────────┐
│ Client Message Header (8 bytes)         │
├─────────────────────────────────────────┤
│ Length (2 bytes, uint16, Little Endian)  │
│ MessageID (2 bytes, uint16, Little Endian)│
│ ServerID (4 bytes, uint32, Little Endian)│
│   └─ Bit structure:                     │
│      • bit 31: isZip (compression)      │
│      • bit 16-30: worldID (15 bits)     │
│      • bit 8-15: serverType (8 bits)    │
│      • bit 0-7: instID (8 bits)         │
└─────────────────────────────────────────┘
┌─────────────────────────────────────────┐
│ Message Data (Length bytes)             │
└─────────────────────────────────────────┘
```

### Gateway → Backend Format

**Original Format (9 bytes, compatible with existing cluster)**:
```
┌─────────────────────────────────────────┐
│ ServerMessageHeader (16 bytes)          │
├─────────────────────────────────────────┤
│ Length (4 bytes): GateMsgHeader + Data  │
│ Type (4 bytes): Message Type             │
│ ServerID (4 bytes): 0 (client message)  │
│ ObjectID (8 bytes): SessionID           │
└─────────────────────────────────────────┘
┌─────────────────────────────────────────┐
│ GateMsgHeader (9 bytes)                 │
├─────────────────────────────────────────┤
│ OpCode (1 byte): GateMsgOpCode.Trans=1  │
│ SessionID (8 bytes, Little Endian)      │
└─────────────────────────────────────────┘
┌─────────────────────────────────────────┐
│ Message Data                            │
└─────────────────────────────────────────┘
```

**Extended Format (33 bytes, with Trace Context, optional)**:
```
┌─────────────────────────────────────────┐
│ GateMsgHeaderExtended (33 bytes)        │
├─────────────────────────────────────────┤
│ OpCode (1 byte): GateMsgOpCode.Trans=1 │
│ SessionID (8 bytes, Little Endian)      │
│ TraceID (16 bytes): W3C Trace ID        │
│ SpanID (8 bytes): Span ID               │
└─────────────────────────────────────────┘
```

**⚠️ Compatibility Note**: Extended format requires backend service support. Default: disabled for compatibility.

## Design Decisions

### 1. In-Memory Session Storage

**Decision**: Store sessions in-memory (sharded maps) instead of Redis

**Rationale**:
- **Performance**: O(1) access, no network latency
- **Simplicity**: No external dependency for session storage
- **Ephemeral**: Sessions are short-lived, don't need persistence
- **Scalability**: Each pod manages its own sessions (stateless design)

**Trade-offs**:
- ❌ Sessions lost on pod restart (acceptable for game sessions)
- ✅ High performance, low latency

### 2. Connection Pooling

**Decision**: Reuse TCP connections to backend services

**Rationale**:
- **Performance**: Avoid connection establishment overhead
- **Resource Efficiency**: Reduce connection churn
- **Backend Protection**: Limit connections per service

**Implementation**:
- Channel-based pool for O(1) operations
- Per-service pools with configurable limits
- Idle timeout for resource cleanup

### 3. Redis for Configuration

**Decision**: Use Redis for dynamic routing configuration

**Rationale**:
- **Hot Reload**: Update routing without code changes
- **Centralized**: Single source of truth
- **Real-time**: Pub/sub for immediate updates

**Alternative Considered**: ConfigMap
- ❌ Requires pod restart for updates
- ✅ Simpler (no external dependency)

**Chosen**: Redis (for dynamic updates)

### 4. Sharded Session Maps

**Decision**: Use 16 sharded maps instead of single map

**Rationale**:
- **Concurrency**: Reduce lock contention
- **Performance**: 4-5x improvement in concurrent access
- **Scalability**: Better performance under high load

**Implementation**:
```go
shard := sessionID & 0xF  // Use low 4 bits
```

### 5. Buffer Pool Reuse

**Decision**: Use `sync.Pool` for message buffers ≤ 8KB

**Rationale**:
- **GC Pressure**: Reduce allocations and GC pauses
- **Performance**: 20-30% throughput improvement
- **Memory Efficiency**: Reuse buffers across requests

### 6. Trace ID Propagation (Optional)

**Decision**: Support Trace ID propagation, but disabled by default

**Rationale**:
- **Compatibility**: Existing cluster services expect 9-byte header
- **Future-Proof**: Support for new services with extended format
- **Configurable**: Can be enabled when backend supports it

## Technology Stack

### Core

- **Language**: Go 1.21+
- **Concurrency**: Goroutines, channels, atomic operations
- **Networking**: `net` package (standard library)

### Observability

- **Logging**: [zap](https://github.com/uber-go/zap) - High-performance structured logging
- **Metrics**: [Prometheus](https://prometheus.io/) - Metrics collection
- **Tracing**: [OpenTelemetry](https://opentelemetry.io/) - Distributed tracing

### Infrastructure

- **Container**: Docker
- **Orchestration**: Kubernetes 1.20+
- **Service Discovery**: Kubernetes DNS
- **Configuration**: Redis (dynamic), ConfigMap (static)

### Dependencies

- `go.uber.org/zap` - Structured logging
- `github.com/prometheus/client_golang` - Prometheus metrics
- `go.opentelemetry.io/otel` - OpenTelemetry tracing
- `github.com/redis/go-redis/v9` - Redis client

## Deployment Architecture

### Kubernetes Deployment

```yaml
Deployment:
  - Replicas: 3 (min), 10 (max with HPA)
  - Strategy: RollingUpdate (maxSurge: 1, maxUnavailable: 0)
  - Resources: 1 CPU, 1GB RAM (requests), 2 CPU, 2GB RAM (limits)

Service:
  - Type: NodePort (for external access)
  - Ports: 8080 (business), 9090 (health/metrics)
  - externalTrafficPolicy: Local (avoid cross-node forwarding)

HPA:
  - Min: 2, Max: 10
  - Metrics: CPU (70%), Memory (80%)
```

### Load Balancing

**External Load Balancer (HAProxy)**:
- Health check: `/health` endpoint on all nodes
- Automatic node discovery
- Remove unhealthy nodes from pool

**Kubernetes Service**:
- `externalTrafficPolicy: Local` - Route to local pods only
- `podAntiAffinity` - Distribute pods across nodes

### High Availability

- **Multiple Pods**: HPA ensures minimum 2 pods
- **Pod Distribution**: Anti-affinity spreads pods across nodes
- **Health Checks**: Liveness and readiness probes
- **Graceful Shutdown**: Drain mode with connection cleanup

### gRPC Communication Architecture: Gateway ↔ GameServer

In gRPC mode, multiple Gateway pods communicate with multiple GameServer instances. Each Gateway maintains bidirectional streams to GameServer instances based on routing rules.

```
┌─────────────────────────────────────────────────────────────────────┐
│                        Gateway Pods (Multiple)                       │
│                                                                     │
│  ┌──────────────────┐  ┌──────────────────┐  ┌──────────────────┐│
│  │ Gateway Pod 1    │  │ Gateway Pod 2    │  │ Gateway Pod 3    ││
│  │ game-gateway-abc │  │ game-gateway-xyz │  │ game-gateway-123 ││
│  │                  │  │                  │  │                  ││
│  │ ┌──────────────┐ │  │ ┌──────────────┐ │  │ ┌──────────────┐ ││
│  │ │gRPC Manager  │ │  │ │gRPC Manager  │ │  │ │gRPC Manager  │ ││
│  │ │              │ │  │ │              │ │  │ │              │ ││
│  │ │ Clients Map: │ │  │ │ Clients Map: │ │  │ │ Clients Map: │ ││
│  │ │ game-0:conn1 │ │  │ │ game-0:conn1 │ │  │ │ game-0:conn1 │ ││
│  │ │ game-1:conn2 │ │  │ │ game-1:conn2 │ │  │ │ game-2:conn3 │ ││
│  │ └──────┬───────┘ │  │ └──────┬───────┘ │  │ └──────┬───────┘ ││
│  └────────┼─────────┘  └────────┼─────────┘  └────────┼─────────┘│
│           │                     │                     │          │
│           │ StreamPackets       │ StreamPackets       │ StreamPackets│
│           │ NotifyConnect        │ NotifyConnect        │ NotifyConnect│
│           │ NotifyDisconnect     │ NotifyDisconnect     │ NotifyDisconnect│
│           │                     │                     │          │
└───────────┼─────────────────────┼─────────────────────┼──────────┘
            │                     │                     │
            │  ┌──────────────────┼──────────────────┐ │
            │  │                  │                  │ │
            ▼  ▼                  ▼                  ▼ ▼
┌─────────────────────────────────────────────────────────────────────┐
│                    GameServer Instances (Multiple)                   │
│                                                                     │
│  ┌──────────────────┐  ┌──────────────────┐  ┌──────────────────┐│
│  │ GameServer 0     │  │ GameServer 1     │  │ GameServer 2     ││
│  │ hgame-game-0     │  │ hgame-game-1     │  │ hgame-game-2     ││
│  │                  │  │                  │  │                  ││
│  │ ┌──────────────┐ │  │ ┌──────────────┐ │  │ ┌──────────────┐ ││
│  │ │GrpcConnMgr   │ │  │ │GrpcConnMgr   │ │  │ │GrpcConnMgr   │ ││
│  │ │              │ │  │ │              │ │  │ │              │ ││
│  │ │ Streams:     │ │  │ │ Streams:     │ │  │ │ Streams:     │ ││
│  │ │ abc → stream1│ │  │ │ abc → stream1│ │  │ │ abc → stream1│ ││
│  │ │ xyz → stream2│ │  │ │ xyz → stream2│ │  │ │ 123 → stream3│ ││
│  │ │ 123 → stream3│ │  │ │              │ │  │ │              │ ││
│  │ └──────────────┘ │  │ └──────────────┘ │  │ └──────────────┘ ││
│  │                  │  │                  │  │                  ││
│  │GameGatewayService│  │GameGatewayService│  │GameGatewayService││
│  └──────────────────┘  └──────────────────┘  └──────────────────┘│
└─────────────────────────────────────────────────────────────────────┘
```

#### Connection Model

**Gateway Side**:
- Each Gateway Pod maintains a **gRPC client connection pool** (`grpc/manager.go`)
- **One bidirectional stream** (`StreamPackets`) per GameServer address
- Stream is established when first client session routes to that GameServer
- Stream is reused for all client sessions routed to the same GameServer
- Gateway Pod Name (e.g., `game-gateway-abc123`) is sent in gRPC metadata header (`gate-name`)

**GameServer Side**:
- Each GameServer instance maintains a **GrpcConnectionManager**
- Maps Gateway Pod Name → `IServerStreamWriter<GamePacket>` stream
- Each Gateway Pod establishes one `StreamPackets` stream per GameServer
- Multiple Gateway Pods can connect to the same GameServer instance

#### Communication Patterns

1. **StreamPackets (Bidirectional Stream)**:
   - **Purpose**: Message forwarding (Gateway → GameServer, GameServer → Gateway)
   - **Frequency**: One stream per Gateway Pod per GameServer address
   - **Lifecycle**: Long-lived, established on first use, closed when Gateway disconnects
   - **Metadata**: `gate-name` header sent once during stream establishment

2. **NotifyConnect (Unary RPC)**:
   - **Purpose**: Notify GameServer of new client connection
   - **Frequency**: Once per client connection
   - **Metadata**: `gate-name` header sent with each call
   - **Caching**: GameServer caches `sessionID → gatewayName` mapping for login flow

3. **NotifyDisconnect (Unary RPC)**:
   - **Purpose**: Notify GameServer of client disconnection
   - **Frequency**: Once per client disconnection
   - **Metadata**: `gate-name` header sent with each call
   - **Cleanup**: GameServer removes `sessionID → gatewayName` mapping

#### Routing Logic

- Gateway uses **Router** to select GameServer address based on:
  - `serverType` (e.g., GameServer, AccountServer)
  - `worldID` (realm ID)
  - `instID` (instance ID)
- Router queries Redis for routing rules (hot reload support)
- Each Gateway independently routes client sessions to appropriate GameServer
- Multiple Gateway Pods can route to the same GameServer instance

#### Benefits

1. **Horizontal Scalability**: Gateway Pods scale independently
2. **Load Distribution**: Client sessions distributed across Gateway Pods
3. **Fault Tolerance**: Gateway Pod failure only affects its own sessions
4. **Efficient Connection Reuse**: One stream per Gateway-GameServer pair
5. **Centralized GameServer**: GameServer instances manage all Gateway connections

## Scalability

### Horizontal Scaling

- **Stateless Design**: No shared state between pods
- **HPA**: Automatic scaling based on CPU/memory
- **Load Distribution**: External load balancer distributes traffic

### Vertical Scaling

- **Resource Limits**: Configurable CPU/memory limits
- **Connection Limits**: Per-pod connection limits
- **Session Limits**: Per-pod session capacity

### Capacity Planning

**Per Pod**:
- Connections: 1,000 (default, configurable)
- Sessions: ~1,000 (depends on connection lifetime)
- Memory: ~100MB base + 10KB per connection
- CPU: ~50m idle, ~200m per 1000 connections

**Cluster**:
- Total Capacity = Per Pod Capacity × Number of Pods
- With HPA: Auto-scale based on metrics

## Reliability

### Failure Modes and Mitigation

1. **Backend Service Failure**
   - **Mitigation**: Circuit breaker, automatic retry, failover

2. **Redis Connection Loss**
   - **Mitigation**: Retry with exponential backoff, cached configuration

3. **High Load**
   - **Mitigation**: Rate limiting, connection limits, HPA scaling

4. **Pod Failure**
   - **Mitigation**: Kubernetes restart, health checks, multiple replicas

5. **Network Issues**
   - **Mitigation**: Connection timeouts, retry mechanism, circuit breaker

### Circuit Breaker

- **States**: Closed, Open, Half-Open
- **Threshold**: Configurable failure rate
- **Recovery**: Automatic transition to half-open after timeout

### Retry Mechanism

- **Strategy**: Exponential backoff
- **Max Retries**: Configurable (default: 3)
- **Retry Delay**: Configurable (default: 100ms)

## Performance Characteristics

- **Connection Handling**: O(1) pool operations
- **Session Lookup**: O(1) sharded map access
- **Message Forwarding**: < 1ms latency (P95)
- **Throughput**: 10,000+ connections per pod

See [PERFORMANCE.md](./PERFORMANCE.md) for detailed benchmarks.

## Security Considerations

- **Rate Limiting**: Global and per-IP limits
- **Input Validation**: Message size limits, protocol validation
- **DoS Protection**: Configurable message size limits
- **Connection Limits**: Per-IP and global limits

## Future Enhancements

- [ ] Consistent hash routing (for session affinity)
- [ ] WebSocket support (full bidirectional)
- [ ] TLS/SSL support
- [ ] Advanced metrics (per-service, per-world)
- [ ] Distributed session storage (optional, for multi-region)

---

**Last Updated**: 2024-01-15  
**Version**: 1.0.0
