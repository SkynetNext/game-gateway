package gateway

import (
	"context"
	"strconv"
	"time"

	gateway "github.com/SkynetNext/game-gateway/api"
	"github.com/SkynetNext/game-gateway/internal/logger"
	"go.uber.org/zap"
)

func (g *Gateway) notifyBackendConnect(ctx context.Context, backendAddr string, sessionID int64, clientIP string, protocol string) error {
	if !g.config.Server.UseGrpc {
		return nil
	}

	g.configMu.RLock()
	gatewayName := g.gatewayName
	g.configMu.RUnlock()

	packet := &gateway.GamePacket{
		SessionId: sessionID,
		MsgId:     0,
		Payload:   nil,
		Metadata: map[string]string{
			"packet_type":  "connect",
			"gateway_name": gatewayName,
			"client_ip":    clientIP,
			"protocol":     protocol,
			"connect_time": strconv.FormatInt(time.Now().UnixMilli(), 10),
		},
	}

	err := g.grpcManager.Send(ctx, backendAddr, packet)
	if err != nil {
		logger.Error("Failed to notify backend of client connect",
			zap.String("backend", backendAddr),
			zap.Int64("sessionID", sessionID),
			zap.String("clientIP", clientIP),
			zap.Error(err))
		return err
	}

	logger.Debug("Successfully notified backend of client connect",
		zap.String("backend", backendAddr),
		zap.Int64("sessionID", sessionID),
		zap.String("clientIP", clientIP),
		zap.String("protocol", protocol))

	return nil
}

func (g *Gateway) notifyBackendDisconnect(ctx context.Context, backendAddr string, sessionID int64, reason string) {
	if !g.config.Server.UseGrpc {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	packet := &gateway.GamePacket{
		SessionId: sessionID,
		MsgId:     0,
		Payload:   nil,
		Metadata: map[string]string{
			"packet_type":    "disconnect",
			"reason":        reason,
			"disconnect_time": strconv.FormatInt(time.Now().UnixMilli(), 10),
		},
	}

	err := g.grpcManager.Send(ctx, backendAddr, packet)
	if err != nil {
		logger.Warn("Failed to notify backend of client disconnect",
			zap.String("backend", backendAddr),
			zap.Int64("sessionID", sessionID),
			zap.String("reason", reason),
			zap.Error(err))
		return
	}

	logger.Debug("Successfully notified backend of client disconnect",
		zap.String("backend", backendAddr),
		zap.Int64("sessionID", sessionID),
		zap.String("reason", reason))
}

