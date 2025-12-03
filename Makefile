.PHONY: build build-release build-debug run test clean configure-log-level

# Default log level (can be overridden: make build LOG_LEVEL=debug)
LOG_LEVEL ?= info

# Build information (can be injected via ldflags)
VERSION ?= dev
BUILD_TIME ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
GIT_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")

# Configure log level for compile-time optimization
configure-log-level:
	@echo "==> Configuring log level: $(LOG_LEVEL)"
	@if [ "$(LOG_LEVEL)" = "debug" ]; then \
		echo "Setting MinLogLevel to DebugLevel (all logs enabled)"; \
		sed -i 's/MinLogLevel zapcore.Level = zapcore\.InfoLevel/MinLogLevel zapcore.Level = zapcore.DebugLevel/' internal/logger/logger.go; \
		sed -i 's/MinLogLevel zapcore.Level = zapcore\.DebugLevel/MinLogLevel zapcore.Level = zapcore.DebugLevel/' internal/logger/logger.go; \
	else \
		echo "Setting MinLogLevel to InfoLevel (Debug logs eliminated at compile time)"; \
		sed -i 's/MinLogLevel zapcore.Level = zapcore\.DebugLevel/MinLogLevel zapcore.Level = zapcore.InfoLevel/' internal/logger/logger.go; \
		sed -i 's/MinLogLevel zapcore.Level = zapcore\.InfoLevel/MinLogLevel zapcore.Level = zapcore.InfoLevel/' internal/logger/logger.go; \
	fi
	@echo "==> Verifying MinLogLevel configuration:"
	@grep "MinLogLevel zapcore.Level" internal/logger/logger.go || true

# Build the gateway (default: production build with Info level)
build: configure-log-level
	@echo "Build info:"
	@echo "  Version: $(VERSION)"
	@echo "  Build Time: $(BUILD_TIME)"
	@echo "  Git Commit: $(GIT_COMMIT)"
	@echo "  Log Level: $(LOG_LEVEL)"
	go build -ldflags "-X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME) -X main.gitCommit=$(GIT_COMMIT)" \
		-o game-gateway ./cmd/gateway

# Build for production (Info level, Debug logs eliminated at compile time)
build-release: LOG_LEVEL=info
build-release: build

# Build for debug (Debug level, all logs enabled)
build-debug: LOG_LEVEL=debug
build-debug: configure-log-level
	@echo "Build info:"
	@echo "  Version: $(VERSION)"
	@echo "  Build Time: $(BUILD_TIME)"
	@echo "  Git Commit: $(GIT_COMMIT)"
	@echo "  Log Level: $(LOG_LEVEL)"
	go build -ldflags "-X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME) -X main.gitCommit=$(GIT_COMMIT)" \
		-o game-gateway-debug ./cmd/gateway

# Run the gateway
run: build
	./game-gateway -config config/config.yaml

# Run tests
test:
	go test ./...

# Clean build artifacts
clean:
	rm -f game-gateway game-gateway.exe game-gateway-debug

# Install dependencies
deps:
	go mod download
	go mod tidy

