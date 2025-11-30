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
	// 广播处理
	if len(packet.TargetSessionIds) > 0 {
		for _, sessID := range packet.TargetSessionIds {
			g.sendToSession(sessID, packet)
		}
		return
	}

	// 单播处理
	if packet.SessionId > 0 {
		g.sendToSession(packet.SessionId, packet)
	}
}

func (g *Gateway) sendToSession(sessionID int64, packet *gateway.GamePacket) {
	sess, ok := g.sessionManager.Get(sessionID)
	if !ok || sess == nil {
		return
	}

	if sess.ClientConn != nil {
		// 设置写超时
		g.configMu.RLock()
		writeTimeout := g.config.ConnectionPool.WriteTimeout
		g.configMu.RUnlock()

		sess.ClientConn.SetWriteDeadline(time.Now().Add(writeTimeout))
		n, err := sess.ClientConn.Write(packet.Payload)
		if err != nil {
			logger.Debug("failed to write to client session", zap.Int64("session_id", sessionID), zap.Error(err))
		} else if sess.BytesOut != nil {
			// Update bytesOut counter (for access log statistics)
			atomic.AddInt64(sess.BytesOut, int64(n))
		}
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
