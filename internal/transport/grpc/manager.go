package grpc

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	gateway "github.com/SkynetNext/game-gateway/api"
	"github.com/SkynetNext/game-gateway/internal/logger"
	"github.com/SkynetNext/game-gateway/internal/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

type PacketHandler func(packet *gateway.GamePacket)

type Manager struct {
	mu      sync.RWMutex
	clients map[string]*Client // address -> client

	gatewayName string // Pod Name (e.g., "game-gateway-7d8f9c-abc12")
	handler     PacketHandler
}

type Client struct {
	conn   *grpc.ClientConn
	stream gateway.GameGatewayService_StreamPacketsClient
	cancel context.CancelFunc
}

func NewManager(gatewayName string, handler PacketHandler) *Manager {
	return &Manager{
		clients:     make(map[string]*Client),
		gatewayName: gatewayName,
		handler:     handler,
	}
}

func (m *Manager) Send(ctx context.Context, address string, packet *gateway.GamePacket) error {
	client, err := m.getClient(ctx, address)
	if err != nil {
		return err
	}

	err = client.stream.Send(packet)
	if err != nil {
		// Send failed, connection may be closed, clean up old connection for next reconnect
		m.mu.Lock()
		if c, ok := m.clients[address]; ok && c.stream == client.stream {
			delete(m.clients, address)
			c.conn.Close()
			c.cancel()
		}
		m.mu.Unlock()

		logger.Warn("gRPC send failed, connection will be recreated on next send",
			zap.String("address", address),
			zap.Error(err))
		return fmt.Errorf("send failed: %w", err)
	}

	return nil
}

func (m *Manager) getClient(ctx context.Context, address string) (*Client, error) {
	m.mu.RLock()
	client, ok := m.clients[address]
	m.mu.RUnlock()

	if ok {
		return client, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Double check
	if client, ok = m.clients[address]; ok {
		return client, nil
	}

	// Create span for gRPC connection establishment
	spanCtx, span := tracing.StartSpan(ctx, "gateway.grpc_connect")
	defer span.End()

	span.SetAttributes(
		attribute.String("backend.address", address),
		attribute.String("gateway.name", m.gatewayName),
		attribute.String("transport", "grpc"),
	)

	// Retry logic: maximum 3 retries with exponential backoff
	const maxRetries = 3
	var conn *grpc.ClientConn
	var stream gateway.GameGatewayService_StreamPacketsClient
	var cancel context.CancelFunc
	var err error

	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff: 100ms, 200ms, 400ms
			backoff := time.Duration(100*(1<<uint(attempt-1))) * time.Millisecond
			logger.Info("Retrying gRPC connection",
				zap.String("address", address),
				zap.Int("attempt", attempt+1),
				zap.Duration("backoff", backoff))
			time.Sleep(backoff)
		}

		// Create new connection using NewClient (replaces deprecated Dial/DialContext)
		// Note: NewClient returns a client that is initially idle and doesn't connect immediately
		conn, err = grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			span.RecordError(err)
			logger.Warn("Failed to create gRPC client",
				zap.String("address", address),
				zap.Int("attempt", attempt+1),
				zap.Error(err))
			continue // retry
		}

		svcClient := gateway.NewGameGatewayServiceClient(conn)

		// Create stream with metadata and trace context
		md := metadata.Pairs("gate-name", m.gatewayName)
		streamCtx, cancelFunc := context.WithCancel(spanCtx) // Use span context to propagate trace
		streamCtx = metadata.NewOutgoingContext(streamCtx, md)
		cancel = cancelFunc

		stream, err = svcClient.StreamPackets(streamCtx)
		if err != nil {
			cancel()
			conn.Close()
			span.RecordError(err)
			logger.Warn("Failed to create gRPC stream",
				zap.String("address", address),
				zap.Int("attempt", attempt+1),
				zap.Error(err))
			continue // retry
		}

		// Success, exit retry loop
		break
	}

	if err != nil {
		span.SetStatus(codes.Error, "connection failed after retries")
		return nil, fmt.Errorf("failed to create stream to %s after %d attempts: %w", address, maxRetries, err)
	}

	span.SetStatus(codes.Ok, "connected")

	// Start receiving goroutine
	go m.recvLoop(address, stream)

	client = &Client{
		conn:   conn,
		stream: stream,
		cancel: cancel,
	}
	m.clients[address] = client

	return client, nil
}

func (m *Manager) recvLoop(address string, stream gateway.GameGatewayService_StreamPacketsClient) {
	defer func() {
		m.mu.Lock()
		if client, ok := m.clients[address]; ok && client.stream == stream {
			delete(m.clients, address)
			client.conn.Close()
			client.cancel()
		}
		m.mu.Unlock()
	}()

	for {
		packet, err := stream.Recv()
		if err != nil {
			// EOF is a normal close signal, should not be treated as ERROR
			if err == io.EOF {
				logger.Info("gRPC stream closed by server", zap.String("address", address))
			} else {
				logger.Error("gRPC stream error", zap.String("address", address), zap.Error(err))
			}
			return
		}

		if m.handler != nil {
			m.handler(packet)
		}
	}
}
