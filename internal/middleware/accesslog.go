package middleware

import (
	"context"
	"sync"

	"github.com/SkynetNext/game-gateway/internal/logger"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// AccessLogEntry represents a single access log entry
// Follows OpenTelemetry Semantic Conventions for HTTP/RPC
// https://opentelemetry.io/docs/specs/semconv/
type AccessLogEntry struct {
	// Timing
	Timestamp  int64 `json:"timestamp"`   // Unix nanoseconds (OTel standard)
	DurationNs int64 `json:"duration_ns"` // Duration in nanoseconds

	// Trace Context (OTel standard)
	TraceID string `json:"trace_id,omitempty"`
	SpanID  string `json:"span_id,omitempty"`

	// Network attributes (OTel semantic conventions)
	ClientAddr  string `json:"client.address"` // client.address
	BackendAddr string `json:"server.address"` // server.address

	// Session attributes
	SessionID int64 `json:"session.id,omitempty"`

	// Routing attributes
	ServerType int `json:"server.type,omitempty"`
	WorldID    int `json:"world.id,omitempty"`

	// Request/Response metrics
	BytesIn  int64 `json:"network.io.receive,omitempty"` // network.io.receive
	BytesOut int64 `json:"network.io.send,omitempty"`    // network.io.send

	// Status
	Status string `json:"status"` // success, error, timeout, rejected, closed
	Error  string `json:"error.message,omitempty"`
}

// accessLogger handles access log recording
// Simplified: no batching, direct structured logging
// Reason: Zap is already highly optimized, batching adds complexity
// For production, logs are collected by Fluent Bit/Vector → Loki
type accessLogger struct {
	enabled bool
}

var (
	globalAccessLogger *accessLogger
	accessLoggerOnce   sync.Once
)

// InitAccessLogger initializes the global access logger
// Simplified API - just enable/disable
func InitAccessLogger(enabled bool) {
	accessLoggerOnce.Do(func() {
		globalAccessLogger = &accessLogger{
			enabled: enabled,
		}
		if enabled {
			logger.Info("access logger initialized")
		}
	})
}

// LogAccess records an access log entry
// This is a single, unified logging point - no duplicate logs
func LogAccess(ctx context.Context, entry *AccessLogEntry) {
	if globalAccessLogger == nil {
		logger.Warn("LogAccess called but globalAccessLogger is nil")
		return
	}
	if !globalAccessLogger.enabled {
		logger.Debug("LogAccess called but access logger is disabled")
		return
	}

	// Extract trace context
	span := trace.SpanFromContext(ctx)
	if span.SpanContext().IsValid() {
		entry.TraceID = span.SpanContext().TraceID().String()
		entry.SpanID = span.SpanContext().SpanID().String()
	}

	// Build fields array - only include non-zero/non-empty fields
	fields := make([]zap.Field, 0, 16)

	// Always include these
	// Note: timestamp is automatically added by Zap logger (from logger config)
	fields = append(fields,
		zap.String("client.address", entry.ClientAddr),
		zap.String("status", entry.Status),
	)

	// Duration
	if entry.DurationNs > 0 {
		fields = append(fields, zap.Int64("duration_ns", entry.DurationNs))
		// Also add duration_ms for human readability
		fields = append(fields, zap.Float64("duration_ms", float64(entry.DurationNs)/1e6))
	}

	// Trace context
	if entry.TraceID != "" {
		fields = append(fields, zap.String("trace_id", entry.TraceID))
	}
	if entry.SpanID != "" {
		fields = append(fields, zap.String("span_id", entry.SpanID))
	}

	// Session
	if entry.SessionID != 0 {
		fields = append(fields, zap.Int64("session.id", entry.SessionID))
	}

	// Routing
	if entry.ServerType != 0 {
		fields = append(fields, zap.Int("server.type", entry.ServerType))
	}
	if entry.WorldID != 0 {
		fields = append(fields, zap.Int("world.id", entry.WorldID))
	}

	// Backend
	if entry.BackendAddr != "" {
		fields = append(fields, zap.String("server.address", entry.BackendAddr))
	}

	// I/O metrics
	if entry.BytesIn > 0 {
		fields = append(fields, zap.Int64("network.io.receive", entry.BytesIn))
	}
	if entry.BytesOut > 0 {
		fields = append(fields, zap.Int64("network.io.send", entry.BytesOut))
	}

	// Error
	if entry.Error != "" {
		fields = append(fields, zap.String("error.message", entry.Error))
	}

	// Add severity_number for OTel
	fields = append(fields, zap.Int("severity_number", logger.SeverityInfo))

	// Log with consistent message key
	logger.L.Info("access_log", fields...)
}

// ShutdownAccessLogger gracefully shuts down the access logger
// Simplified: no batching means no pending logs to flush
func ShutdownAccessLogger() {
	if globalAccessLogger != nil {
		logger.Info("access logger shutdown")
	}
}

// ============================================================================
// Helper functions for common access log patterns
// ============================================================================

// LogSessionEstablished logs a successful session establishment
// This is the ONLY log for session establishment - no duplicate
func LogSessionEstablished(ctx context.Context, sessionID int64, clientAddr, backendAddr string, serverType, worldID int, durationNs int64) {
	LogAccess(ctx, &AccessLogEntry{
		SessionID:   sessionID,
		ClientAddr:  clientAddr,
		BackendAddr: backendAddr,
		ServerType:  serverType,
		WorldID:     worldID,
		DurationNs:  durationNs,
		Status:      "success",
	})
}

// LogSessionClosed logs a session closure
// This is the ONLY log for session closure - no duplicate
func LogSessionClosed(ctx context.Context, sessionID int64, clientAddr, backendAddr string, serverType, worldID int, durationNs, bytesIn, bytesOut int64) {
	LogAccess(ctx, &AccessLogEntry{
		SessionID:   sessionID,
		ClientAddr:  clientAddr,
		BackendAddr: backendAddr,
		ServerType:  serverType,
		WorldID:     worldID,
		DurationNs:  durationNs,
		BytesIn:     bytesIn,
		BytesOut:    bytesOut,
		Status:      "closed",
	})
}

// LogConnectionRejected logs a rejected connection
func LogConnectionRejected(ctx context.Context, clientAddr, reason string) {
	LogAccess(ctx, &AccessLogEntry{
		ClientAddr: clientAddr,
		Status:     "rejected",
		Error:      reason,
	})
}

// LogRoutingError logs a routing error
func LogRoutingError(ctx context.Context, clientAddr string, serverType, worldID int, err error, durationNs int64) {
	LogAccess(ctx, &AccessLogEntry{
		ClientAddr: clientAddr,
		ServerType: serverType,
		WorldID:    worldID,
		DurationNs: durationNs,
		Status:     "error",
		Error:      err.Error(),
	})
}

// LogCircuitBreakerOpen logs circuit breaker rejection
func LogCircuitBreakerOpen(ctx context.Context, clientAddr, backendAddr string, serverType, worldID int, durationNs int64) {
	LogAccess(ctx, &AccessLogEntry{
		ClientAddr:  clientAddr,
		BackendAddr: backendAddr,
		ServerType:  serverType,
		WorldID:     worldID,
		DurationNs:  durationNs,
		Status:      "rejected",
		Error:       "circuit breaker open",
	})
}

// LogBackendError logs a backend connection/communication error
func LogBackendError(ctx context.Context, sessionID int64, clientAddr, backendAddr string, serverType, worldID int, err error, durationNs int64) {
	LogAccess(ctx, &AccessLogEntry{
		SessionID:   sessionID,
		ClientAddr:  clientAddr,
		BackendAddr: backendAddr,
		ServerType:  serverType,
		WorldID:     worldID,
		DurationNs:  durationNs,
		Status:      "error",
		Error:       err.Error(),
	})
}
