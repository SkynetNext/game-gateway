# Game Gateway

A microservice-based game gateway service implemented in Go.

## Features

- ✅ Multi-protocol support (HTTP/WebSocket/TCP)
- ✅ Session management (in-memory storage, high performance)
- ✅ Connection pooling (TCP connection reuse)
- ✅ Dynamic routing (Account/Version/Game service routing)
- ✅ Realm-based routing mapping (based on realm_id)
- ✅ Redis configuration management (routing rules, realm mapping)
- ✅ Kubernetes integration (HPA support, Pod identification)
- ✅ Protobuf protocol support

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

Main configuration items:
- Listen address
- Redis connection
- Backend service addresses
- Routing rules (loaded from Redis)

## Deployment

Supports Kubernetes Deployment with HPA auto-scaling.

See `deploy/` directory for details.

