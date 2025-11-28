package gateway

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SkynetNext/game-gateway/internal/buffer"
	"github.com/SkynetNext/game-gateway/internal/circuitbreaker"
	"github.com/SkynetNext/game-gateway/internal/config"
	"github.com/SkynetNext/game-gateway/internal/logger"
	"github.com/SkynetNext/game-gateway/internal/metrics"
	"github.com/SkynetNext/game-gateway/internal/middleware"
	"github.com/SkynetNext/game-gateway/internal/pool"
	"github.com/SkynetNext/game-gateway/internal/protocol"
	"github.com/SkynetNext/game-gateway/internal/ratelimit"
	"github.com/SkynetNext/game-gateway/internal/redis"
	"github.com/SkynetNext/game-gateway/internal/retry"
	"github.com/SkynetNext/game-gateway/internal/router"
	"github.com/SkynetNext/game-gateway/internal/session"
	"github.com/SkynetNext/game-gateway/internal/tracing"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// Gateway represents the game gateway service
type Gateway struct {
	config  *config.Config
	podName string

	// Components
	sessionManager *session.Manager
	poolManager    *pool.Manager
	router         *router.Router
	redisClient    *redis.Client

	// Rate limiting and circuit breaking
	rateLimiter     *ratelimit.Limiter
	ipLimiter       *ratelimit.IPLimiter               // IP-based rate limiter
	circuitBreakers map[string]*circuitbreaker.Breaker // backend address -> breaker
	breakerMu       sync.RWMutex

	// Configuration hot reload
	configMu sync.RWMutex // Protects config updates

	// Network
	listener      net.Listener
	metricsServer *http.Server

	// Session ID generation
	sessionSeq uint64 // Session sequence counter (atomic)
	startTime  int64  // Start timestamp (seconds)
	podHash    uint16 // Pod name hash (16 bits, computed at startup)

	// State
	draining int32 // Atomic: 0=Running, 1=Draining
	wg       sync.WaitGroup
}

// New creates a new gateway instance
func New(cfg *config.Config, podName string) (*Gateway, error) {
	// Initialize components
	sessionMgr := session.NewManager()
	poolMgr := pool.NewManager(&cfg.ConnectionPool)
	rtr := router.NewRouter(&cfg.Routing)
	redisCli := redis.NewClient(&cfg.Redis)

	// Test Redis connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := redisCli.Ping(ctx); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	// Compute podHash once at startup
	podHash := uint16(crc32.ChecksumIEEE([]byte(podName)) & 0xFFFF)

	// Initialize rate limiter (use max_connections from config)
	rateLimiter := ratelimit.NewLimiter(int64(cfg.ConnectionPool.MaxConnections))

	// Initialize IP-based rate limiter
	ipLimiter := ratelimit.NewIPLimiter(
		cfg.Security.MaxConnectionsPerIP,
		cfg.Security.ConnectionRateLimit,
	)

	return &Gateway{
		config:          cfg,
		podName:         podName,
		startTime:       time.Now().Unix(),
		podHash:         podHash,
		sessionManager:  sessionMgr,
		poolManager:     poolMgr,
		router:          rtr,
		redisClient:     redisCli,
		rateLimiter:     rateLimiter,
		ipLimiter:       ipLimiter,
		circuitBreakers: make(map[string]*circuitbreaker.Breaker),
	}, nil
}

// Start starts the gateway service
func (g *Gateway) Start(ctx context.Context) error {
	// 1. Load initial routing rules and realm mapping from Redis
	if err := g.loadConfigFromRedis(ctx); err != nil {
		return fmt.Errorf("failed to load initial config: %w", err)
	}

	// 2. Start Redis refresh loop
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		g.redisClient.RefreshLoop(
			ctx,
			g.config.Routing.RefreshInterval,
			g.onRoutingRulesUpdate,
			g.onRealmMappingUpdate,
		)
	}()

	// 3. Start connection pool cleanup
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		g.poolManager.StartCleanup(ctx, 1*time.Minute)
	}()

	// 4. Start session cleanup
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				g.sessionManager.CleanupIdle(30 * time.Minute)
			}
		}
	}()

	// 5. Initialize access logger with batching
	middleware.InitAccessLogger(100, 5*time.Second) // Batch 100 logs or flush every 5 seconds

	// 5. Start metrics and health check server
	if err := g.startMetricsServer(ctx); err != nil {
		return fmt.Errorf("failed to start metrics server: %w", err)
	}

	// 6. Start business listener
	if err := g.startListener(ctx); err != nil {
		return fmt.Errorf("failed to start listener: %w", err)
	}

	return nil
}

// Shutdown gracefully shuts down the gateway
func (g *Gateway) Shutdown(ctx context.Context) error {
	// 1. Enter drain mode
	atomic.StoreInt32(&g.draining, 1)

	// 2. Stop accepting new connections
	if g.listener != nil {
		g.listener.Close()
	}

	// 3. Wait for active connections to close (with timeout)
	done := make(chan struct{})
	go func() {
		g.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// All connections closed
	case <-ctx.Done():
		// Timeout reached
	}

	// 4. Close all connection pools
	if err := g.poolManager.Close(); err != nil {
		return fmt.Errorf("failed to close connection pools: %w", err)
	}

	// 5. Close Redis connection
	if err := g.redisClient.Close(); err != nil {
		return fmt.Errorf("failed to close Redis connection: %w", err)
	}

	// 6. Shutdown metrics server
	if g.metricsServer != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := g.metricsServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("failed to shutdown metrics server: %w", err)
		}
	}

	// 7. Shutdown access logger
	middleware.ShutdownAccessLogger()

	return nil
}

// loadConfigFromRedis loads initial configuration from Redis
func (g *Gateway) loadConfigFromRedis(ctx context.Context) error {
	// Load routing rules
	rules, err := g.redisClient.LoadRoutingRules(ctx)
	if err != nil {
		return fmt.Errorf("failed to load routing rules: %w", err)
	}
	for _, rule := range rules {
		g.router.UpdateRule(rule)
	}

	// Realm mapping will be used when routing game requests
	// It's loaded but not directly used here (used in routing logic)

	return nil
}

// onRoutingRulesUpdate handles routing rules updates from Redis
// Enhanced: adds error logging and metrics
func (g *Gateway) onRoutingRulesUpdate(rules map[int]*router.RoutingRule) {
	if rules == nil {
		metrics.ConfigRefreshErrors.WithLabelValues("routing_rules").Inc()
		logger.L.Warn("received nil routing rules, skipping update") // No context available here
		return
	}
	for _, rule := range rules {
		g.router.UpdateRule(rule)
	}
}

// onRealmMappingUpdate handles realm mapping updates from Redis
// Enhanced: adds error logging and metrics
func (g *Gateway) onRealmMappingUpdate(mapping map[int32]string) {
	if mapping == nil {
		metrics.ConfigRefreshErrors.WithLabelValues("realm_mapping").Inc()
		logger.L.Warn("received nil realm mapping, skipping update") // No context available here
		return
	}

	// Realm mapping is used during routing, can be stored in router if needed
	// For now, it's accessed directly from Redis when needed
	logger.L.Debug("realm mapping updated", // No context available here
		zap.Int("count", len(mapping)),
	)
}

// startMetricsServer starts the metrics and health check HTTP server
func (g *Gateway) startMetricsServer(_ context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", g.healthHandler)
	mux.HandleFunc("/ready", g.readyHandler)
	mux.Handle("/metrics", promhttp.Handler()) // Prometheus metrics endpoint

	g.metricsServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", g.config.Server.HealthCheckPort),
		Handler: mux,
	}

	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		if err := g.metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.L.Error("metrics server error", // No context available here
				zap.Error(err),
			)
		}
	}()

	logger.L.Info("metrics server started", // No context available here
		zap.Int("port", g.config.Server.HealthCheckPort),
	)

	return nil
}

// startListener starts the business listener
func (g *Gateway) startListener(ctx context.Context) error {
	var err error
	g.listener, err = net.Listen("tcp", g.config.Server.ListenAddr)
	if err != nil {
		return err
	}

	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		g.acceptLoop(ctx)
	}()

	return nil
}

// acceptLoop accepts incoming connections
func (g *Gateway) acceptLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			// Set accept timeout to allow context cancellation check
			if tcpListener, ok := g.listener.(*net.TCPListener); ok {
				tcpListener.SetDeadline(time.Now().Add(1 * time.Second))
			}

			conn, err := g.listener.Accept()
			if err != nil {
				// Check if listener was closed (normal shutdown)
				if atomic.LoadInt32(&g.draining) == 1 {
					return
				}
				// Check for timeout (expected when checking context)
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					continue
				}
				// Log other errors
				logger.L.Warn("accept connection error",
					zap.Error(err),
				)
				continue
			}

			// Set connection timeouts
			if err := conn.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
				conn.Close()
				logger.L.Debug("failed to set initial read deadline",
					zap.Error(err),
				)
				continue
			}

			// Handle connection in goroutine
			g.wg.Add(1)
			go func(c net.Conn) {
				defer g.wg.Done()
				g.handleConnection(ctx, c)
			}(conn)
		}
	}
}

// handleConnection handles a client connection
func (g *Gateway) handleConnection(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	remoteAddr := conn.RemoteAddr().String()
	startTime := time.Now()

	// Create span for distributed tracing
	ctx, span := tracing.StartSpan(ctx, "gateway.handle_connection")
	defer span.End()

	// Extract IP address from remote address
	ip := extractIP(remoteAddr)

	// IP-based rate limiting: check if connection from this IP is allowed
	if !g.ipLimiter.Allow(ip) {
		logger.WarnWithTrace(ctx, "IP rate limit exceeded",
			zap.String("remote_addr", remoteAddr),
			zap.String("ip", ip),
		)
		metrics.RateLimitRejected.Inc()
		middleware.LogAccess(ctx, &middleware.AccessLogEntry{
			RemoteAddr: remoteAddr,
			DurationMs: time.Since(startTime).Milliseconds(),
			Status:     "rejected",
			Error:      "IP rate limit exceeded",
		})
		return
	}
	defer g.ipLimiter.Release(ip)

	// Global rate limiting: check if connection is allowed
	if !g.rateLimiter.Allow() {
		logger.WarnWithTrace(ctx, "connection rate limit exceeded",
			zap.String("remote_addr", remoteAddr),
			zap.Int64("max_connections", g.rateLimiter.Max()),
			zap.Int64("current_connections", g.rateLimiter.Current()),
		)
		metrics.RateLimitRejected.Inc()
		middleware.LogAccess(ctx, &middleware.AccessLogEntry{
			RemoteAddr: remoteAddr,
			DurationMs: time.Since(startTime).Milliseconds(),
			Status:     "rejected",
			Error:      "connection rate limit exceeded",
		})
		return
	}
	defer g.rateLimiter.Release()

	metrics.TotalConnections.Inc()
	metrics.ActiveConnections.Inc()
	defer metrics.ActiveConnections.Dec()

	logger.InfoWithTrace(ctx, "new connection",
		zap.String("remote_addr", remoteAddr),
	)

	// Create sniffed connection
	sniffConn := protocol.NewSniffConn(conn)

	// Sniff protocol
	protoType, _, err := sniffConn.Sniff()
	if err != nil {
		logger.WarnWithTrace(ctx, "failed to sniff protocol",
			zap.String("remote_addr", remoteAddr),
			zap.Error(err),
		)
		middleware.LogAccess(ctx, &middleware.AccessLogEntry{
			RemoteAddr: remoteAddr,
			DurationMs: time.Since(startTime).Milliseconds(),
			Status:     "error",
			Error:      err.Error(),
		})
		return
	}

	// Handle based on protocol type
	switch protoType {
	case protocol.ProtocolWebSocket:
		g.handleWebSocketConnection(ctx, sniffConn, nil)
	case protocol.ProtocolHTTP:
		g.handleHTTPConnection(ctx, sniffConn)
	case protocol.ProtocolTCP:
		g.handleTCPConnection(ctx, sniffConn)
	default:
		logger.DebugWithTrace(ctx, "unknown protocol",
			zap.String("remote_addr", remoteAddr),
			zap.String("protocol", fmt.Sprint(protoType)),
		)
		middleware.LogAccess(ctx, &middleware.AccessLogEntry{
			RemoteAddr: remoteAddr,
			DurationMs: time.Since(startTime).Milliseconds(),
			Status:     "rejected",
			Error:      "unknown protocol",
		})
		return
	}
}

func (g *Gateway) handleHTTPConnection(ctx context.Context, conn *protocol.SniffConn) {
	startTime := time.Now()
	remoteAddr := conn.RemoteAddr().String()

	req, err := http.ReadRequest(conn.Reader())
	if err != nil {
		logger.DebugWithTrace(ctx, "failed to parse HTTP request",
			zap.String("remote_addr", remoteAddr),
			zap.Error(err),
		)
		_ = writeHTTPResponse(conn.Conn, http.StatusBadRequest, "text/plain; charset=utf-8", []byte("Malformed HTTP request\n"), nil)
		middleware.LogAccess(ctx, &middleware.AccessLogEntry{
			RemoteAddr: remoteAddr,
			DurationMs: time.Since(startTime).Milliseconds(),
			Status:     "rejected",
			Error:      "malformed http request",
		})
		return
	}
	defer req.Body.Close()

	if isWebSocketUpgrade(req) {
		g.handleWebSocketConnection(ctx, conn, req)
		return
	}

	body := []byte("Unsupported protocol. Gateway expects WebSocket or TCP payloads.\n")
	_ = writeHTTPResponse(conn.Conn, http.StatusUpgradeRequired, "text/plain; charset=utf-8", body, map[string]string{
		"Connection": "close",
	})
	middleware.LogAccess(ctx, &middleware.AccessLogEntry{
		RemoteAddr: remoteAddr,
		DurationMs: time.Since(startTime).Milliseconds(),
		Status:     "rejected",
		Error:      "unsupported protocol (http)",
	})
}

func (g *Gateway) handleWebSocketConnection(ctx context.Context, conn *protocol.SniffConn, req *http.Request) {
	startTime := time.Now()
	remoteAddr := conn.RemoteAddr().String()

	var err error
	if req == nil {
		req, err = http.ReadRequest(conn.Reader())
		if err != nil {
			logger.DebugWithTrace(ctx, "failed to parse WebSocket handshake",
				zap.String("remote_addr", remoteAddr),
				zap.Error(err),
			)
			_ = writeHTTPResponse(conn.Conn, http.StatusBadRequest, "text/plain; charset=utf-8", []byte("Malformed WebSocket handshake\n"), nil)
			middleware.LogAccess(ctx, &middleware.AccessLogEntry{
				RemoteAddr: remoteAddr,
				DurationMs: time.Since(startTime).Milliseconds(),
				Status:     "rejected",
				Error:      "malformed websocket handshake",
			})
			return
		}
	}
	defer req.Body.Close()

	if !isWebSocketRequest(req) {
		logger.DebugWithTrace(ctx, "invalid WebSocket upgrade",
			zap.String("remote_addr", remoteAddr),
		)
		_ = writeHTTPResponse(conn.Conn, http.StatusBadRequest, "text/plain; charset=utf-8", []byte("Invalid WebSocket upgrade request\n"), nil)
		middleware.LogAccess(ctx, &middleware.AccessLogEntry{
			RemoteAddr: remoteAddr,
			DurationMs: time.Since(startTime).Milliseconds(),
			Status:     "rejected",
			Error:      "invalid websocket upgrade",
		})
		return
	}

	acceptKey, err := computeWebSocketAcceptKey(req.Header.Get("Sec-WebSocket-Key"))
	if err != nil {
		logger.DebugWithTrace(ctx, "failed to compute WebSocket key",
			zap.String("remote_addr", remoteAddr),
			zap.Error(err),
		)
		_ = writeHTTPResponse(conn.Conn, http.StatusBadRequest, "text/plain; charset=utf-8", []byte("Invalid WebSocket key\n"), nil)
		middleware.LogAccess(ctx, &middleware.AccessLogEntry{
			RemoteAddr: remoteAddr,
			DurationMs: time.Since(startTime).Milliseconds(),
			Status:     "rejected",
			Error:      "invalid websocket key",
		})
		return
	}

	subprotocol := selectWebSocketSubprotocol(req.Header.Values("Sec-WebSocket-Protocol"))
	if err := writeWebSocketHandshake(conn.Conn, acceptKey, subprotocol); err != nil {
		logger.WarnWithTrace(ctx, "failed to write WebSocket handshake response",
			zap.String("remote_addr", remoteAddr),
			zap.Error(err),
		)
		return
	}

	g.configMu.RLock()
	maxMessageSize := g.config.Security.MaxMessageSize
	g.configMu.RUnlock()

	wsConn := protocol.NewWebSocketConn(conn.Conn, conn.Reader(), maxMessageSize)
	g.handleTCPConnection(ctx, wsConn)
}

func isWebSocketUpgrade(req *http.Request) bool {
	return strings.EqualFold(req.Header.Get("Upgrade"), "websocket")
}

func isWebSocketRequest(req *http.Request) bool {
	if req.Method != http.MethodGet {
		return false
	}
	if !strings.EqualFold(req.Header.Get("Upgrade"), "websocket") {
		return false
	}
	if !headerContainsToken(req.Header, "Connection", "Upgrade") {
		return false
	}
	key := strings.TrimSpace(req.Header.Get("Sec-WebSocket-Key"))
	if key == "" {
		return false
	}
	if version := strings.TrimSpace(req.Header.Get("Sec-WebSocket-Version")); version != "" && version != "13" {
		return false
	}
	return true
}

const websocketGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

func computeWebSocketAcceptKey(clientKey string) (string, error) {
	clientKey = strings.TrimSpace(clientKey)
	if clientKey == "" {
		return "", fmt.Errorf("empty Sec-WebSocket-Key")
	}
	h := sha1.Sum([]byte(clientKey + websocketGUID))
	return base64.StdEncoding.EncodeToString(h[:]), nil
}

func writeHTTPResponse(w io.Writer, status int, contentType string, body []byte, extraHeaders map[string]string) error {
	if body == nil {
		body = []byte{}
	}
	var resp bytes.Buffer
	text := http.StatusText(status)
	if text == "" {
		text = "Status"
	}
	fmt.Fprintf(&resp, "HTTP/1.1 %d %s\r\n", status, text)
	fmt.Fprintf(&resp, "Content-Length: %d\r\n", len(body))
	if contentType != "" {
		fmt.Fprintf(&resp, "Content-Type: %s\r\n", contentType)
	}
	hasConnectionHeader := false
	for k, v := range extraHeaders {
		fmt.Fprintf(&resp, "%s: %s\r\n", k, v)
		if strings.EqualFold(k, "Connection") {
			hasConnectionHeader = true
		}
	}
	if !hasConnectionHeader {
		resp.WriteString("Connection: close\r\n")
	}
	resp.WriteString("\r\n")
	resp.Write(body)
	_, err := w.Write(resp.Bytes())
	return err
}

func writeWebSocketHandshake(w io.Writer, acceptKey, subprotocol string) error {
	var resp bytes.Buffer
	resp.WriteString("HTTP/1.1 101 Switching Protocols\r\n")
	resp.WriteString("Upgrade: websocket\r\n")
	resp.WriteString("Connection: Upgrade\r\n")
	fmt.Fprintf(&resp, "Sec-WebSocket-Accept: %s\r\n", acceptKey)
	if subprotocol != "" {
		fmt.Fprintf(&resp, "Sec-WebSocket-Protocol: %s\r\n", subprotocol)
	}
	resp.WriteString("\r\n")
	_, err := w.Write(resp.Bytes())
	return err
}

func selectWebSocketSubprotocol(values []string) string {
	for _, raw := range values {
		for _, token := range strings.Split(raw, ",") {
			if proto := strings.TrimSpace(token); proto != "" {
				return proto
			}
		}
	}
	return ""
}

func headerContainsToken(h http.Header, key, token string) bool {
	for _, v := range h.Values(key) {
		for _, part := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}

// handleTCPConnection handles TCP connection (game protocol)
func (g *Gateway) handleTCPConnection(ctx context.Context, conn net.Conn) {
	startTime := time.Now()
	remoteAddr := conn.RemoteAddr().String()

	// Create span for TCP connection handling
	ctx, span := tracing.StartSpan(ctx, "gateway.handle_tcp_connection")
	defer span.End()

	// Read first packet to get routing information
	// Use buffer pool and validate message size
	// Get maxMessageSize from config (thread-safe read, supports hot reload)
	g.configMu.RLock()
	maxMessageSize := g.config.Security.MaxMessageSize
	g.configMu.RUnlock()

	clientHeader, messageData, err := protocol.ReadFullPacket(conn, maxMessageSize)
	if err != nil {
		if err == protocol.ErrMessageTooLarge {
			logger.WarnWithTrace(ctx, "message too large, rejecting connection",
				zap.String("remote_addr", remoteAddr),
				zap.Error(err),
			)
			metrics.RoutingErrors.WithLabelValues("message_too_large").Inc()
			middleware.LogAccess(ctx, &middleware.AccessLogEntry{
				RemoteAddr: remoteAddr,
				DurationMs: time.Since(startTime).Milliseconds(),
				Status:     "rejected",
				Error:      "message too large",
			})
		} else if err != io.EOF {
			logger.WarnWithTrace(ctx, "failed to read client packet",
				zap.String("remote_addr", remoteAddr),
				zap.Error(err),
			)
			middleware.LogAccess(ctx, &middleware.AccessLogEntry{
				RemoteAddr: remoteAddr,
				DurationMs: time.Since(startTime).Milliseconds(),
				Status:     "error",
				Error:      err.Error(),
			})
		}
		return
	}

	// Track if we used pooled buffer (for cleanup)
	var pooledBuf []byte
	if messageData != nil && cap(messageData) >= 8192 {
		pooledBuf = messageData[:cap(messageData)]
	}
	defer func() {
		// Return buffer to pool if it was pooled
		if pooledBuf != nil {
			buffer.Put(pooledBuf)
		}
	}()

	// Extract routing information from ServerID
	_, worldID, serverType, instID := protocol.ExtractServerIDInfo(clientHeader.ServerID)

	// Generate session ID (will be assigned by Gateway)
	sessionID := g.generateSessionID()

	// Route to backend service using serverType + worldID + instID
	backendAddr, err := g.routeToBackend(ctx, int(serverType), int(worldID), int(instID))
	if err != nil {
		logger.ErrorWithTrace(ctx, "routing failed",
			zap.String("remote_addr", remoteAddr),
			zap.Int("server_type", int(serverType)),
			zap.Int("world_id", int(worldID)),
			zap.Int("inst_id", int(instID)),
			zap.Error(err),
		)
		metrics.RoutingErrors.WithLabelValues("not_found").Inc()
		middleware.LogAccess(ctx, &middleware.AccessLogEntry{
			RemoteAddr: remoteAddr,
			ServerType: int(serverType),
			WorldID:    int(worldID),
			DurationMs: time.Since(startTime).Milliseconds(),
			Status:     "error",
			Error:      err.Error(),
		})
		return
	}

	// Check circuit breaker
	breaker := g.getOrCreateBreaker(backendAddr)
	if !breaker.Allow() {
		logger.WarnWithTrace(ctx, "circuit breaker is open",
			zap.String("remote_addr", remoteAddr),
			zap.String("backend_addr", backendAddr),
		)
		metrics.RoutingErrors.WithLabelValues("circuit_breaker_open").Inc()
		middleware.LogAccess(ctx, &middleware.AccessLogEntry{
			RemoteAddr:  remoteAddr,
			ServerType:  int(serverType),
			WorldID:     int(worldID),
			BackendAddr: backendAddr,
			DurationMs:  time.Since(startTime).Milliseconds(),
			Status:      "rejected",
			Error:       "circuit breaker open",
		})
		return
	}

	// Get connection from pool with retry
	var backendConn *pool.Connection
	retryCfg := retry.RetryConfig{
		MaxRetries: g.config.ConnectionPool.MaxRetries,
		RetryDelay: g.config.ConnectionPool.RetryDelay,
	}
	err = retry.Do(ctx, retryCfg, func() error {
		var err error
		backendConn, err = g.poolManager.GetConnection(ctx, backendAddr)
		if err == nil {
			breaker.RecordSuccess()
		} else {
			breaker.RecordFailure()
		}
		return err
	})
	if err != nil {
		logger.ErrorWithTrace(ctx, "failed to get backend connection after retries",
			zap.String("remote_addr", remoteAddr),
			zap.String("backend_addr", backendAddr),
			zap.Error(err),
		)
		metrics.RoutingErrors.WithLabelValues("connection_failed").Inc()
		middleware.LogAccess(ctx, &middleware.AccessLogEntry{
			RemoteAddr:  remoteAddr,
			ServerType:  int(serverType),
			WorldID:     int(worldID),
			BackendAddr: backendAddr,
			DurationMs:  time.Since(startTime).Milliseconds(),
			Status:      "error",
			Error:       err.Error(),
		})
		return
	}
	// Ensure connection is returned to pool even on panic
	defer func() {
		if r := recover(); r != nil {
			// Log panic and ensure connection is returned
			logger.ErrorWithTrace(ctx, "panic in handleTCPConnection",
				zap.Any("panic", r),
				zap.String("remote_addr", remoteAddr),
			)
			// Re-panic after cleanup
			panic(r)
		}
		if backendConn != nil {
			g.poolManager.PutConnection(backendAddr, backendConn)
		}
	}()

	// Record request latency
	latency := time.Since(startTime)
	metrics.RequestLatency.WithLabelValues(fmt.Sprintf("%d", serverType)).Observe(latency.Seconds())

	logger.InfoWithTrace(ctx, "session established",
		zap.Int64("session_id", sessionID),
		zap.String("remote_addr", remoteAddr),
		zap.String("backend_addr", backendAddr),
		zap.Int("server_type", int(serverType)),
		zap.Int("world_id", int(worldID)),
		zap.Duration("latency", latency),
	)

	// Extract Trace ID and Span ID from context for propagation to backend
	// Only propagate if enabled in config (for compatibility with existing cluster)
	var traceContext map[string]string
	g.configMu.RLock()
	enableTracePropagation := g.config.Tracing.EnableTracePropagation
	g.configMu.RUnlock()

	if enableTracePropagation {
		traceContext = extractTraceContext(ctx)
	}

	// Send first message to backend
	// Note: gateway_id is not needed since sessionID is globally unique
	// Backend can identify Gateway through connection source if needed
	if err := protocol.WriteServerMessage(backendConn.Conn(), int32(clientHeader.MessageID), sessionID, messageData, traceContext); err != nil {
		logger.ErrorWithTrace(ctx, "failed to send initial message to backend",
			zap.Int64("session_id", sessionID),
			zap.String("backend_addr", backendAddr),
			zap.Error(err),
		)
		middleware.LogAccess(ctx, &middleware.AccessLogEntry{
			RemoteAddr:  remoteAddr,
			SessionID:   sessionID,
			ServerType:  int(serverType),
			WorldID:     int(worldID),
			BackendAddr: backendAddr,
			DurationMs:  time.Since(startTime).Milliseconds(),
			Status:      "error",
			Error:       err.Error(),
		})
		return
	}

	metrics.MessagesProcessed.WithLabelValues("client_to_backend", fmt.Sprintf("%d", serverType)).Inc()

	// Create session
	sess := &session.Session{
		SessionID:    sessionID,
		ClientConn:   conn,
		BackendConn:  backendConn.Conn(),
		ServiceType:  fmt.Sprintf("%d", serverType),
		WorldID:      int32(worldID),
		InstID:       int32(instID),
		CreatedAt:    time.Now(),
		LastActiveAt: time.Now(),
		State:        session.SessionStateConnected,
	}

	g.sessionManager.Add(sess)
	metrics.ActiveSessions.Inc()

	// Log successful session establishment
	middleware.LogAccess(ctx, &middleware.AccessLogEntry{
		RemoteAddr:  remoteAddr,
		SessionID:   sessionID,
		ServerType:  int(serverType),
		WorldID:     int(worldID),
		BackendAddr: backendAddr,
		DurationMs:  time.Since(startTime).Milliseconds(),
		Status:      "success",
	})

	defer func() {
		g.sessionManager.Remove(sessionID)
		metrics.ActiveSessions.Dec()
		duration := time.Since(startTime)
		logger.InfoWithTrace(ctx, "session closed",
			zap.Int64("session_id", sessionID),
			zap.String("remote_addr", remoteAddr),
			zap.Duration("duration", duration),
		)
		// Log session closure
		middleware.LogAccess(ctx, &middleware.AccessLogEntry{
			RemoteAddr:  remoteAddr,
			SessionID:   sessionID,
			ServerType:  int(serverType),
			WorldID:     int(worldID),
			BackendAddr: backendAddr,
			DurationMs:  duration.Milliseconds(),
			Status:      "closed",
		})
	}()

	// Forward subsequent data bidirectionally
	g.forwardConnection(ctx, sess)
}

// routeToBackend routes request to backend service using serverType + worldID + instID
func (g *Gateway) routeToBackend(ctx context.Context, serverType, worldID, instID int) (string, error) {
	// Use router to get backend address
	// For account/version services, router may only use serverType
	// For game service, router may use serverType + worldID + instID
	return g.router.RouteByServerID(ctx, serverType, worldID, instID)
}

// extractIP extracts IP address from "host:port" format
func extractIP(addr string) string {
	if idx := strings.LastIndex(addr, ":"); idx != -1 {
		return addr[:idx]
	}
	return addr
}

// extractTraceContext extracts Trace ID and Span ID from context for propagation to backend
// Optimized: early return when no trace context to avoid allocation
func extractTraceContext(ctx context.Context) map[string]string {
	span := trace.SpanFromContext(ctx)
	if !span.SpanContext().IsValid() {
		return nil // Return nil to avoid allocation when no trace context
	}

	traceID := span.SpanContext().TraceID()
	spanID := span.SpanContext().SpanID()

	// Check if trace ID is valid (not all zeros)
	// TraceID.IsValid() returns true if trace ID is not all zeros
	if traceID.IsValid() {
		traceContext := make(map[string]string, 2)
		// Convert to hex string (required for protocol)
		traceContext["trace_id"] = traceID.String()
		if spanID.IsValid() {
			traceContext["span_id"] = spanID.String()
		}
		return traceContext
	}

	return nil
}

// forwardConnection forwards data bidirectionally between client and backend
// Optimized: uses bufio for buffered I/O to reduce system calls
// Fixed: adds context support for cancellation and timeout control
// Fixed: uses configurable timeouts from config (supports hot reload)
func (g *Gateway) forwardConnection(ctx context.Context, sess *session.Session) {
	// Get timeouts from config (thread-safe read)
	g.configMu.RLock()
	readTimeout := g.config.ConnectionPool.ReadTimeout
	writeTimeout := g.config.ConnectionPool.WriteTimeout
	maxMessageSize := g.config.Security.MaxMessageSize
	g.configMu.RUnlock()

	// Create buffered readers/writers for better I/O performance
	clientReader := protocol.NewSniffConn(sess.ClientConn) // Already has buffering
	backendWriter := sess.BackendConn

	// Use context with timeout for connection lifecycle
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Forward client -> backend
	go func() {
		defer cancel()
		// Ensure backend connection is closed and returned to pool
		defer func() {
			if sess.BackendConn != nil {
				sess.BackendConn.Close()
			}
		}()

		// Set periodic read deadline
		ticker := time.NewTicker(readTimeout / 2)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Refresh read deadline
				if err := sess.ClientConn.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
					return
				}
			default:
				// Read complete client packet with size validation
				// Use maxMessageSize from config (supports hot reload)
				clientHeader, messageData, err := protocol.ReadFullPacket(clientReader, maxMessageSize)
				if err != nil {
					if err == protocol.ErrMessageTooLarge {
						logger.WarnWithTrace(ctx, "message too large, closing connection",
							zap.Int64("session_id", sess.SessionID),
							zap.Error(err),
						)
						metrics.RoutingErrors.WithLabelValues("message_too_large").Inc()
					} else if err != io.EOF {
						logger.DebugWithTrace(ctx, "client read error",
							zap.Int64("session_id", sess.SessionID),
							zap.Error(err),
						)
					}
					return
				}

				// Track pooled buffer for cleanup
				var pooledBuf []byte
				if messageData != nil && cap(messageData) >= 8192 {
					pooledBuf = messageData[:cap(messageData)]
				}

				// Update last active time
				sess.LastActiveAt = time.Now()

				// Set write deadline before writing
				if err := backendWriter.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
					logger.DebugWithTrace(ctx, "failed to set write deadline",
						zap.Int64("session_id", sess.SessionID),
						zap.Error(err),
					)
					return
				}

				// Extract Trace ID and Span ID from context for propagation to backend
				// Only propagate if enabled in config (for compatibility with existing cluster)
				var traceContext map[string]string
				g.configMu.RLock()
				enableTracePropagation := g.config.Tracing.EnableTracePropagation
				g.configMu.RUnlock()

				if enableTracePropagation {
					traceContext = extractTraceContext(ctx)
				}

				// Write to backend in server message format
				if err := protocol.WriteServerMessage(backendWriter, int32(clientHeader.MessageID), sess.SessionID, messageData, traceContext); err != nil {
					// Return buffer to pool on error
					if pooledBuf != nil {
						buffer.Put(pooledBuf)
					}
					logger.DebugWithTrace(ctx, "backend write error",
						zap.Int64("session_id", sess.SessionID),
						zap.Error(err),
					)
					return
				}

				// Return buffer to pool after successful write
				if pooledBuf != nil {
					buffer.Put(pooledBuf)
				}

				metrics.MessagesProcessed.WithLabelValues("client_to_backend", sess.ServiceType).Inc()
			}
		}
	}()

	// Forward backend -> client
	// Backend sends server message format, we need to extract and forward to client
	defer sess.ClientConn.Close()

	// Set initial read deadline
	if err := sess.BackendConn.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
		logger.DebugWithTrace(ctx, "failed to set initial read deadline",
			zap.Int64("session_id", sess.SessionID),
			zap.Error(err),
		)
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
			// Refresh read deadline periodically
			if err := sess.BackendConn.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
				return
			}

			// Read server message header (16 bytes) - optimized: single ReadFull call
			serverHeader, err := protocol.ReadServerMessageHeader(sess.BackendConn)
			if err != nil {
				if err != io.EOF {
					logger.DebugWithTrace(ctx, "backend read error",
						zap.Int64("session_id", sess.SessionID),
						zap.Error(err),
					)
				}
				return
			}

			// Read Gate message header - always use original format (9 bytes) for compatibility
			// Backend services (cluster) always send original format, never extended format
			// Even if Gateway sends extended format to backend, backend will only read first 9 bytes
			// and will reply with original format (9 bytes)
			gateHeader, gateErr := protocol.ReadGateMsgHeader(sess.BackendConn)
			if gateErr != nil {
				if gateErr != io.EOF {
					logger.DebugWithTrace(ctx, "gate header read error",
						zap.Int64("session_id", sess.SessionID),
						zap.Error(gateErr),
					)
				}
				return
			}
			_ = gateHeader.OpCode // OpCode (currently not used, but read for protocol correctness)

			// Calculate message data length: totalLength - GateMsgHeaderSize (9 bytes)
			// Backend always uses original format, so always subtract 9 bytes
			expectedDataLen := int(serverHeader.Length) - protocol.GateMsgHeaderSize
			messageDataLen := expectedDataLen
			if messageDataLen < 0 {
				logger.WarnWithTrace(ctx, "invalid message length",
					zap.Int64("session_id", sess.SessionID),
					zap.Int32("total_length", serverHeader.Length),
				)
				return
			}

			// Read message data - use buffer pool for large messages
			var messageData []byte
			var pooledBuf []byte
			if messageDataLen > 0 {
				if messageDataLen <= 8192 {
					// Use buffer pool for messages <= 8KB
					pooledBuf = buffer.Get()
					messageData = pooledBuf[:messageDataLen]
				} else {
					// Allocate new buffer for large messages
					messageData = make([]byte, messageDataLen)
				}
				if _, err := io.ReadFull(sess.BackendConn, messageData); err != nil {
					// Return buffer to pool if it was pooled
					if pooledBuf != nil {
						buffer.Put(pooledBuf)
					}
					if err != io.EOF {
						logger.DebugWithTrace(ctx, "message data read error",
							zap.Int64("session_id", sess.SessionID),
							zap.Int("data_len", messageDataLen),
							zap.Error(err),
						)
					}
					return
				}
			}

			// Set write deadline before writing to client
			if err := sess.ClientConn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
				if pooledBuf != nil {
					buffer.Put(pooledBuf)
				}
				return
			}

			// Write to client in client message format
			// Client format: [length(2)] [messageId(2)] [serverId(4)] [data]
			// We need to reconstruct serverId from session info or use 0
			clientHeader := protocol.ClientMessageHeader{
				Length:    uint16(messageDataLen),
				MessageID: uint16(serverHeader.Type),
				ServerID:  0, // Can be reconstructed if needed
			}
			if err := binary.Write(sess.ClientConn, binary.LittleEndian, clientHeader.Length); err != nil {
				if pooledBuf != nil {
					buffer.Put(pooledBuf)
				}
				logger.DebugWithTrace(ctx, "client write error (length)",
					zap.Int64("session_id", sess.SessionID),
					zap.Error(err),
				)
				return
			}
			if err := binary.Write(sess.ClientConn, binary.LittleEndian, clientHeader.MessageID); err != nil {
				if pooledBuf != nil {
					buffer.Put(pooledBuf)
				}
				logger.DebugWithTrace(ctx, "client write error (message_id)",
					zap.Int64("session_id", sess.SessionID),
					zap.Error(err),
				)
				return
			}
			if err := binary.Write(sess.ClientConn, binary.LittleEndian, clientHeader.ServerID); err != nil {
				if pooledBuf != nil {
					buffer.Put(pooledBuf)
				}
				logger.DebugWithTrace(ctx, "client write error (server_id)",
					zap.Int64("session_id", sess.SessionID),
					zap.Error(err),
				)
				return
			}
			if len(messageData) > 0 {
				if _, err := sess.ClientConn.Write(messageData); err != nil {
					if pooledBuf != nil {
						buffer.Put(pooledBuf)
					}
					logger.DebugWithTrace(ctx, "client write error (data)",
						zap.Int64("session_id", sess.SessionID),
						zap.Error(err),
					)
					return
				}
			}

			// Return buffer to pool after use (if it was pooled)
			if pooledBuf != nil {
				buffer.Put(pooledBuf)
			}

			// Update last active time
			sess.LastActiveAt = time.Now()
			metrics.MessagesProcessed.WithLabelValues("backend_to_client", sess.ServiceType).Inc()
		}
	}
}

// generateSessionID generates a unique session ID
// For Deployment + HPA, we don't have fixed instID, so we use a simpler format:
// Format: (podHash << 48) + (timestamp << 16) + sequence
// - bit 48-63: podHash (16 bits) - Hash of Pod name for uniqueness across Pods (computed at startup)
// - bit 16-47: timestamp (32 bits) - Start time in seconds (wraps around in year 2106)
// - bit 0-15: sequence (16 bits) - Incremental sequence number (wraps at 65k)
//
// Alternative: Since GatewayID (Pod name) already uniquely identifies the Gateway,
// we could simplify to just timestamp + sequence, but keeping podHash ensures
// sessionID uniqueness even if two Pods start at the same second.
func (g *Gateway) generateSessionID() int64 {
	// Use atomic increment for thread-safe sequence counter
	seq := atomic.AddUint64(&g.sessionSeq, 1)

	// Wrap around at 65k (should be rare)
	seq16 := uint16(seq & 0xFFFF)
	if seq16 == 0 {
		// Skip 0, start from 1
		seq16 = 1
		atomic.StoreUint64(&g.sessionSeq, 1)
	}

	// Format: (podHash << 48) + (timestamp << 16) + sequence
	sessionID := (int64(g.podHash) << 48) | (g.startTime << 16) | int64(seq16)

	return sessionID
}

// healthHandler handles health check requests
func (g *Gateway) healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

// readyHandler handles readiness probe requests
func (g *Gateway) readyHandler(w http.ResponseWriter, r *http.Request) {
	if atomic.LoadInt32(&g.draining) == 1 {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("Draining"))
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Ready"))
}
