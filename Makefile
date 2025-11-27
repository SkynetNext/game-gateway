.PHONY: build run test clean

# Build the gateway
build:
	go build -o game-gateway ./cmd/gateway

# Run the gateway
run: build
	./game-gateway -config config/config.yaml

# Run tests
test:
	go test ./...

# Clean build artifacts
clean:
	rm -f game-gateway game-gateway.exe

# Install dependencies
deps:
	go mod download
	go mod tidy

