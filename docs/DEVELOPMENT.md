# Development Guide

## Table of Contents

1. [Getting Started](#getting-started)
2. [Project Structure](#project-structure)
3. [Development Workflow](#development-workflow)
4. [Code Standards](#code-standards)
5. [Testing](#testing)
6. [Debugging](#debugging)
7. [Contributing](#contributing)

## Getting Started

### Prerequisites

- Go 1.21 or later
- Docker (for containerization)
- Kubernetes cluster (for deployment testing)
- Redis (for configuration)

### Development Environment Setup

```bash
# Clone repository
git clone https://github.com/SkynetNext/game-gateway.git
cd game-gateway

# Install dependencies
go mod download

# Build
go build -o game-gateway ./cmd/gateway

# Run tests
go test ./...

# Run with debug logging
LOG_LEVEL=debug ./game-gateway -config config/config.yaml
```

## Project Structure

```
game-gateway/
├── cmd/
│   └── gateway/              # Main application entry point
│       └── main.go
├── internal/
│   ├── gateway/              # Core gateway logic
│   │   ├── gateway.go        # Main gateway service
│   │   ├── config_reload.go  # Hot reload support
│   │   └── gateway_test.go   # Unit tests
│   ├── protocol/             # Protocol handling
│   │   ├── packet.go         # Message parsing
│   │   ├── server_packet.go  # Server message handling
│   │   └── sniffer.go        # Protocol detection
│   ├── router/               # Routing engine
│   │   └── router.go
│   ├── pool/                 # Connection pool
│   │   ├── pool.go
│   │   └── manager.go
│   ├── session/              # Session management
│   │   └── session.go
│   ├── logger/               # Structured logging
│   │   └── logger.go
│   ├── metrics/              # Prometheus metrics
│   │   └── metrics.go
│   ├── middleware/          # Middleware (access logs)
│   │   └── accesslog.go
│   ├── tracing/              # OpenTelemetry tracing
│   │   └── tracing.go
│   ├── config/               # Configuration management
│   │   ├── config.go
│   │   └── hotreload.go
│   ├── redis/                # Redis client
│   │   └── client.go
│   ├── ratelimit/            # Rate limiting
│   │   ├── limiter.go
│   │   └── ip_limiter.go
│   ├── circuitbreaker/       # Circuit breaker
│   │   └── breaker.go
│   ├── retry/                # Retry mechanism
│   │   └── retry.go
│   └── buffer/               # Buffer pool
│       └── pool.go
├── config/                   # Configuration files
│   └── config.yaml
├── deploy/                   # Kubernetes manifests
│   ├── deployment.yaml
│   ├── monitoring.yaml
│   └── prometheus-alerts.yaml
├── tools/                    # Utility tools
│   └── loadtest/            # Load testing tool
├── docs/                     # Documentation
│   ├── README.md
│   ├── LOGGING_IMPROVEMENTS.md
│   └── TRACE_PROPAGATION.md
├── README.md                 # Project overview
├── ARCHITECTURE.md           # Architecture documentation
├── PERFORMANCE.md           # Performance guide
├── go.mod                   # Go module dependencies
└── Dockerfile               # Docker build file
```

## Development Workflow

### 1. Create Feature Branch

```bash
git checkout -b feature/your-feature-name
```

### 2. Make Changes

- Follow code standards (see below)
- Add tests for new features
- Update documentation

### 3. Run Tests

```bash
# All tests
go test ./...

# With coverage
go test -cover ./...

# Specific package
go test ./internal/gateway
```

### 4. Run Linters

```bash
# Format code
go fmt ./...

# Run static analysis
go vet ./...

# Run golangci-lint (if installed)
golangci-lint run
```

### 5. Commit Changes

```bash
git add .
git commit -m "feat: add new feature"
```

### 6. Push and Create PR

```bash
git push origin feature/your-feature-name
# Create Pull Request on GitHub
```

## Code Standards

### Go Style Guide

Follow [Effective Go](https://golang.org/doc/effective_go) and [Go Code Review Comments](https://github.com/golang/go/wiki/CodeReviewComments).

### Naming Conventions

- **Packages**: Lowercase, single word
- **Exported Functions**: PascalCase
- **Unexported Functions**: camelCase
- **Constants**: PascalCase or UPPER_CASE
- **Variables**: camelCase

### Code Organization

1. **Imports**: Group by standard library, third-party, local
2. **Functions**: Keep functions small and focused
3. **Error Handling**: Always handle errors, don't ignore them
4. **Context**: Use context for cancellation and timeouts

### Example

```go
package gateway

import (
    "context"
    "time"

    "go.uber.org/zap"

    "github.com/SkynetNext/game-gateway/internal/logger"
)

// HandleConnection handles a client connection
// Context is used for cancellation and timeout control
func (g *Gateway) HandleConnection(ctx context.Context, conn net.Conn) error {
    // Always use context-aware logging
    logger.InfoWithTrace(ctx, "handling connection",
        zap.String("remote_addr", conn.RemoteAddr().String()),
    )
    
    // Handle errors properly
    if err := doSomething(); err != nil {
        logger.ErrorWithTrace(ctx, "operation failed",
            zap.Error(err),
        )
        return fmt.Errorf("operation failed: %w", err)
    }
    
    return nil
}
```

### Best Practices

1. **Use Structured Logging**
   ```go
   // ✅ Good
   logger.InfoWithTrace(ctx, "message", zap.String("key", "value"))
   
   // ❌ Bad
   log.Printf("message: key=%s", value)
   ```

2. **Use Context for Cancellation**
   ```go
   // ✅ Good
   ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
   defer cancel()
   
   // ❌ Bad
   time.Sleep(5 * time.Second)
   ```

3. **Handle Errors Properly**
   ```go
   // ✅ Good
   if err != nil {
       return fmt.Errorf("operation failed: %w", err)
   }
   
   // ❌ Bad
   _ = err  // Ignoring error
   ```

4. **Use Buffer Pools**
   ```go
   // ✅ Good
   buf := buffer.Get()
   defer buffer.Put(buf)
   
   // ❌ Bad
   buf := make([]byte, 8192)
   ```

## Testing

### Unit Tests

```bash
# Run all tests
go test ./...

# Run with verbose output
go test -v ./...

# Run specific test
go test -v -run TestHandleConnection ./internal/gateway
```

### Benchmarks

```bash
# Run all benchmarks
go test -bench=. -benchmem ./...

# Run specific benchmark
go test -bench=BenchmarkSessionManager_Get -benchmem ./internal/session
```

### Integration Tests

```bash
# Run integration tests (requires Redis)
go test -tags=integration ./...
```

### Test Coverage

```bash
# Generate coverage report
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## Debugging

### Debug Logging

```bash
LOG_LEVEL=debug ./game-gateway -config config/config.yaml
```

### Profiling

```bash
# CPU profile
go tool pprof http://localhost:9090/debug/pprof/profile

# Memory profile
go tool pprof http://localhost:9090/debug/pprof/heap

# Goroutine profile
go tool pprof http://localhost:9090/debug/pprof/goroutine
```

### Trace Analysis

```bash
# Generate trace
curl http://localhost:9090/debug/pprof/trace?seconds=5 > trace.out
go tool trace trace.out
```

### Log Analysis

```bash
# View logs with trace ID
kubectl logs -n hgame -l app=game-gateway | jq 'select(.trace_id=="...")'

# Filter by level
kubectl logs -n hgame -l app=game-gateway | jq 'select(.level=="error")'
```

## Contributing

### Pull Request Process

1. **Fork** the repository
2. **Create** a feature branch
3. **Make** your changes
4. **Add** tests
5. **Update** documentation
6. **Submit** pull request

### Commit Message Format

Follow [Conventional Commits](https://www.conventionalcommits.org/):

```
<type>(<scope>): <subject>

<body>

<footer>
```

**Types**:
- `feat`: New feature
- `fix`: Bug fix
- `docs`: Documentation
- `style`: Code style changes
- `refactor`: Code refactoring
- `test`: Test additions
- `chore`: Maintenance tasks

**Examples**:
```
feat(gateway): add circuit breaker support
fix(protocol): fix message size validation
docs(readme): update installation instructions
```

### Code Review Checklist

- [ ] Code follows style guide
- [ ] Tests added/updated
- [ ] Documentation updated
- [ ] No linter errors
- [ ] Performance considered
- [ ] Error handling complete

## Common Tasks

### Adding a New Feature

1. Create feature branch
2. Implement feature
3. Add unit tests
4. Update documentation
5. Run tests and linters
6. Submit PR

### Fixing a Bug

1. Create bug fix branch
2. Write failing test
3. Fix bug
4. Verify test passes
5. Update documentation if needed
6. Submit PR

### Performance Optimization

1. Identify bottleneck (profiling)
2. Implement optimization
3. Add benchmark
4. Verify improvement
5. Update performance docs
6. Submit PR

---

**Last Updated**: 2024-01-15

