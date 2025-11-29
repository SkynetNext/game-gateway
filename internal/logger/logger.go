package logger

import (
	"context"
	"os"
	"sync"
	"time"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// OTel Log Data Model Severity Numbers
// https://opentelemetry.io/docs/specs/otel/logs/data-model/#field-severitynumber
const (
	SeverityTrace = 1
	SeverityDebug = 5
	SeverityInfo  = 9
	SeverityWarn  = 13
	SeverityError = 17
	SeverityFatal = 21
)

// ServiceInfo holds service metadata for structured logging
// Follows OpenTelemetry Semantic Conventions for Resources
// https://opentelemetry.io/docs/specs/semconv/resource/
// Note: service.instance.id is added by Fluent Bit from Pod name (infrastructure layer)
type ServiceInfo struct {
	Name      string // service.name
	Namespace string // service.namespace (from POD_NAMESPACE env var)
	Version   string // service.version
}

var (
	// L is the global logger instance
	L *zap.Logger

	// serviceInfo holds service metadata
	serviceInfo ServiceInfo

	// initOnce ensures Init is called only once
	initOnce sync.Once
)

// Config holds logger configuration
type Config struct {
	Level       string
	ServiceInfo ServiceInfo
}

// Init initializes the global logger with OpenTelemetry-compatible format
// This should be called once at application startup
func Init(cfg Config) error {
	var initErr error
	initOnce.Do(func() {
		serviceInfo = cfg.ServiceInfo

		// Get service name (application defines its own name)
		if serviceInfo.Name == "" {
			serviceInfo.Name = "game-gateway"
		}
		// Note: service.instance.id is added by Fluent Bit from Pod name (infrastructure layer)
		// Always prefer POD_NAMESPACE from environment (infrastructure layer)
		// This ensures consistency with tracing.go and K8s metadata
		// Only use provided Namespace if POD_NAMESPACE is not available
		if podNamespace := os.Getenv("POD_NAMESPACE"); podNamespace != "" {
			serviceInfo.Namespace = podNamespace
		}

		var zapLevel zapcore.Level
		switch cfg.Level {
		case "debug":
			zapLevel = zapcore.DebugLevel
		case "info":
			zapLevel = zapcore.InfoLevel
		case "warn":
			zapLevel = zapcore.WarnLevel
		case "error":
			zapLevel = zapcore.ErrorLevel
		default:
			zapLevel = zapcore.InfoLevel
		}

		// Create encoder config following OpenTelemetry Log Data Model
		encoderConfig := zapcore.EncoderConfig{
			// Timestamp: Unix nanoseconds (OTel standard)
			TimeKey:    "timestamp",
			EncodeTime: zapcore.EpochNanosTimeEncoder,

			// Level: mapped to OTel severity
			LevelKey:    "severity_text",
			EncodeLevel: zapcore.CapitalLevelEncoder,

			// Message
			MessageKey: "body",

			// Caller info
			CallerKey:    "caller",
			EncodeCaller: zapcore.ShortCallerEncoder,

			// Stack trace for errors
			StacktraceKey: "stacktrace",

			// Separator for console output
			ConsoleSeparator: " ",
		}

		// Build core with JSON encoder for structured output
		core := zapcore.NewCore(
			zapcore.NewJSONEncoder(encoderConfig),
			zapcore.AddSync(os.Stdout),
			zapLevel,
		)

		// Build logger with default fields (OTel Resource attributes)
		// Application-level attributes (should be set by application)
		L = zap.New(core, zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel)).With(
			// OpenTelemetry Resource Semantic Conventions
			zap.String("service.name", serviceInfo.Name),           // Application defines its name
			zap.String("service.namespace", serviceInfo.Namespace), // From POD_NAMESPACE (K8s namespace)
			// Note: service.instance.id is added by Fluent Bit (infrastructure layer)
		)

		// Add version if available (build-time information)
		if serviceInfo.Version != "" {
			L = L.With(zap.String("service.version", serviceInfo.Version))
		}
	})
	return initErr
}

// InitSimple initializes logger with just a log level (backward compatible)
func InitSimple(level string) error {
	return Init(Config{Level: level})
}

// Sync flushes any buffered log entries
func Sync() {
	if L != nil {
		_ = L.Sync()
	}
}

// GetServiceInfo returns the current service info
func GetServiceInfo() ServiceInfo {
	return serviceInfo
}

// WithContext creates a child logger with trace context
// This is the ONLY way to log with trace correlation
// Usage: logger.WithContext(ctx).Info("message", fields...)
func WithContext(ctx context.Context) *zap.Logger {
	if L == nil {
		return zap.NewNop()
	}

	span := trace.SpanFromContext(ctx)
	if !span.SpanContext().IsValid() {
		return L
	}

	// Add OTel trace context fields
	return L.With(
		zap.String("trace_id", span.SpanContext().TraceID().String()),
		zap.String("span_id", span.SpanContext().SpanID().String()),
		// Add severity_number for OTel compatibility (will be overwritten per log)
	)
}

// ContextLogger is a helper struct for logging with context
// It caches the trace context to avoid repeated extraction
type ContextLogger struct {
	logger  *zap.Logger
	traceID string
	spanID  string
}

// NewContextLogger creates a new context-aware logger
// Use this when you need to log multiple times with the same context
func NewContextLogger(ctx context.Context) *ContextLogger {
	cl := &ContextLogger{
		logger: L,
	}

	if L == nil {
		cl.logger = zap.NewNop()
		return cl
	}

	span := trace.SpanFromContext(ctx)
	if span.SpanContext().IsValid() {
		cl.traceID = span.SpanContext().TraceID().String()
		cl.spanID = span.SpanContext().SpanID().String()
		cl.logger = L.With(
			zap.String("trace_id", cl.traceID),
			zap.String("span_id", cl.spanID),
		)
	}

	return cl
}

// TraceID returns the trace ID
func (cl *ContextLogger) TraceID() string {
	return cl.traceID
}

// SpanID returns the span ID
func (cl *ContextLogger) SpanID() string {
	return cl.spanID
}

// Debug logs at debug level with severity_number
func (cl *ContextLogger) Debug(msg string, fields ...zap.Field) {
	fields = append(fields, zap.Int("severity_number", SeverityDebug))
	cl.logger.WithOptions(zap.AddCallerSkip(1)).Debug(msg, fields...)
}

// Info logs at info level with severity_number
func (cl *ContextLogger) Info(msg string, fields ...zap.Field) {
	fields = append(fields, zap.Int("severity_number", SeverityInfo))
	cl.logger.WithOptions(zap.AddCallerSkip(1)).Info(msg, fields...)
}

// Warn logs at warn level with severity_number
func (cl *ContextLogger) Warn(msg string, fields ...zap.Field) {
	fields = append(fields, zap.Int("severity_number", SeverityWarn))
	cl.logger.WithOptions(zap.AddCallerSkip(1)).Warn(msg, fields...)
}

// Error logs at error level with severity_number
func (cl *ContextLogger) Error(msg string, fields ...zap.Field) {
	fields = append(fields, zap.Int("severity_number", SeverityError))
	cl.logger.WithOptions(zap.AddCallerSkip(1)).Error(msg, fields...)
}

// ============================================================================
// Convenience functions (for backward compatibility and simple cases)
// These create a new ContextLogger for each call - use NewContextLogger for
// multiple logs with the same context
// ============================================================================

// Info logs at info level (no trace context)
func Info(msg string, fields ...zap.Field) {
	if L != nil {
		fields = append(fields, zap.Int("severity_number", SeverityInfo))
		L.WithOptions(zap.AddCallerSkip(1)).Info(msg, fields...)
	}
}

// Error logs at error level (no trace context)
func Error(msg string, fields ...zap.Field) {
	if L != nil {
		fields = append(fields, zap.Int("severity_number", SeverityError))
		L.WithOptions(zap.AddCallerSkip(1)).Error(msg, fields...)
	}
}

// Warn logs at warn level (no trace context)
func Warn(msg string, fields ...zap.Field) {
	if L != nil {
		fields = append(fields, zap.Int("severity_number", SeverityWarn))
		L.WithOptions(zap.AddCallerSkip(1)).Warn(msg, fields...)
	}
}

// Debug logs at debug level (no trace context)
func Debug(msg string, fields ...zap.Field) {
	if L != nil {
		fields = append(fields, zap.Int("severity_number", SeverityDebug))
		L.WithOptions(zap.AddCallerSkip(1)).Debug(msg, fields...)
	}
}

// ============================================================================
// DEPRECATED: Legacy functions for backward compatibility
// Use NewContextLogger(ctx) instead for better performance
// ============================================================================

// InfoWithTrace logs at Info level with trace context
// Deprecated: Use NewContextLogger(ctx).Info() instead
func InfoWithTrace(ctx context.Context, msg string, fields ...zap.Field) {
	NewContextLogger(ctx).Info(msg, fields...)
}

// ErrorWithTrace logs at Error level with trace context
// Deprecated: Use NewContextLogger(ctx).Error() instead
func ErrorWithTrace(ctx context.Context, msg string, fields ...zap.Field) {
	NewContextLogger(ctx).Error(msg, fields...)
}

// WarnWithTrace logs at Warn level with trace context
// Deprecated: Use NewContextLogger(ctx).Warn() instead
func WarnWithTrace(ctx context.Context, msg string, fields ...zap.Field) {
	NewContextLogger(ctx).Warn(msg, fields...)
}

// DebugWithTrace logs at Debug level with trace context
// Deprecated: Use NewContextLogger(ctx).Debug() instead
func DebugWithTrace(ctx context.Context, msg string, fields ...zap.Field) {
	NewContextLogger(ctx).Debug(msg, fields...)
}

// ============================================================================
// Structured Event Logging
// For important business events that need consistent structure
// ============================================================================

// EventType defines the type of event for structured logging
type EventType string

const (
	EventSessionEstablished EventType = "session.established"
	EventSessionClosed      EventType = "session.closed"
	EventConnectionRejected EventType = "connection.rejected"
	EventRoutingError       EventType = "routing.error"
	EventCircuitBreakerOpen EventType = "circuit_breaker.open"
	EventRateLimitHit       EventType = "rate_limit.hit"
	EventBackendError       EventType = "backend.error"
)

// Event logs a structured event with consistent format
// This is the preferred way to log important business events
func Event(ctx context.Context, eventType EventType, fields ...zap.Field) {
	cl := NewContextLogger(ctx)
	fields = append(fields,
		zap.String("event.type", string(eventType)),
		zap.Int64("event.timestamp", time.Now().UnixNano()),
		zap.Int("severity_number", SeverityInfo),
	)
	// Skip 2 levels: Event() -> ContextLogger.logger.Info()
	cl.logger.WithOptions(zap.AddCallerSkip(2)).Info(string(eventType), fields...)
}

// EventError logs a structured error event
func EventError(ctx context.Context, eventType EventType, err error, fields ...zap.Field) {
	cl := NewContextLogger(ctx)
	fields = append(fields,
		zap.String("event.type", string(eventType)),
		zap.Int64("event.timestamp", time.Now().UnixNano()),
		zap.Error(err),
		zap.Int("severity_number", SeverityError),
	)
	// Skip 2 levels: EventError() -> ContextLogger.logger.Error()
	cl.logger.WithOptions(zap.AddCallerSkip(2)).Error(string(eventType), fields...)
}
