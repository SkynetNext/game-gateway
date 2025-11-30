package grpc

import (
	"context"
	"fmt"
	"sync"

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

	return client.stream.Send(packet)
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

	// Create new connection using NewClient (replaces deprecated Dial/DialContext)
	// Note: NewClient returns a client that is initially idle and doesn't connect immediately
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "dial failed")
		return nil, fmt.Errorf("failed to create client for %s: %w", address, err)
	}

	svcClient := gateway.NewGameGatewayServiceClient(conn)

	// Create stream with metadata and trace context
	md := metadata.Pairs("gate-name", m.gatewayName)
	streamCtx, cancel := context.WithCancel(spanCtx) // Use span context to propagate trace
	streamCtx = metadata.NewOutgoingContext(streamCtx, md)

	stream, err := svcClient.StreamPackets(streamCtx)
	if err != nil {
		cancel()
		conn.Close()
		span.RecordError(err)
		span.SetStatus(codes.Error, "create stream failed")
		return nil, fmt.Errorf("failed to create stream to %s: %w", address, err)
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
			logger.Error("gRPC stream error", zap.String("address", address), zap.Error(err))
			return
		}

		if m.handler != nil {
			m.handler(packet)
		}
	}
}
