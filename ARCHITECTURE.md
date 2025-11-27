# Game Gateway Architecture

## Overview

Game Gateway is a microservice-based game gateway service implemented in Go, designed to handle client connections and route them to appropriate backend services (AccountServer, VersionServer, GameServer).

## Core Components

### 1. Session Management (`internal/session`)
- In-memory session storage (sessionID -> Session)
- No Redis or shared memory required
- Session lifecycle management (create, update, cleanup)

### 2. Connection Pool (`internal/pool`)
- TCP connection pool to backend services
- Connection reuse and idle timeout management
- Per-service connection limits

### 3. Router (`internal/router`)
- Dynamic routing based on service type from Redis config
- Supports multiple routing strategies:
  - Round-robin
  - Consistent hash (based on user_id)
  - Realm-based (based on realm_id)
- No hardcoded service types, all from Redis configuration

### 4. Redis Client (`internal/redis`)
- Loads routing rules from Redis
- Loads realm_id -> GameServer address mapping
- Periodic refresh of configuration
- Supports pub/sub for real-time updates

### 5. Protocol Handler (`internal/protocol`)
- Protocol sniffing (HTTP/WebSocket/TCP)
- Packet header parsing (service_type, realm_id, user_id, session_id)
- Binary protocol support

### 6. Gateway (`internal/gateway`)
- Main gateway service
- Network listener
- Health check and metrics endpoints
- Graceful shutdown with drain mode

## Data Flow

```
Client Connection
  ↓
Protocol Sniffing (HTTP/WebSocket/TCP)
  ↓
Parse Packet Header (service_type, realm_id, user_id, session_id)
  ↓
Router (get backend address from Redis config)
  ↓
Connection Pool (get/reuse TCP connection)
  ↓
Create Session (in-memory)
  ↓
Bidirectional Forwarding (client ↔ backend)
```

## Configuration

### Redis Keys

1. **Routing Rules**: `game-gateway:routing:rules`
   - JSON format: `{"service_type": {"strategy": "...", "endpoints": [...]}}`
   - Example: `{"account": {"strategy": "consistent_hash", "endpoints": ["account-server:8080"]}}`

2. **Realm Mapping**: `game-gateway:realm:mapping`
   - Hash format: `realm_id -> GameServer address`
   - Example: `1 -> "game-server-1:8080"`

## Session Identification

- **Gateway ID**: Pod name (from K8s POD_NAME env var)
- **Session ID**: long integer (from packet header or generated)
- **Global Session ID**: `{gateway_id}:{session_id}`

## Backend Communication

- Gateway sends `gateway_id` to backend on first connection
- Backend maintains `sessionID -> gateway_id` mapping
- Backend replies include only `sessionID`, Gateway looks up client connection

## Deployment

- Kubernetes Deployment with HPA
- Health check endpoints: `/health`, `/ready`
- Graceful shutdown with drain mode
- Pod name as unique gateway identifier

