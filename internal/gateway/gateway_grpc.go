package gateway

import (
	"context"
	"sync/atomic"
	"time"

	gateway "github.com/SkynetNext/game-gateway/api"
	"github.com/SkynetNext/game-gateway/internal/logger"
	"github.com/SkynetNext/game-gateway/internal/protocol"
	"github.com/SkynetNext/game-gateway/internal/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.uber.org/zap"
)

// handleGrpcPacket handles packets received from GameServer via gRPC
func (g *Gateway) handleGrpcPacket(packet *gateway.GamePacket) {
	logger.Debug("handleGrpcPacket: received packet from gRPC",
		zap.Int64("session_id", packet.SessionId),
		zap.Int32("msg_id", packet.MsgId),
		zap.Int("payload_size", int(len(packet.Payload))),
		zap.Int("target_session_ids_count", len(packet.TargetSessionIds)))

	// Handle connection lifecycle events (packet_type in metadata)
	if packet.Metadata != nil {
		if packetType, ok := packet.Metadata["packet_type"]; ok {
			switch packetType {
			case "server_connect":
				// GameServer accepted connection (login response)
				// Gateway doesn't need to do anything special, just return
				return
			case "server_disconnect":
				// GameServer-initiated disconnect: close the client connection
				if packet.SessionId > 0 {
					g.handleServerDisconnect(packet.SessionId, packet.Metadata)
				}
				return
			}
		}
	}

	// Handle broadcast packets
	if len(packet.TargetSessionIds) > 0 {
		for _, sessID := range packet.TargetSessionIds {
			g.sendToSession(sessID, packet)
		}
		return
	}

	// Handle unicast packets
	if packet.SessionId > 0 {
		g.sendToSession(packet.SessionId, packet)
	}
}

// handleServerDisconnect handles server-initiated disconnect requests
func (g *Gateway) handleServerDisconnect(sessionID int64, metadata map[string]string) {
	sess, ok := g.sessionManager.Get(sessionID)
	if !ok || sess == nil {
		return
	}

	reason := "server_initiated"
	if r, ok := metadata["reason"]; ok {
		reason = r
	}

	logger.Info("server_disconnect: closing client connection",
		zap.Int64("session_id", sessionID),
		zap.String("reason", reason))

	// Close client connection
	if sess.ClientConn != nil {
		sess.ClientConn.Close()
	}

	// Remove session
	g.sessionManager.Remove(sessionID)
}

func (g *Gateway) sendToSession(sessionID int64, packet *gateway.GamePacket) {
	sess, ok := g.sessionManager.Get(sessionID)
	if !ok || sess == nil {
		// Log when session not found - this helps diagnose why packets aren't being forwarded
		logger.Info("sendToSession: session not found, cannot forward packet",
			zap.Int64("session_id", sessionID),
			zap.Int32("msg_id", packet.MsgId),
			zap.Int("payload_size", len(packet.Payload)))
		return
	}

	if sess.ClientConn == nil {
		logger.Info("sendToSession: session exists but ClientConn is nil",
			zap.Int64("session_id", sessionID),
			zap.Int32("msg_id", packet.MsgId))
		return
	}

	// Set write timeout
	g.configMu.RLock()
	writeTimeout := g.config.ConnectionPool.WriteTimeout
	g.configMu.RUnlock()

	sess.ClientConn.SetWriteDeadline(time.Now().Add(writeTimeout))

	logger.Debug("sendToSession: writing client header", zap.Int64("session_id", sessionID), zap.Int32("msg_id", packet.MsgId), zap.Int32("payload_size", (int32)(len(packet.Payload))))

	// Write ClientMessageHeader (8 bytes) before sending payload
	clientHeader := &protocol.ClientMessageHeader{
		Length:    uint16(len(packet.Payload)),
		MessageID: uint16(packet.MsgId),
		ServerID:  0, // ServerID is 0 for server-to-client messages (same as TCP mode)
	}

	logger.Debug("sendToSession: writing packet header to client",
		zap.Int64("session_id", sessionID),
		zap.Uint16("header_length", clientHeader.Length),
		zap.Uint16("header_message_id", clientHeader.MessageID),
		zap.Uint32("header_server_id", clientHeader.ServerID),
		zap.Int("payload_size", len(packet.Payload)))

	if err := protocol.WriteClientMessageHeader(sess.ClientConn, clientHeader); err != nil {
		logger.Debug("failed to write client header", zap.Int64("session_id", sessionID), zap.Error(err))
		return
	}

	// Write payload
	if len(packet.Payload) > 0 {
		n, err := sess.ClientConn.Write(packet.Payload)
		if err != nil {
			logger.Debug("failed to write payload to client session", zap.Int64("session_id", sessionID), zap.Error(err))
			return
		} else if sess.BytesOut != nil {
			// Update bytesOut counter (for access log statistics)
			// Include header size (8 bytes) + payload size
			atomic.AddInt64(sess.BytesOut, int64(8+n))
		}
	} else if sess.BytesOut != nil {
		// Even if payload is empty, we still wrote the header (8 bytes)
		atomic.AddInt64(sess.BytesOut, 8)
	}
}

// forwardGrpcConnection forwards data from client to GameServer via gRPC
func (g *Gateway) forwardGrpcConnection(ctx context.Context, sessID int64, clientConn *protocol.SniffConn, backendAddr string, log *logger.ContextLogger, bytesIn, bytesOut *int64) {
	// Create span for gRPC forwarding
	ctx, span := tracing.StartSpan(ctx, "gateway.grpc_forward")
	defer func() {
		// Record final bytes transferred
		span.SetAttributes(
			attribute.Int64("bytes.in", atomic.LoadInt64(bytesIn)),
			attribute.Int64("bytes.out", atomic.LoadInt64(bytesOut)),
		)
		span.End()
	}()

	span.SetAttributes(
		attribute.Int64("session.id", sessID),
		attribute.String("backend.address", backendAddr),
		attribute.String("transport", "grpc"),
	)

	defer func() {
		// Cleanup session
		g.sessionManager.Remove(sessID)
	}()

	g.configMu.RLock()
	maxMessageSize := g.config.Security.MaxMessageSize
	enableTracePropagation := g.config.Tracing.EnableTracePropagation
	g.configMu.RUnlock()

	packetCount := 0
	for {
		// Read Packet
		header, data, err := protocol.ReadFullPacket(clientConn, maxMessageSize)
		if err != nil {
			if err != protocol.ErrMessageTooLarge {
				// Normal close (EOF), not an error
				span.SetStatus(codes.Ok, "connection closed")
			} else {
				span.RecordError(err)
				span.SetStatus(codes.Error, "read packet failed")
			}
			return
		}

		// Send via gRPC
		// data returned by ReadFullPacket is the Body.
		packet := &gateway.GamePacket{
			SessionId: sessID,
			MsgId:     int32(header.MessageID),
			Payload:   data,
		}

		// Propagate trace context if enabled
		if enableTracePropagation {
			packet.Metadata = extractTraceContext(ctx)
		}

		err = g.grpcManager.Send(ctx, backendAddr, packet)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "grpc send failed")
			log.Error("failed to send grpc packet", zap.Error(err))
			return
		}

		atomic.AddInt64(bytesIn, int64(len(data)))
		packetCount++
	}
}
