package middleware

import (
	"context"
	"sync"
	"time"

	"github.com/SkynetNext/game-gateway/internal/logger"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// AccessLogEntry represents a single access log entry
// This is used for structured logging and can be sent to Kafka/ELK for analysis
type AccessLogEntry struct {
	Timestamp   time.Time `json:"timestamp"`
	TraceID     string    `json:"trace_id,omitempty"`
	SpanID      string    `json:"span_id,omitempty"`
	RemoteAddr  string    `json:"remote_addr"`
	SessionID   int64     `json:"session_id,omitempty"`
	ServerType  int       `json:"server_type,omitempty"`
	WorldID     int       `json:"world_id,omitempty"`
	BackendAddr string    `json:"backend_addr,omitempty"`
	DurationMs  int64     `json:"duration_ms"`
	Status      string    `json:"status"` // success, error, timeout, rejected
	BytesIn     int64     `json:"bytes_in,omitempty"`
	BytesOut    int64     `json:"bytes_out,omitempty"`
	Error       string    `json:"error,omitempty"`
}

// AccessLogger handles access log recording with batching support
type AccessLogger struct {
	logChan       chan *AccessLogEntry
	batchSize     int
	flushInterval time.Duration
	wg            sync.WaitGroup
	stopChan      chan struct{}
}

var (
	// Global access logger instance
	globalAccessLogger *AccessLogger
	once               sync.Once
)

// InitAccessLogger initializes the global access logger
// batchSize: number of logs to accumulate before flushing
// flushInterval: maximum time to wait before flushing
func InitAccessLogger(batchSize int, flushInterval time.Duration) {
	once.Do(func() {
		globalAccessLogger = &AccessLogger{
			logChan:       make(chan *AccessLogEntry, batchSize*2), // Buffer 2x batch size
			batchSize:     batchSize,
			flushInterval: flushInterval,
			stopChan:      make(chan struct{}),
		}
		globalAccessLogger.start()
	})
}

// LogAccess records an access log entry
// This is non-blocking - if buffer is full, log is dropped to prevent blocking main flow
func LogAccess(ctx context.Context, entry *AccessLogEntry) {
	if globalAccessLogger == nil {
		// Fallback: log directly if access logger not initialized
		logAccessDirect(ctx, entry)
		return
	}

	// Extract trace context
	span := trace.SpanFromContext(ctx)
	if span.SpanContext().IsValid() {
		entry.TraceID = span.SpanContext().TraceID().String()
		entry.SpanID = span.SpanContext().SpanID().String()
	}

	entry.Timestamp = time.Now()

	// Non-blocking send
	select {
	case globalAccessLogger.logChan <- entry:
	default:
		// Buffer full, log warning and drop entry
		logger.L.Warn("access log buffer full, dropping entry",
			zap.String("remote_addr", entry.RemoteAddr),
		)
	}
}

// logAccessDirect logs access entry directly (fallback when batcher not initialized)
func logAccessDirect(ctx context.Context, entry *AccessLogEntry) {
	span := trace.SpanFromContext(ctx)
	if span.SpanContext().IsValid() {
		entry.TraceID = span.SpanContext().TraceID().String()
		entry.SpanID = span.SpanContext().SpanID().String()
	}

	fields := []zap.Field{
		zap.String("remote_addr", entry.RemoteAddr),
		zap.Int64("duration_ms", entry.DurationMs),
		zap.String("status", entry.Status),
	}

	if entry.TraceID != "" {
		fields = append(fields, zap.String("trace_id", entry.TraceID))
	}
	if entry.SpanID != "" {
		fields = append(fields, zap.String("span_id", entry.SpanID))
	}
	if entry.SessionID != 0 {
		fields = append(fields, zap.Int64("session_id", entry.SessionID))
	}
	if entry.ServerType != 0 {
		fields = append(fields, zap.Int("server_type", entry.ServerType))
	}
	if entry.WorldID != 0 {
		fields = append(fields, zap.Int("world_id", entry.WorldID))
	}
	if entry.BackendAddr != "" {
		fields = append(fields, zap.String("backend_addr", entry.BackendAddr))
	}
	if entry.BytesIn > 0 {
		fields = append(fields, zap.Int64("bytes_in", entry.BytesIn))
	}
	if entry.BytesOut > 0 {
		fields = append(fields, zap.Int64("bytes_out", entry.BytesOut))
	}
	if entry.Error != "" {
		fields = append(fields, zap.String("error", entry.Error))
	}

	logger.L.Info("access_log", fields...)
}

// start starts the batch processing goroutine
func (al *AccessLogger) start() {
	al.wg.Add(1)
	go al.processBatches()
}

// processBatches processes access logs in batches
func (al *AccessLogger) processBatches() {
	defer al.wg.Done()

	batch := make([]*AccessLogEntry, 0, al.batchSize)
	ticker := time.NewTicker(al.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-al.stopChan:
			// Flush remaining logs
			if len(batch) > 0 {
				al.flushBatch(batch)
			}
			return
		case entry := <-al.logChan:
			batch = append(batch, entry)
			if len(batch) >= al.batchSize {
				al.flushBatch(batch)
				batch = batch[:0]
			}
		case <-ticker.C:
			if len(batch) > 0 {
				al.flushBatch(batch)
				batch = batch[:0]
			}
		}
	}
}

// flushBatch flushes a batch of access logs
// In production, this could send to Kafka, ELK, or other log aggregation systems
func (al *AccessLogger) flushBatch(batch []*AccessLogEntry) {
	// For now, log each entry individually
	// In production, you could:
	// 1. Send to Kafka topic
	// 2. Send to ELK via HTTP
	// 3. Write to file for Fluentd/Fluent Bit to collect
	for _, entry := range batch {
		fields := []zap.Field{
			zap.String("remote_addr", entry.RemoteAddr),
			zap.Int64("duration_ms", entry.DurationMs),
			zap.String("status", entry.Status),
		}

		if entry.TraceID != "" {
			fields = append(fields, zap.String("trace_id", entry.TraceID))
		}
		if entry.SpanID != "" {
			fields = append(fields, zap.String("span_id", entry.SpanID))
		}
		if entry.SessionID != 0 {
			fields = append(fields, zap.Int64("session_id", entry.SessionID))
		}
		if entry.ServerType != 0 {
			fields = append(fields, zap.Int("server_type", entry.ServerType))
		}
		if entry.WorldID != 0 {
			fields = append(fields, zap.Int("world_id", entry.WorldID))
		}
		if entry.BackendAddr != "" {
			fields = append(fields, zap.String("backend_addr", entry.BackendAddr))
		}
		if entry.BytesIn > 0 {
			fields = append(fields, zap.Int64("bytes_in", entry.BytesIn))
		}
		if entry.BytesOut > 0 {
			fields = append(fields, zap.Int64("bytes_out", entry.BytesOut))
		}
		if entry.Error != "" {
			fields = append(fields, zap.String("error", entry.Error))
		}

		logger.L.Info("access_log", fields...)
	}
}

// Shutdown gracefully shuts down the access logger
func ShutdownAccessLogger() {
	if globalAccessLogger != nil {
		close(globalAccessLogger.stopChan)
		globalAccessLogger.wg.Wait()
	}
}
