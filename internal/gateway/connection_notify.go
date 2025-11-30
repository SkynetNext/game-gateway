package gateway

import (
	"context"
	"time"

	"github.com/SkynetNext/game-gateway/internal/logger"
	"go.uber.org/zap"
)

// notifyBackendConnect 通知后端服务器客户端连接建立
// 业界最佳实践：连接建立时通知一次，携带完整元数据
func (g *Gateway) notifyBackendConnect(ctx context.Context, backendAddr string, sessionID int64, clientIP string, protocol string) error {
	// 如果不是 gRPC 模式，跳过通知（TCP 模式下由 Gate 服务器处理）
	if !g.config.Server.UseGrpc {
		return nil
	}

	// 调用 gRPC Manager 的 NotifyConnect 方法
	err := g.grpcManager.NotifyConnect(ctx, backendAddr, sessionID, clientIP, protocol)
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

// notifyBackendDisconnect 通知后端服务器客户端断开连接
// 业界最佳实践：连接断开时通知，及时清理资源
func (g *Gateway) notifyBackendDisconnect(ctx context.Context, backendAddr string, sessionID int64, reason string) {
	// 如果不是 gRPC 模式，跳过通知
	if !g.config.Server.UseGrpc {
		return
	}

	// 设置超时，避免阻塞断开流程
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	// 调用 gRPC Manager 的 NotifyDisconnect 方法
	err := g.grpcManager.NotifyDisconnect(ctx, backendAddr, sessionID, reason)
	if err != nil {
		// 断开通知失败不是致命错误，只记录日志
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

