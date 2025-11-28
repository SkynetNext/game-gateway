package gateway

import (
	"context"
	"time"

	gateway "github.com/SkynetNext/game-gateway/api"
	"github.com/SkynetNext/game-gateway/internal/logger"
	"github.com/SkynetNext/game-gateway/internal/protocol"
	"go.opentelemetry.io/otel/trace"
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
		_, err := sess.ClientConn.Write(packet.Payload)
		if err != nil {
			logger.Debug("failed to write to client session", zap.Int64("session_id", sessionID), zap.Error(err))
		}
	}
}

// forwardGrpcConnection forwards data from client to GameServer via gRPC
func (g *Gateway) forwardGrpcConnection(ctx context.Context, sessID int64, clientConn *protocol.SniffConn, backendAddr string, log *logger.ContextLogger, bytesIn, bytesOut *int64) {
	defer func() {
		// Cleanup session
		g.sessionManager.Remove(sessID)
	}()

	g.configMu.RLock()
	maxMessageSize := g.config.Security.MaxMessageSize
	g.configMu.RUnlock()

	for {
		// Read Packet
		header, data, err := protocol.ReadFullPacket(clientConn, maxMessageSize)
		if err != nil {
			return
		}

		// Send via gRPC
		// data returned by ReadFullPacket is the Body.
		packet := &gateway.GamePacket{
			SessionId: sessID,
			MsgId:     int32(header.MessageID),
			Payload:   data,
		}

		// Extract trace context from ctx and add to packet
		if span := trace.SpanFromContext(ctx); span.SpanContext().IsValid() {
			packet.TraceId = span.SpanContext().TraceID().String()
			packet.SpanId = span.SpanContext().SpanID().String()
		}

		err = g.grpcManager.Send(ctx, backendAddr, packet)
		if err != nil {
			logger.Error("failed to send grpc packet", zap.Error(err))
			return
		}

		*bytesIn += int64(len(data))
	}
}
