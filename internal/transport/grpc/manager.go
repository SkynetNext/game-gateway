package grpc

import (
	"context"
	"fmt"
	"hash/fnv"
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
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
)

type PacketHandler func(packet *gateway.GamePacket)

type Manager struct {
	// Use sync.Map instead of map + RWMutex for better concurrent read performance.
	// sync.Map is optimized for cases where:
	// - Reads are much more common than writes
	// - Multiple goroutines read/write different keys
	// - We don't need to iterate over all entries
	clients sync.Map // address -> *Client

	gatewayName string // Pod Name (e.g., "game-gateway-7d8f9c-abc12")
	handler     PacketHandler

	// Configuration
	keepaliveTime        time.Duration // Used for gRPC keepalive configuration
	reconnectInterval    time.Duration
	maxReconnectAttempts int
	ctx                  context.Context
	cancel               context.CancelFunc
}

type Client struct {
	conn   *grpc.ClientConn
	stream gateway.GameGatewayService_StreamPacketsClient
	cancel context.CancelFunc

	// Connection state
	reconnectAttempts int
	mu                sync.RWMutex
}

func NewManager(gatewayName string, handler PacketHandler, keepaliveTime, reconnectInterval time.Duration, maxReconnectAttempts int) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		gatewayName:          gatewayName,
		handler:              handler,
		keepaliveTime:        keepaliveTime,
		reconnectInterval:    reconnectInterval,
		maxReconnectAttempts: maxReconnectAttempts,
		ctx:                  ctx,
		cancel:               cancel,
	}
}

// Close gracefully closes all gRPC connections
// This should be called during gateway shutdown to ensure all connections are properly closed
// It cancels all recvLoop goroutines and closes all gRPC connections
func (m *Manager) Close() error {
	logger.Info("Closing gRPC transport manager", zap.String("gateway_name", m.gatewayName))

	// Cancel manager context to stop all background goroutines
	if m.cancel != nil {
		m.cancel()
	}

	// Collect all clients first to avoid modifying map during iteration
	var clientsToClose []*Client
	m.clients.Range(func(key, value interface{}) bool {
		if client, ok := value.(*Client); ok {
			clientsToClose = append(clientsToClose, client)
		}
		return true
	})

	// Close all collected clients
	for _, client := range clientsToClose {
		// Cancel context to stop recvLoop goroutine
		if client.cancel != nil {
			client.cancel()
		}
		// Close gRPC connection
		if client.conn != nil {
			client.conn.Close()
		}
	}

	// Clear all entries from map
	m.clients.Range(func(key, value interface{}) bool {
		m.clients.Delete(key)
		return true
	})

	logger.Info("gRPC transport manager closed", zap.Int("connections_closed", len(clientsToClose)))
	return nil
}

func (m *Manager) Send(ctx context.Context, address string, packet *gateway.GamePacket) error {
	client, err := m.getClient(ctx, address)
	if err != nil {
		return err
	}

	err = client.stream.Send(packet)
	if err != nil {
		// Send failed, connection may be closed
		// Use CompareAndDelete to safely remove only if it's still the same client
		// This prevents race conditions where another goroutine might be using the client
		if m.clients.CompareAndDelete(address, client) {
			// Successfully deleted, safe to close
			client.conn.Close()
			client.cancel()
			// Trigger reconnection in background
			go m.reconnect(address)
		} else {
			// Another goroutine might have replaced it, just log
			logger.Debug("gRPC send failed, client was already replaced",
				zap.String("address", address))
		}

		logger.Warn("gRPC send failed, will attempt to reconnect",
			zap.String("address", address),
			zap.Error(err))
		return fmt.Errorf("send failed: %w", err)
	}

	return nil
}

func (m *Manager) getClient(ctx context.Context, address string) (*Client, error) {
	// Fast path: check if client already exists (lock-free read with sync.Map)
	if actualClient, ok := m.clients.Load(address); ok {
		if client, ok := actualClient.(*Client); ok {
			return client, nil
		}
	}

	// Slow path: need to create new client
	// Use LoadOrStore to handle race condition where multiple goroutines try to create the same client
	// This eliminates the need for double-checked locking pattern

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
		// Configure gRPC keepalive for connection health monitoring
		// Note: NewClient returns a client that is initially idle and doesn't connect immediately
		keepaliveParams := keepalive.ClientParameters{
			Time:                m.keepaliveTime,     // Send keepalive ping every keepaliveTime
			Timeout:             m.keepaliveTime / 2, // Wait keepaliveTime/2 for ping ack before considering connection dead
			PermitWithoutStream: true,                // Send keepalive pings even when there are no active streams
		}
		conn, err = grpc.NewClient(address,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithKeepaliveParams(keepaliveParams))
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

	client := &Client{
		conn:              conn,
		stream:            stream,
		cancel:            cancel,
		reconnectAttempts: 0,
	}

	// Start receiving goroutine
	go m.recvLoop(address, stream, client)

	// Use LoadOrStore to atomically store the client, or return existing one if another goroutine created it
	actualClient, loaded := m.clients.LoadOrStore(address, client)
	if loaded {
		// Another goroutine already created a client for this address
		// Close our newly created connection and use the existing one
		conn.Close()
		cancel()
		if existingClient, ok := actualClient.(*Client); ok {
			return existingClient, nil
		}
	}

	return client, nil
}

func (m *Manager) recvLoop(address string, stream gateway.GameGatewayService_StreamPacketsClient, client *Client) {
	defer func() {
		// First load the client to check if it matches our stream
		if actualClient, loaded := m.clients.Load(address); loaded {
			if actualClient == client && client.stream == stream {
				// Use CompareAndDelete to safely remove only if it's still the same client
				// This prevents deleting a client that was replaced by another goroutine
				if m.clients.CompareAndDelete(address, client) {
					client.conn.Close()
					client.cancel()
					// Trigger reconnection in background
					go m.reconnect(address)
				}
			}
		}
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

		logger.Debug("gRPC recv packet",
			zap.String("address", address),
			zap.Int64("session_id", packet.SessionId),
			zap.Int32("msg_id", packet.MsgId),
			zap.Any("metadata", packet.Metadata),
			zap.Int32("payload_size", int32(len(packet.Payload))),
		)

		if m.handler != nil {
			// Protect handler execution with recover to prevent panic from terminating the recvLoop.
			// Without this protection, if handler panics, the entire recvLoop goroutine exits,
			// causing the defer cleanup to close the connection and stream, which prevents
			// receiving any subsequent packets.
			func() {
				defer func() {
					if r := recover(); r != nil {
						logger.Error("panic in packet handler, continuing to receive packets",
							zap.String("address", address),
							zap.Any("panic", r),
							zap.Int64("session_id", packet.SessionId),
							zap.Int32("msg_id", packet.MsgId))
					}
				}()
				m.handler(packet)
			}()
		} else {
			logger.Warn("recvLoop: handler is nil, packet not processed",
				zap.String("address", address),
				zap.Int64("session_id", packet.SessionId),
				zap.Int32("msg_id", packet.MsgId))
		}
	}
}

// reconnect attempts to reconnect to the given address
func (m *Manager) reconnect(address string) {
	// Check if already reconnecting or reconnected
	if _, ok := m.clients.Load(address); ok {
		return // Already reconnected
	}

	var attempts int
	client, ok := m.clients.Load(address)
	if ok {
		actualClient := client.(*Client)
		actualClient.mu.Lock()
		attempts = actualClient.reconnectAttempts
		actualClient.mu.Unlock()

		// Check max reconnect attempts
		if m.maxReconnectAttempts > 0 && attempts >= m.maxReconnectAttempts {
			logger.Error("gRPC max reconnect attempts reached",
				zap.String("address", address),
				zap.Int("attempts", attempts))
			return
		}
	}

	// Calculate exponential backoff with jitter (similar to getClient logic)
	// Base delay: 100ms, 200ms, 400ms, 800ms, ...
	// Cap at reconnectInterval * 10 to avoid excessive delays
	baseBackoff := time.Duration(100*(1<<uint(attempts))) * time.Millisecond
	maxBackoff := m.reconnectInterval * 10
	if baseBackoff > maxBackoff {
		baseBackoff = maxBackoff
	}

	// Add jitter (0-100ms random delay) to avoid thundering herd problem
	// when multiple gateways reconnect simultaneously
	maxJitter := 100 * time.Millisecond
	// Use hash-based jitter for deterministic but distributed delays
	// This ensures different addresses get different jitter values
	h := fnv.New32a()
	h.Write([]byte(address))
	jitterValue := time.Duration(h.Sum32()%uint32(maxJitter.Milliseconds())) * time.Millisecond
	backoff := baseBackoff + jitterValue

	logger.Info("gRPC attempting to reconnect",
		zap.String("address", address),
		zap.Int("attempt", attempts+1),
		zap.Duration("base_backoff", baseBackoff),
		zap.Duration("jitter", jitterValue),
		zap.Duration("total_backoff", backoff))

	// Wait before reconnecting (exponential backoff with jitter)
	select {
	case <-m.ctx.Done():
		return
	case <-time.After(backoff):
	}

	// Try to reconnect
	ctx, cancel := context.WithTimeout(m.ctx, 10*time.Second)
	defer cancel()

	_, err := m.getClient(ctx, address)
	if err != nil {
		logger.Warn("gRPC reconnect failed, will retry",
			zap.String("address", address),
			zap.Int("attempt", attempts+1),
			zap.Error(err))
		// Update reconnect attempts
		if client, ok := m.clients.Load(address); ok {
			actualClient := client.(*Client)
			actualClient.mu.Lock()
			actualClient.reconnectAttempts++
			actualClient.mu.Unlock()
		}
		// Retry after exponential backoff
		go m.reconnect(address)
	} else {
		logger.Info("gRPC reconnected successfully",
			zap.String("address", address))
		// Reset reconnect attempts on success
		if client, ok := m.clients.Load(address); ok {
			actualClient := client.(*Client)
			actualClient.mu.Lock()
			actualClient.reconnectAttempts = 0
			actualClient.mu.Unlock()
		}
	}
}
