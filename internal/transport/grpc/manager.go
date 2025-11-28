package grpc

import (
	"context"
	"fmt"
	"sync"

	gateway "github.com/SkynetNext/game-gateway/api"
	"github.com/SkynetNext/game-gateway/internal/logger"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

type PacketHandler func(packet *gateway.GamePacket)

type Manager struct {
	mu      sync.RWMutex
	clients map[string]*Client // address -> client

	gatewayID uint32
	handler   PacketHandler
}

type Client struct {
	conn   *grpc.ClientConn
	stream gateway.GameGatewayService_StreamPacketsClient
	cancel context.CancelFunc
}

func NewManager(gatewayID uint32, handler PacketHandler) *Manager {
	return &Manager{
		clients:   make(map[string]*Client),
		gatewayID: gatewayID,
		handler:   handler,
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

	// Create new connection
	conn, err := grpc.Dial(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("failed to dial %s: %w", address, err)
	}

	svcClient := gateway.NewGameGatewayServiceClient(conn)

	// Create stream with metadata
	md := metadata.Pairs("gate-id", fmt.Sprintf("%d", m.gatewayID))
	streamCtx, cancel := context.WithCancel(context.Background())
	streamCtx = metadata.NewOutgoingContext(streamCtx, md)

	stream, err := svcClient.StreamPackets(streamCtx)
	if err != nil {
		cancel()
		conn.Close()
		return nil, fmt.Errorf("failed to create stream to %s: %w", address, err)
	}

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
