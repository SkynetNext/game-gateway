package gateway

import (
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SkynetNext/game-gateway/internal/buffer"
	"github.com/SkynetNext/game-gateway/internal/config"
	"github.com/SkynetNext/game-gateway/internal/logger"
	"github.com/SkynetNext/game-gateway/internal/metrics"
	"github.com/SkynetNext/game-gateway/internal/pool"
	"github.com/SkynetNext/game-gateway/internal/protocol"
	"github.com/SkynetNext/game-gateway/internal/redis"
	"github.com/SkynetNext/game-gateway/internal/router"
	"github.com/SkynetNext/game-gateway/internal/session"
	"github.com/prometheus/client_golang/prometheus/promhttp"
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

	return &Gateway{
		config:         cfg,
		podName:        podName,
		startTime:      time.Now().Unix(),
		podHash:        podHash,
		sessionManager: sessionMgr,
		poolManager:    poolMgr,
		router:         rtr,
		redisClient:    redisCli,
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
func (g *Gateway) onRoutingRulesUpdate(rules map[int]*router.RoutingRule) {
	for _, rule := range rules {
		g.router.UpdateRule(rule)
	}
}

// onRealmMappingUpdate handles realm mapping updates from Redis
func (g *Gateway) onRealmMappingUpdate(mapping map[int32]string) {
	// Realm mapping is used during routing, can be stored in router if needed
	// For now, it's accessed directly from Redis when needed
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
			logger.L.Error("metrics server error",
				zap.Error(err),
			)
		}
	}()

	logger.L.Info("metrics server started",
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
		conn, err := g.listener.Accept()
		if err != nil {
			// Check if listener was closed (normal shutdown)
			if atomic.LoadInt32(&g.draining) == 1 {
				return
			}
			// Log error and continue
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

// handleConnection handles a client connection
func (g *Gateway) handleConnection(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	remoteAddr := conn.RemoteAddr().String()
	metrics.TotalConnections.Inc()
	metrics.ActiveConnections.Inc()
	defer metrics.ActiveConnections.Dec()

	logger.L.Info("new connection",
		zap.String("remote_addr", remoteAddr),
	)

	// Create sniffed connection
	sniffConn := protocol.NewSniffConn(conn)

	// Sniff protocol
	protoType, peeked, err := sniffConn.Sniff()
	if err != nil {
		logger.L.Warn("failed to sniff protocol",
			zap.String("remote_addr", remoteAddr),
			zap.Error(err),
		)
		return
	}

	// Handle based on protocol type
	switch protoType {
	case protocol.ProtocolHTTP, protocol.ProtocolWebSocket:
		logger.L.Debug("unsupported protocol",
			zap.String("remote_addr", remoteAddr),
			zap.String("protocol", fmt.Sprint(protoType)),
		)
		// TODO: Handle HTTP/WebSocket (can be forwarded to HttpProxy if needed)
		return
	case protocol.ProtocolTCP:
		g.handleTCPConnection(ctx, sniffConn, peeked)
	default:
		logger.L.Debug("unknown protocol",
			zap.String("remote_addr", remoteAddr),
			zap.String("protocol", fmt.Sprint(protoType)),
		)
		return
	}
}

// handleTCPConnection handles TCP connection (game protocol)
func (g *Gateway) handleTCPConnection(ctx context.Context, conn *protocol.SniffConn, _ []byte) {
	startTime := time.Now()
	remoteAddr := conn.RemoteAddr().String()

	// Read first packet to get routing information
	clientHeader, messageData, err := protocol.ReadFullPacket(conn)
	if err != nil {
		if err != io.EOF {
			logger.L.Warn("failed to read client packet",
				zap.String("remote_addr", remoteAddr),
				zap.Error(err),
			)
		}
		return
	}

	// Extract routing information from ServerID
	_, worldID, serverType, instID := protocol.ExtractServerIDInfo(clientHeader.ServerID)

	// Generate session ID (will be assigned by Gateway)
	sessionID := g.generateSessionID()

	// Route to backend service using serverType + worldID + instID
	backendAddr, err := g.routeToBackend(ctx, int(serverType), int(worldID), int(instID))
	if err != nil {
		logger.L.Error("routing failed",
			zap.String("remote_addr", remoteAddr),
			zap.Int("server_type", int(serverType)),
			zap.Int("world_id", int(worldID)),
			zap.Int("inst_id", int(instID)),
			zap.Error(err),
		)
		metrics.RoutingErrors.WithLabelValues("not_found").Inc()
		return
	}

	// Get connection from pool
	backendConn, err := g.poolManager.GetConnection(ctx, backendAddr)
	if err != nil {
		logger.L.Error("failed to get backend connection",
			zap.String("remote_addr", remoteAddr),
			zap.String("backend_addr", backendAddr),
			zap.Error(err),
		)
		metrics.RoutingErrors.WithLabelValues("connection_failed").Inc()
		return
	}
	defer g.poolManager.PutConnection(backendAddr, backendConn)

	logger.L.Info("session established",
		zap.Int64("session_id", sessionID),
		zap.String("remote_addr", remoteAddr),
		zap.String("backend_addr", backendAddr),
		zap.Int("server_type", int(serverType)),
		zap.Int("world_id", int(worldID)),
		zap.Duration("latency", time.Since(startTime)),
	)

	// Send first message to backend
	// Note: gateway_id is not needed since sessionID is globally unique
	// Backend can identify Gateway through connection source if needed
	if err := protocol.WriteServerMessage(backendConn.Conn(), int32(clientHeader.MessageID), sessionID, messageData); err != nil {
		logger.L.Error("failed to send initial message to backend",
			zap.Int64("session_id", sessionID),
			zap.String("backend_addr", backendAddr),
			zap.Error(err),
		)
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
	defer func() {
		g.sessionManager.Remove(sessionID)
		metrics.ActiveSessions.Dec()
		logger.L.Info("session closed",
			zap.Int64("session_id", sessionID),
			zap.String("remote_addr", remoteAddr),
			zap.Duration("duration", time.Since(startTime)),
		)
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

// forwardConnection forwards data bidirectionally between client and backend
// Optimized: uses bufio for buffered I/O to reduce system calls
func (g *Gateway) forwardConnection(_ context.Context, sess *session.Session) {
	// Create buffered readers/writers for better I/O performance
	clientReader := protocol.NewSniffConn(sess.ClientConn) // Already has buffering
	backendWriter := sess.BackendConn

	// Forward client -> backend
	go func() {
		defer sess.BackendConn.Close()
		for {
			// Read complete client packet
			clientHeader, messageData, err := protocol.ReadFullPacket(clientReader)
			if err != nil {
				if err != io.EOF {
					logger.L.Debug("client read error",
						zap.Int64("session_id", sess.SessionID),
						zap.Error(err),
					)
				}
				return
			}

			// Update last active time
			sess.LastActiveAt = time.Now()

			// Write to backend in server message format
			if err := protocol.WriteServerMessage(backendWriter, int32(clientHeader.MessageID), sess.SessionID, messageData); err != nil {
				logger.L.Debug("backend write error",
					zap.Int64("session_id", sess.SessionID),
					zap.Error(err),
				)
				return
			}

			metrics.MessagesProcessed.WithLabelValues("client_to_backend", sess.ServiceType).Inc()
		}
	}()

	// Forward backend -> client
	// Backend sends server message format, we need to extract and forward to client
	defer sess.ClientConn.Close()
	for {
		// Read server message header (16 bytes) - optimized: single ReadFull call
		serverHeader, err := protocol.ReadServerMessageHeader(sess.BackendConn)
		if err != nil {
			if err != io.EOF {
				logger.L.Debug("backend read error",
					zap.Int64("session_id", sess.SessionID),
					zap.Error(err),
				)
			}
			return
		}

		// Read Gate message header (9 bytes) - optimized: single ReadFull call
		gateHeader, err := protocol.ReadGateMsgHeader(sess.BackendConn)
		if err != nil {
			if err != io.EOF {
				logger.L.Debug("gate header read error",
					zap.Int64("session_id", sess.SessionID),
					zap.Error(err),
				)
			}
			return
		}
		_ = gateHeader.OpCode // OpCode (currently not used, but read for protocol correctness)

		// Calculate message data length: totalLength - GateMsgHeaderSize
		messageDataLen := int(serverHeader.Length) - protocol.GateMsgHeaderSize
		if messageDataLen < 0 {
			logger.L.Warn("invalid message length",
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
					logger.L.Debug("message data read error",
						zap.Int64("session_id", sess.SessionID),
						zap.Int("data_len", messageDataLen),
						zap.Error(err),
					)
				}
				return
			}
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
			logger.L.Debug("client write error (length)",
				zap.Int64("session_id", sess.SessionID),
				zap.Error(err),
			)
			return
		}
		if err := binary.Write(sess.ClientConn, binary.LittleEndian, clientHeader.MessageID); err != nil {
			if pooledBuf != nil {
				buffer.Put(pooledBuf)
			}
			logger.L.Debug("client write error (message_id)",
				zap.Int64("session_id", sess.SessionID),
				zap.Error(err),
			)
			return
		}
		if err := binary.Write(sess.ClientConn, binary.LittleEndian, clientHeader.ServerID); err != nil {
			if pooledBuf != nil {
				buffer.Put(pooledBuf)
			}
			logger.L.Debug("client write error (server_id)",
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
				logger.L.Debug("client write error (data)",
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
