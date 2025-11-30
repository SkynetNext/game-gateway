package grpc

import (
	"context"
	"fmt"
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
	conn      *grpc.ClientConn
	svcClient gateway.GameGatewayServiceClient // 复用 service client
	stream    gateway.GameGatewayService_StreamPacketsClient
	cancel    context.CancelFunc
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
		conn:      conn,
		svcClient: svcClient, // 保存 service client 供 NotifyConnect/Disconnect 复用
		stream:    stream,
		cancel:    cancel,
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

// NotifyConnect 通知后端服务器客户端连接建立
// 业界最佳实践：连接建立时通知一次，携带完整元数据
func (m *Manager) NotifyConnect(ctx context.Context, address string, sessionID int64, clientIP string, protocol string) error {
	ctx, span := tracing.StartSpan(ctx, "gateway.notify_connect")
	defer span.End()

	span.SetAttributes(
		attribute.String("backend.address", address),
		attribute.Int64("session.id", sessionID),
		attribute.String("client.ip", clientIP),
		attribute.String("protocol", protocol),
	)

	// 获取或创建 gRPC 客户端（复用已有连接）
	client, err := m.getClient(ctx, address)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "get client failed")
		return fmt.Errorf("failed to get client for %s: %w", address, err)
	}

	// 复用已有的 service client（避免重复创建）
	svcClient := client.svcClient

	// 添加 gate-name 元数据
	md := metadata.Pairs("gate-name", m.gatewayName)
	callCtx := metadata.NewOutgoingContext(ctx, md)

	// 调用 NotifyConnect RPC
	req := &gateway.ConnectRequest{
		SessionId:   sessionID,
		ClientIp:    clientIP,
		Protocol:    protocol,
		ConnectTime: time.Now().UnixMilli(),
		Metadata:    make(map[string]string),
	}

	resp, err := svcClient.NotifyConnect(callCtx, req)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "notify connect failed")
		logger.Error("Failed to notify connect",
			zap.String("address", address),
			zap.Int64("sessionID", sessionID),
			zap.Error(err))
		return fmt.Errorf("failed to notify connect to %s: %w", address, err)
	}

	if !resp.Success {
		span.SetStatus(codes.Error, "connect rejected")
		logger.Warn("Connect rejected by backend",
			zap.String("address", address),
			zap.Int64("sessionID", sessionID),
			zap.String("reason", resp.Reason))
		return fmt.Errorf("connect rejected: %s", resp.Reason)
	}

	span.SetStatus(codes.Ok, "connect notified")
	logger.Debug("Notified backend of client connect",
		zap.String("address", address),
		zap.Int64("sessionID", sessionID),
		zap.String("clientIP", clientIP))

	return nil
}

// NotifyDisconnect 通知后端服务器客户端断开连接
// 业界最佳实践：连接断开时通知，及时清理资源
func (m *Manager) NotifyDisconnect(ctx context.Context, address string, sessionID int64, reason string) error {
	ctx, span := tracing.StartSpan(ctx, "gateway.notify_disconnect")
	defer span.End()

	span.SetAttributes(
		attribute.String("backend.address", address),
		attribute.Int64("session.id", sessionID),
		attribute.String("reason", reason),
	)

	// 获取现有客户端（断开时可能连接已关闭，所以不强制创建）
	m.mu.RLock()
	client, ok := m.clients[address]
	m.mu.RUnlock()

	if !ok {
		// 连接已关闭，记录但不报错
		logger.Debug("gRPC client already closed, skip disconnect notification",
			zap.String("address", address),
			zap.Int64("sessionID", sessionID))
		return nil
	}

	// 复用已有的 service client（避免重复创建）
	svcClient := client.svcClient

	// 添加 gate-name 元数据
	md := metadata.Pairs("gate-name", m.gatewayName)
	callCtx := metadata.NewOutgoingContext(ctx, md)

	// 调用 NotifyDisconnect RPC
	req := &gateway.DisconnectRequest{
		SessionId:      sessionID,
		Reason:         reason,
		DisconnectTime: time.Now().UnixMilli(),
	}

	resp, err := svcClient.NotifyDisconnect(callCtx, req)
	if err != nil {
		// 断开通知失败不是致命错误，记录日志即可
		span.RecordError(err)
		span.SetStatus(codes.Error, "notify disconnect failed")
		logger.Warn("Failed to notify disconnect",
			zap.String("address", address),
			zap.Int64("sessionID", sessionID),
			zap.Error(err))
		return nil // 不返回错误，避免影响断开流程
	}

	if resp.Acknowledged {
		span.SetStatus(codes.Ok, "disconnect acknowledged")
		logger.Debug("Backend acknowledged client disconnect",
			zap.String("address", address),
			zap.Int64("sessionID", sessionID))
	}

	return nil
}
