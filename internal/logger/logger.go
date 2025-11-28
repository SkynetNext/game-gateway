package logger

import (
	"context"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	// L is the global logger instance
	L *zap.Logger
)

// Init initializes the global logger
func Init(level string) error {
	var zapLevel zapcore.Level
	switch level {
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

	config := zap.NewProductionConfig()
	config.Level = zap.NewAtomicLevelAt(zapLevel)
	config.EncoderConfig.TimeKey = "timestamp"
	config.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	var err error
	L, err = config.Build()
	if err != nil {
		return err
	}

	return nil
}

// Sync flushes any buffered log entries
func Sync() {
	if L != nil {
		_ = L.Sync()
	}
}

// WithTrace extracts trace context from context.Context and adds trace_id and span_id fields
// This enables log correlation with distributed tracing (OpenTelemetry)
func WithTrace(ctx context.Context, fields ...zap.Field) []zap.Field {
	span := trace.SpanFromContext(ctx)
	if span.SpanContext().IsValid() {
		traceID := span.SpanContext().TraceID().String()
		spanID := span.SpanContext().SpanID().String()
		fields = append(fields,
			zap.String("trace_id", traceID),
			zap.String("span_id", spanID),
		)
	}
	return fields
}

// InfoWithTrace logs at Info level with trace context
func InfoWithTrace(ctx context.Context, msg string, fields ...zap.Field) {
	L.Info(msg, WithTrace(ctx, fields...)...)
}

// ErrorWithTrace logs at Error level with trace context
func ErrorWithTrace(ctx context.Context, msg string, fields ...zap.Field) {
	L.Error(msg, WithTrace(ctx, fields...)...)
}

// WarnWithTrace logs at Warn level with trace context
func WarnWithTrace(ctx context.Context, msg string, fields ...zap.Field) {
	L.Warn(msg, WithTrace(ctx, fields...)...)
}

// DebugWithTrace logs at Debug level with trace context
func DebugWithTrace(ctx context.Context, msg string, fields ...zap.Field) {
	L.Debug(msg, WithTrace(ctx, fields...)...)
}
