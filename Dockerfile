# Build stage
FROM golang:1.21-alpine AS builder

# Set Go proxy for China network (use goproxy.cn or direct)
ENV GOPROXY=https://goproxy.cn,direct
ENV CGO_ENABLED=0
ENV GOOS=linux

WORKDIR /build

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build
RUN go build -a -installsuffix cgo -o game-gateway ./cmd/gateway

# Runtime stage
FROM alpine:latest

RUN apk --no-cache add ca-certificates tzdata
ENV TZ=Asia/Shanghai

WORKDIR /app

# Copy binary from builder
COPY --from=builder /build/game-gateway .
COPY --from=builder /build/config ./config

# Expose ports
EXPOSE 8080 9090

# Run
CMD ["./game-gateway", "-config", "config/config.yaml"]

