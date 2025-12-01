# Build stage
FROM golang:1.24-alpine AS builder

# Go build arguments (can be overridden at build time)
ARG GOPROXY=https://proxy.golang.org,direct
ARG CGO_ENABLED=0
ARG GOOS=linux
ARG VERSION=dev
ARG BUILD_TIME=unknown
ARG GIT_COMMIT=unknown

# Set as environment variables for use in RUN commands
ENV GOPROXY=${GOPROXY}
ENV CGO_ENABLED=${CGO_ENABLED}
ENV GOOS=${GOOS}

WORKDIR /build

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build with version info injection
RUN go build -a -installsuffix cgo \
    -ldflags "-X main.version=${VERSION} -X main.buildTime=${BUILD_TIME} -X main.gitCommit=${GIT_COMMIT}" \
    -o game-gateway ./cmd/gateway

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

