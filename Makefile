.PHONY: build build-release build-debug run test clean

# Build the gateway (default: production build with Info level)
build:
	go build -o game-gateway ./cmd/gateway

# Build for production (Info level, Debug logs eliminated at compile time)
build-release:
	go build -o game-gateway ./cmd/gateway

# Build for debug (requires MinLogLevel = zapcore.DebugLevel in logger.go)
# Note: You need to manually change MinLogLevel in internal/logger/logger.go
build-debug:
	@echo "Warning: Make sure MinLogLevel = zapcore.DebugLevel in internal/logger/logger.go"
	go build -o game-gateway-debug ./cmd/gateway

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

