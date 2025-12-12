package gateway

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	gateway "github.com/SkynetNext/game-gateway/api"
	"github.com/SkynetNext/game-gateway/internal/buffer"
	"github.com/SkynetNext/game-gateway/internal/circuitbreaker"
	"github.com/SkynetNext/game-gateway/internal/config"
	consul "github.com/SkynetNext/game-gateway/internal/consul"
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
	grpcmgr "github.com/SkynetNext/game-gateway/internal/transport/grpc"
	"github.com/panjf2000/ants/v2"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// Gateway represents the game gateway service
type Gateway struct {
	config      *config.Config
	podName     string
	gatewayName string

	// Components
	sessionManager  *session.Manager
	poolManager     *pool.Manager
	grpcManager     *grpcmgr.Manager
	router          *router.Router
	redisClient     *redis.Client
	consulDiscovery *consul.Discovery

	// Routing rule configurations from Redis (without endpoints)
	// Key: "serverType:worldID", Value: RoutingRule config (endpoints will be filled from Consul)
	routingConfigs   map[string]*router.RoutingRule
	routingConfigsMu sync.RWMutex

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
	sessionSeq    uint64 // Session sequence counter (atomic)
	lastTimestamp int64  // Last timestamp used for ID generation (atomic)
	podHash       uint16 // Pod name hash (12 bits used, computed at startup)

	// State
	draining int32 // Atomic: 0=Running, 1=Draining
	wg       sync.WaitGroup

	// Goroutine pool for connection handling
	goroutinePool *ants.Pool

	// Connection queue: buffer connections when pool is busy
	// This prevents blocking acceptLoop and allows graceful degradation
	connQueue chan net.Conn

	// Context for graceful shutdown
	ctx    context.Context
	cancel context.CancelFunc
}

// connectionStats holds connection-level statistics
// Shared between handleConnection and sub-handlers for unified logging
type connectionStats struct {
	sessionID   int64
	backendAddr string
	serverType  int
	worldID     int
	bytesIn     int64
	bytesOut    int64
	status      string
	errorMsg    string
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

	// Initialize goroutine pool for connection handling
	// Default: 10 * CPU cores (can be overridden in config)
	maxGoroutines := cfg.ConnectionPool.MaxGoroutines
	if maxGoroutines == 0 {
		maxGoroutines = 10 * runtime.NumCPU()
	}
	// Use non-blocking mode: when pool is full, return error immediately
	// We'll use a connection queue to buffer connections instead of blocking
	goroutinePool, err := ants.NewPool(
		maxGoroutines,
		ants.WithOptions(ants.Options{
			ExpiryDuration: cfg.ConnectionPool.GoroutineExpiry,
			Nonblocking:    true, // Non-blocking: return error when pool is full
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create goroutine pool: %w", err)
	}

	// Create connection queue: buffer connections when pool is busy
	// This prevents blocking acceptLoop and allows graceful degradation
	connQueueSize := cfg.ConnectionPool.ConnectionQueueSize
	connQueue := make(chan net.Conn, connQueueSize)

	logger.Info("goroutine pool initialized",
		zap.Int("max_goroutines", maxGoroutines),
		zap.Duration("expiry_duration", cfg.ConnectionPool.GoroutineExpiry),
		zap.Int("connection_queue_size", connQueueSize),
	)

	g := &Gateway{
		config:          cfg,
		podName:         podName,
		podHash:         podHash,
		sessionManager:  sessionMgr,
		poolManager:     poolMgr,
		router:          rtr,
		redisClient:     redisCli,
		rateLimiter:     rateLimiter,
		ipLimiter:       ipLimiter,
		circuitBreakers: make(map[string]*circuitbreaker.Breaker),
		goroutinePool:   goroutinePool,
		connQueue:       connQueue,
		routingConfigs:  make(map[string]*router.RoutingRule),
	}

	if cfg.Server.UseGrpc {
		gatewayName := cfg.Server.GatewayName
		if gatewayName == "" {
			gatewayName = os.Getenv("POD_NAME")
		}
		if gatewayName == "" {
			gatewayName = podName
		}

		g.gatewayName = gatewayName
		g.grpcManager = grpcmgr.NewManager(
			gatewayName,
			g.handleGrpcPacket,
			cfg.Grpc.HeartbeatInterval, // Used as keepalive time for gRPC keepalive
			cfg.Grpc.ReconnectInterval,
			cfg.Grpc.MaxReconnectAttempts,
		)
		logger.Info("gRPC transport enabled", zap.String("gateway_name", gatewayName))
	} else {
		g.gatewayName = podName
	}

	// Initialize Consul service discovery (if enabled)
	if cfg.Consul.Enabled {
		// Use NODE_IP environment variable to access node-local Consul client
		nodeIP := os.Getenv("NODE_IP")
		if nodeIP == "" {
			logger.Warn("Consul is enabled but NODE_IP environment variable is not set, skipping Consul discovery")
		} else {
			// Use node IP with Consul client port (8500)
			consulAddress := fmt.Sprintf("http://%s:8500", nodeIP)
			refreshInterval := cfg.Consul.RefreshInterval
			if refreshInterval == 0 {
				refreshInterval = 10 * time.Second // Default refresh interval
			}
			g.consulDiscovery = consul.NewDiscovery(consulAddress, refreshInterval)
			logger.Info("Consul service discovery initialized",
				zap.String("node_ip", nodeIP),
				zap.String("address", consulAddress),
				zap.String("service", cfg.Consul.ServiceName),
				zap.Duration("refresh_interval", refreshInterval))
		}
	}

	return g, nil
}

// Start starts the gateway service
func (g *Gateway) Start(ctx context.Context) error {
	// Create cancellable context for graceful shutdown
	g.ctx, g.cancel = context.WithCancel(ctx)

	// 1. Load initial routing rules and realm mapping from Redis
	if err := g.loadConfigFromRedis(g.ctx); err != nil {
		return fmt.Errorf("failed to load initial config: %w", err)
	}

	// 2. Start Redis refresh loop
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		g.redisClient.RefreshLoop(
			g.ctx,
			g.config.Routing.RefreshInterval,
			g.onRoutingRulesUpdate,
			g.onRealmMappingUpdate,
		)
	}()

	// 2.5. Start Consul service discovery (if enabled)
	if g.config.Consul.Enabled && g.consulDiscovery != nil {
		g.wg.Add(1)
		go func() {
			defer g.wg.Done()
			g.consulDiscovery.StartRefreshLoop(
				g.ctx,
				g.config.Consul.ServiceName,
				g.config.Consul.Namespace,
				g.onConsulServicesUpdate,
			)
		}()
		nodeIP := os.Getenv("NODE_IP")
		consulAddress := fmt.Sprintf("http://%s:8500", nodeIP)
		logger.Info("Consul service discovery enabled",
			zap.String("service", g.config.Consul.ServiceName),
			zap.String("namespace", g.config.Consul.Namespace),
			zap.String("node_ip", nodeIP),
			zap.String("address", consulAddress),
			zap.Duration("refresh_interval", g.config.Consul.RefreshInterval))
	}

	// 3. Start connection pool cleanup
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		g.poolManager.StartCleanup(g.ctx, 1*time.Minute)
	}()

	// 4. Start session cleanup
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-g.ctx.Done():
				return
			case <-ticker.C:
				g.sessionManager.CleanupIdle(30 * time.Minute)
			}
		}
	}()

	// 5. Start connection queue processor
	// This goroutine processes connections from the queue and submits them to the pool
	// This prevents blocking acceptLoop when pool is busy
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		g.processConnectionQueue(g.ctx)
	}()

	// 6. Initialize access logger (simplified - no batching needed, Zap is already fast)
	middleware.InitAccessLogger(true)

	// 7. Start metrics and health check server
	if err := g.startMetricsServer(g.ctx); err != nil {
		return fmt.Errorf("failed to start metrics server: %w", err)
	}

	// 8. Start business listener
	if err := g.startListener(g.ctx); err != nil {
		return fmt.Errorf("failed to start listener: %w", err)
	}

	return nil
}

// Shutdown gracefully shuts down the gateway
func (g *Gateway) Shutdown(ctx context.Context) error {
	// 1. Enter drain mode
	atomic.StoreInt32(&g.draining, 1)

	// 2. Cancel context to stop all goroutines (including acceptLoop)
	if g.cancel != nil {
		g.cancel()
	}

	// 3. Stop accepting new connections
	if g.listener != nil {
		g.listener.Close()
	}

	// 4. Wait for active connections to close (with timeout)
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

	// 5. Close all gRPC connections (if using gRPC transport)
	if g.grpcManager != nil {
		if err := g.grpcManager.Close(); err != nil {
			return fmt.Errorf("failed to close gRPC connections: %w", err)
		}
	}

	// 6. Close connection queue (this will stop queue processor)
	if g.connQueue != nil {
		close(g.connQueue)
	}

	// 7. Release goroutine pool
	if g.goroutinePool != nil {
		g.goroutinePool.Release()
	}

	// 8. Close all connection pools
	if err := g.poolManager.Close(); err != nil {
		return fmt.Errorf("failed to close connection pools: %w", err)
	}

	// 9. Close Redis connection
	if err := g.redisClient.Close(); err != nil {
		return fmt.Errorf("failed to close Redis connection: %w", err)
	}

	// 10. Shutdown metrics server
	if g.metricsServer != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := g.metricsServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("failed to shutdown metrics server: %w", err)
		}
	}

	// 9. Shutdown access logger
	middleware.ShutdownAccessLogger()

	return nil
}

// loadConfigFromRedis loads initial configuration from Redis
func (g *Gateway) loadConfigFromRedis(ctx context.Context) error {
	// Load routing rules (configurations without endpoints)
	rules, err := g.redisClient.LoadRoutingRules(ctx)
	if err != nil {
		return fmt.Errorf("failed to load routing rules: %w", err)
	}

	// Store routing configurations (endpoints will be filled from Consul if enabled)
	g.routingConfigsMu.Lock()
	for _, rule := range rules {
		configKey := fmt.Sprintf("%d:%d", rule.ServerType, rule.WorldID)
		config := &router.RoutingRule{
			ServerType:  rule.ServerType,
			ServiceName: rule.ServiceName,
			Strategy:    rule.Strategy,
			HashKey:     rule.HashKey,
			WorldID:     rule.WorldID,
			Endpoints:   nil, // Will be filled from Consul
		}
		g.routingConfigs[configKey] = config
	}
	g.routingConfigsMu.Unlock()

	// If Consul is disabled, use endpoints from Redis (backward compatibility)
	if !g.config.Consul.Enabled {
		for _, rule := range rules {
			g.router.UpdateRule(rule)
		}
	}
	// If Consul is enabled, endpoints will be filled in onConsulServicesUpdate

	// Realm mapping will be used when routing game requests
	// It's loaded but not directly used here (used in routing logic)

	return nil
}

// onRoutingRulesUpdate handles routing rules updates from Redis
// Redis stores routing configuration (serverType, serviceName, strategy, etc.) but NOT endpoints
// Endpoints will be filled from Consul based on serviceName
func (g *Gateway) onRoutingRulesUpdate(rules map[int]*router.RoutingRule) {
	if rules == nil {
		metrics.ConfigRefreshErrors.WithLabelValues("routing_rules").Inc()
		logger.Warn("received nil routing rules, skipping update")
		return
	}

	// Record successful config refresh
	defer metrics.ConfigRefreshSuccess.WithLabelValues("routing_rules").Inc()

	// Update routing configurations (without endpoints)
	g.routingConfigsMu.Lock()
	for _, rule := range rules {
		// Create a copy without endpoints
		configKey := fmt.Sprintf("%d:%d", rule.ServerType, rule.WorldID)
		config := &router.RoutingRule{
			ServerType:  rule.ServerType,
			ServiceName: rule.ServiceName,
			Strategy:    rule.Strategy,
			HashKey:     rule.HashKey,
			WorldID:     rule.WorldID,
			Endpoints:   nil, // Endpoints will be filled from Consul
		}
		g.routingConfigs[configKey] = config
	}
	g.routingConfigsMu.Unlock()

	// If Consul is enabled, trigger service discovery to fill endpoints
	if g.config.Consul.Enabled && g.consulDiscovery != nil {
		// Trigger Consul refresh to update endpoints
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		services, err := g.consulDiscovery.DiscoverServices(ctx, g.config.Consul.ServiceName, g.config.Consul.Namespace)
		if err != nil {
			logger.Error("failed to refresh Consul services after Redis update",
				zap.Error(err))
		} else {
			g.mergeConsulEndpoints(services)
		}
	} else {
		// If Consul is disabled, use endpoints from Redis (backward compatibility)
		for _, rule := range rules {
			g.router.UpdateRule(rule)
		}
	}
}

// onConsulServicesUpdate handles Consul service discovery updates
// Merges Consul endpoints with Redis routing configurations
func (g *Gateway) onConsulServicesUpdate(services map[int][]consul.ServiceEntry) {
	if services == nil {
		logger.Warn("received nil Consul services, skipping update")
		return
	}

	g.mergeConsulEndpoints(services)
}

// mergeConsulEndpoints merges Consul service endpoints with Redis routing configurations
func (g *Gateway) mergeConsulEndpoints(services map[int][]consul.ServiceEntry) {
	g.routingConfigsMu.RLock()
	defer g.routingConfigsMu.RUnlock()

	// Update routing rules for each world_id found in Consul
	for worldID, entries := range services {
		if len(entries) == 0 {
			continue
		}

		// Build endpoints list from Consul service entries
		endpoints := make([]string, 0, len(entries))
		for _, entry := range entries {
			endpoint := fmt.Sprintf("%s:%d", entry.Address, entry.Port)
			endpoints = append(endpoints, endpoint)
		}

		// Find matching routing configuration from Redis
		// Try to find config for each serverType that matches this worldID
		// We need to match by serviceName from Consul (which is "gameserver")
		matched := false
		for _, config := range g.routingConfigs {
			// Check if this config matches the worldID and serviceName
			if config.WorldID == worldID || (config.WorldID == 0 && worldID > 0) {
				// Check if serviceName matches (if specified in config)
				if config.ServiceName == "" || config.ServiceName == g.config.Consul.ServiceName {
					// Merge: use config from Redis, but endpoints from Consul
					rule := &router.RoutingRule{
						ServerType:  config.ServerType,
						ServiceName: config.ServiceName,
						Strategy:    config.Strategy,
						HashKey:     config.HashKey,
						WorldID:     worldID,
						Endpoints:   endpoints, // From Consul
					}

					g.router.UpdateRule(rule)
					matched = true
					logger.Info("merged routing rule: Redis config + Consul endpoints",
						zap.Int("server_type", config.ServerType),
						zap.Int("world_id", worldID),
						zap.String("service_name", config.ServiceName),
						zap.String("strategy", string(config.Strategy)),
						zap.Int("endpoints_count", len(endpoints)),
						zap.Strings("endpoints", endpoints))
				}
			}
		}

		// If no matching config found, log warning but still create a default rule
		if !matched {
			logger.Warn("no Redis routing config found for Consul services, using defaults",
				zap.Int("world_id", worldID),
				zap.String("service_name", g.config.Consul.ServiceName))

			// Use default configuration (GameServer serverType is 200)
			const gameServerType = 200
			rule := &router.RoutingRule{
				ServerType:  gameServerType,
				ServiceName: g.config.Consul.ServiceName,
				Strategy:    router.StrategyRoundRobin,
				Endpoints:   endpoints,
				WorldID:     worldID,
			}
			g.router.UpdateRule(rule)
		}
	}
}

// onRealmMappingUpdate handles realm mapping updates from Redis
func (g *Gateway) onRealmMappingUpdate(mapping map[int32]string) {
	if mapping == nil {
		metrics.ConfigRefreshErrors.WithLabelValues("realm_mapping").Inc()
		logger.Warn("received nil realm mapping, skipping update")
		return
	}

	// Record successful config refresh
	defer metrics.ConfigRefreshSuccess.WithLabelValues("realm_mapping").Inc()

	logger.Debug("realm mapping updated", zap.Int("count", len(mapping)))
}

// startMetricsServer starts the metrics and health check HTTP server
func (g *Gateway) startMetricsServer(ctx context.Context) error {
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
			logger.Error("metrics server error", zap.Error(err))
		}
	}()

	// Start resource metrics updater
	g.wg.Add(1)
	go g.resourceMetricsUpdater(ctx)

	logger.Info("metrics server started", zap.Int("port", g.config.Server.HealthCheckPort))

	return nil
}

// resourceMetricsUpdater periodically updates resource utilization metrics
func (g *Gateway) resourceMetricsUpdater(ctx context.Context) {
	defer g.wg.Done()

	ticker := time.NewTicker(15 * time.Second) // Update every 15 seconds
	defer ticker.Stop()

	// Update immediately on start
	metrics.UpdateResourceMetrics()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			metrics.UpdateResourceMetrics()
		}
	}
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

	logger.Info("gateway listener started", zap.String("address", g.config.Server.ListenAddr))

	return nil
}

// processConnectionQueue processes connections from the queue
// This allows acceptLoop to continue accepting connections even when pool is busy
func (g *Gateway) processConnectionQueue(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			// Shutdown: drain remaining connections in queue
			for {
				select {
				case conn := <-g.connQueue:
					conn.Close()
					g.wg.Done() // Release waitgroup counter
				default:
					// Queue is empty, exit
					return
				}
			}
		case conn := <-g.connQueue:
			// Try to submit connection to pool
			err := g.goroutinePool.Submit(func() {
				defer g.wg.Done() // Release waitgroup counter
				g.handleConnection(ctx, conn)
			})
			if err != nil {
				// Pool is closed or still full, close connection
				conn.Close()
				g.wg.Done() // Release waitgroup counter
				logger.Warn("failed to submit queued connection to pool, connection closed",
					zap.Error(err),
					zap.String("remote_addr", conn.RemoteAddr().String()),
				)
				metrics.IncConnectionRejected("pool_closed_or_full")
			}
		}
	}
}

// acceptLoop accepts incoming connections
// Simplified implementation following unified-access-gateway pattern
// Removed SetDeadline on listener to avoid Accept delays
func (g *Gateway) acceptLoop(ctx context.Context) {
	for {
		// Check context cancellation before Accept (non-blocking)
		select {
		case <-ctx.Done():
			return
		default:
		}

		conn, err := g.listener.Accept()
		if err != nil {
			// Check if listener was closed (normal shutdown)
			if atomic.LoadInt32(&g.draining) == 1 {
				return
			}
			// Check for temporary errors (network issues, can retry)
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Temporary() {
				logger.Debug("temporary accept error", zap.Error(err))
				continue
			}
			// Check for timeout errors (should not happen without SetDeadline, but handle gracefully)
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			// Log other errors
			logger.Warn("accept connection error", zap.Error(err))
			continue
		}

		// Set TCP_NODELAY for all connections (TCP and WebSocket)
		// This ensures immediate send for small packets (handshakes, game protocol)
		// Critical for low-latency requirements
		if tcpConn, ok := conn.(*net.TCPConn); ok {
			_ = tcpConn.SetNoDelay(true)
		}

		// Set connection timeouts
		if err := conn.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
			conn.Close()
			logger.Debug("failed to set initial read deadline", zap.Error(err))
			continue
		}

		// Try to submit connection to pool immediately (non-blocking)
		// If pool is busy, put connection in queue for later processing
		g.wg.Add(1)
		err = g.goroutinePool.Submit(func() {
			defer g.wg.Done()
			g.handleConnection(ctx, conn)
		})
		if err != nil {
			// Pool is full, try to put connection in queue
			select {
			case g.connQueue <- conn:
				// Connection queued successfully, will be processed by queue processor
				// Note: wg.Done() will be called in processConnectionQueue
			default:
				// Queue is also full, reject connection
				g.wg.Done()
				conn.Close()
				logger.Warn("connection rejected: pool and queue both full",
					zap.String("remote_addr", conn.RemoteAddr().String()),
					zap.Int("queue_size", len(g.connQueue)),
					zap.Int("queue_capacity", cap(g.connQueue)),
				)
				metrics.IncConnectionRejected("pool_and_queue_full")
			}
		}
	}
}

// handleConnection handles a client connection
// Following Envoy/Kong best practice: only 1 access log per connection
func (g *Gateway) handleConnection(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	remoteAddr := conn.RemoteAddr().String()
	startTime := time.Now()

	// Create span for distributed tracing
	ctx, span := tracing.StartSpan(ctx, "gateway.handle_connection")
	defer span.End()

	// Create context logger for this connection (caches trace context)
	log := logger.NewContextLogger(ctx)

	// Connection-level statistics (shared with sub-handlers)
	connStats := &connectionStats{
		sessionID:   0,
		backendAddr: "",
		serverType:  0,
		worldID:     0,
		bytesIn:     0,
		bytesOut:    0,
		status:      "closed", // Default status
		errorMsg:    "",
	}

	// Single access log per connection (at connection close)
	// Best practice from Envoy/Kong: log complete connection lifecycle
	defer func() {
		middleware.LogAccess(ctx, &middleware.AccessLogEntry{
			SessionID:   connStats.sessionID,
			ClientAddr:  remoteAddr,
			BackendAddr: connStats.backendAddr,
			ServerType:  connStats.serverType,
			WorldID:     connStats.worldID,
			DurationNs:  time.Since(startTime).Nanoseconds(),
			BytesIn:     connStats.bytesIn,
			BytesOut:    connStats.bytesOut,
			Status:      connStats.status,
			Error:       connStats.errorMsg,
		})
	}()

	// Extract IP address from remote address
	ip := extractIP(remoteAddr)

	// IP-based rate limiting: check if connection from this IP is allowed
	if !g.ipLimiter.Allow(ip) {
		metrics.IncConnectionRejected("ip_rate_limit")
		connStats.status = "rejected"
		connStats.errorMsg = "IP rate limit exceeded"
		return
	}
	defer g.ipLimiter.Release(ip)

	// Global rate limiting: check if connection is allowed
	if !g.rateLimiter.Allow() {
		metrics.IncConnectionRejected("global_rate_limit")
		connStats.status = "rejected"
		connStats.errorMsg = "connection rate limit exceeded"
		return
	}
	defer g.rateLimiter.Release()

	metrics.TotalConnections.Inc()
	metrics.ActiveConnections.Inc()
	defer metrics.ActiveConnections.Dec()

	// Create sniffed connection
	sniffConn := protocol.NewSniffConn(conn)

	// Sniff protocol
	protoType, _, err := sniffConn.Sniff()
	if err != nil {
		connStats.status = "error"
		connStats.errorMsg = "protocol sniff failed: " + err.Error()
		return
	}

	// Handle based on protocol type
	switch protoType {
	case protocol.ProtocolWebSocket:
		g.handleWebSocketConnection(ctx, sniffConn, nil, log, connStats)
	case protocol.ProtocolHTTP:
		g.handleHTTPConnection(ctx, sniffConn, log, connStats)
	case protocol.ProtocolTCP:
		g.handleTCPConnection(ctx, sniffConn, log, connStats)
	default:
		connStats.status = "rejected"
		connStats.errorMsg = "unknown protocol"
		return
	}
}

func (g *Gateway) handleHTTPConnection(ctx context.Context, conn *protocol.SniffConn, log *logger.ContextLogger, connStats *connectionStats) {

	req, err := http.ReadRequest(conn.Reader())
	if err != nil {
		_ = writeHTTPResponse(conn.Conn, http.StatusBadRequest, "text/plain; charset=utf-8", []byte("Malformed HTTP request\n"), nil)
		connStats.status = "rejected"
		connStats.errorMsg = "malformed http request"
		return
	}
	defer req.Body.Close()

	if isWebSocketUpgrade(req) {
		g.handleWebSocketConnection(ctx, conn, req, log, connStats)
		return
	}

	body := []byte("Unsupported protocol. Gateway expects WebSocket or TCP payloads.\n")
	_ = writeHTTPResponse(conn.Conn, http.StatusUpgradeRequired, "text/plain; charset=utf-8", body, map[string]string{
		"Connection": "close",
	})
	connStats.status = "rejected"
	connStats.errorMsg = "unsupported protocol (http)"
}

func (g *Gateway) handleWebSocketConnection(ctx context.Context, conn *protocol.SniffConn, req *http.Request, log *logger.ContextLogger, connStats *connectionStats) {
	remoteAddr := conn.RemoteAddr().String()

	var err error
	if req == nil {
		req, err = http.ReadRequest(conn.Reader())
		if err != nil {
			_ = writeHTTPResponse(conn.Conn, http.StatusBadRequest, "text/plain; charset=utf-8", []byte("Malformed WebSocket handshake\n"), nil)
			connStats.status = "rejected"
			connStats.errorMsg = "malformed websocket handshake"
			return
		}
	}
	defer req.Body.Close()

	if !isWebSocketRequest(req) {
		_ = writeHTTPResponse(conn.Conn, http.StatusBadRequest, "text/plain; charset=utf-8", []byte("Invalid WebSocket upgrade request\n"), nil)
		connStats.status = "rejected"
		connStats.errorMsg = "invalid websocket upgrade"
		return
	}

	acceptKey, err := computeWebSocketAcceptKey(req.Header.Get("Sec-WebSocket-Key"))
	if err != nil {
		_ = writeHTTPResponse(conn.Conn, http.StatusBadRequest, "text/plain; charset=utf-8", []byte("Invalid WebSocket key\n"), nil)
		connStats.status = "rejected"
		connStats.errorMsg = "invalid websocket key"
		return
	}

	subprotocol := selectWebSocketSubprotocol(req.Header.Values("Sec-WebSocket-Protocol"))
	if err := writeWebSocketHandshake(conn.Conn, acceptKey, subprotocol); err != nil {
		log.Warn("failed to write WebSocket handshake response",
			zap.String("remote_addr", remoteAddr),
			zap.Error(err),
		)
		connStats.status = "error"
		connStats.errorMsg = "handshake write failed"
		return
	}

	// Clear read deadline after handshake - WebSocket connections should wait for frames without timeout
	_ = conn.Conn.SetReadDeadline(time.Time{})

	g.configMu.RLock()
	maxMessageSize := g.config.Security.MaxMessageSize
	g.configMu.RUnlock()

	wsConn := protocol.NewWebSocketConn(conn.Conn, conn.Reader(), maxMessageSize)

	// Bridge to TCP handler
	g.handleTCPConnection(ctx, wsConn, log, connStats)
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
func (g *Gateway) handleTCPConnection(ctx context.Context, conn net.Conn, log *logger.ContextLogger, connStats *connectionStats) {
	startTime := time.Now()
	remoteAddr := conn.RemoteAddr().String()

	// Create span for TCP connection handling
	ctx, span := tracing.StartSpan(ctx, "gateway.handle_tcp_connection")
	defer span.End()

	// Update logger with new span context
	log = logger.NewContextLogger(ctx)

	// Read first packet to get routing information
	g.configMu.RLock()
	maxMessageSize := g.config.Security.MaxMessageSize
	g.configMu.RUnlock()

	clientHeader, messageData, err := protocol.ReadFullPacket(conn, maxMessageSize)
	if err != nil {
		switch err {
		case protocol.ErrMessageTooLarge:
			metrics.RoutingErrors.WithLabelValues("message_too_large").Inc()
			connStats.status = "rejected"
			connStats.errorMsg = "message too large"
		case io.EOF:
			// Connection closed before first packet received
			// This is normal for idle connections or clients that close immediately
			connStats.status = "closed"
			connStats.errorMsg = "connection closed before first packet"
		default:
			connStats.status = "error"
			connStats.errorMsg = err.Error()
		}
		return
	}

	// Track if we used pooled buffer (for cleanup)
	var pooledBuf []byte
	if messageData != nil && cap(messageData) >= 8192 {
		pooledBuf = messageData[:cap(messageData)]
	}
	defer func() {
		if pooledBuf != nil {
			buffer.Put(pooledBuf)
		}
	}()

	// Extract routing information from ServerID
	_, worldID, serverType, instID := protocol.ExtractServerIDInfo(clientHeader.ServerID)

	// Generate session ID
	sessionID := g.generateSessionID()

	// Start routing span
	ctx, routeSpan := tracing.StartSpan(ctx, "gateway.route_to_backend")
	routeSpan.SetAttributes(
		attribute.Int("server.type", int(serverType)),
		attribute.Int("world.id", int(worldID)),
		attribute.Int("instance.id", int(instID)),
		attribute.Int64("session.id", sessionID),
	)

	// Route to backend service
	backendAddr, err := g.routeToBackend(ctx, int(serverType), int(worldID), int(instID))
	if err != nil {
		routeSpan.RecordError(err)
		routeSpan.SetStatus(codes.Error, "routing failed")
		routeSpan.End()
		metrics.RoutingErrors.WithLabelValues("not_found").Inc()
		connStats.status = "error"
		connStats.errorMsg = fmt.Sprintf("routing failed: %v", err)
		connStats.serverType = int(serverType)
		connStats.worldID = int(worldID)
		return
	}

	routeSpan.SetAttributes(attribute.String("backend.address", backendAddr))

	// Record backend request attempt
	backendRequestStart := time.Now()

	// Check circuit breaker
	breaker := g.getOrCreateBreaker(backendAddr)
	if !breaker.Allow() {
		routeSpan.RecordError(fmt.Errorf("circuit breaker open"))
		routeSpan.SetStatus(codes.Error, "circuit breaker open")
		routeSpan.End()
		metrics.RoutingErrors.WithLabelValues("circuit_breaker_open").Inc()
		metrics.BackendRequestsTotal.WithLabelValues(backendAddr, "error").Inc()
		metrics.BackendErrorsTotal.WithLabelValues(backendAddr, "circuit_breaker_open").Inc()
		connStats.status = "error"
		connStats.errorMsg = "circuit breaker open"
		connStats.backendAddr = backendAddr
		connStats.serverType = int(serverType)
		connStats.worldID = int(worldID)
		return
	}

	// Get connection from pool with retry (only for TCP mode)
	var backendConn *pool.Connection
	var connectionStartTime time.Time
	retryCount := 0

	if !g.config.Server.UseGrpc {
		retryCfg := retry.RetryConfig{
			MaxRetries: g.config.ConnectionPool.MaxRetries,
			RetryDelay: g.config.ConnectionPool.RetryDelay,
		}
		err = retry.Do(ctx, retryCfg, func() error {
			if retryCount > 0 {
				metrics.BackendRetriesTotal.WithLabelValues(backendAddr).Inc()
			}
			retryCount++

			connectionStartTime = time.Now()
			var err error
			backendConn, err = g.poolManager.GetConnection(ctx, backendAddr)
			if err == nil {
				// Record connection latency
				connectionLatency := time.Since(connectionStartTime)
				metrics.BackendConnectionLatency.WithLabelValues(backendAddr).Observe(connectionLatency.Seconds())
				// Increment active connections counter
				metrics.BackendConnectionsActive.WithLabelValues(backendAddr).Inc()
				breaker.RecordSuccess()
			} else {
				breaker.RecordFailure()
				// Record connection error
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					metrics.BackendTimeoutsTotal.WithLabelValues(backendAddr).Inc()
					metrics.BackendErrorsTotal.WithLabelValues(backendAddr, "timeout").Inc()
				} else {
					metrics.BackendErrorsTotal.WithLabelValues(backendAddr, "connection_failed").Inc()
				}
			}
			return err
		})
		if err != nil {
			routeSpan.RecordError(err)
			routeSpan.SetStatus(codes.Error, "connection failed")
			routeSpan.End()
			metrics.RoutingErrors.WithLabelValues("connection_failed").Inc()
			metrics.BackendRequestsTotal.WithLabelValues(backendAddr, "error").Inc()
			connStats.status = "error"
			connStats.errorMsg = fmt.Sprintf("backend connection failed: %v", err)
			connStats.sessionID = sessionID
			connStats.backendAddr = backendAddr
			connStats.serverType = int(serverType)
			connStats.worldID = int(worldID)
			return
		}
	}

	// Routing successful
	routeSpan.SetStatus(codes.Ok, "routed")
	routeSpan.End()

	// Record successful backend request
	metrics.BackendRequestsTotal.WithLabelValues(backendAddr, "success").Inc()

	// Notify backend server that client has connected (best practice)
	clientIP := extractIP(remoteAddr)
	if err := g.notifyBackendClientConnect(ctx, backendAddr, sessionID, clientIP, "tcp"); err != nil {
		// Connection notification failed, reject this connection
		metrics.BackendErrorsTotal.WithLabelValues(backendAddr, "notification_failed").Inc()
		connStats.status = "error"
		connStats.errorMsg = fmt.Sprintf("notify connect failed: %v", err)
		connStats.sessionID = sessionID
		connStats.backendAddr = backendAddr
		connStats.serverType = int(serverType)
		connStats.worldID = int(worldID)
		return
	}

	// Ensure connection is returned to pool even on panic
	// Also notify backend of disconnection when connection ends
	defer func() {
		// Notify backend server that client has disconnected
		disconnectReason := "normal"
		if connStats.status == "error" {
			disconnectReason = connStats.errorMsg
		}
		g.notifyBackendClientDisconnect(context.Background(), backendAddr, sessionID, disconnectReason)

		if r := recover(); r != nil {
			log.Error("panic in handleTCPConnection",
				zap.Any("panic", r),
				zap.String("remote_addr", remoteAddr),
			)
			panic(r)
		}
		if backendConn != nil {
			g.poolManager.PutConnection(backendAddr, backendConn)
			// Decrement active connections counter
			metrics.BackendConnectionsActive.WithLabelValues(backendAddr).Dec()
		}
	}()

	// Record request latency (frontend)
	latency := time.Since(startTime)
	metrics.RequestLatency.WithLabelValues(fmt.Sprintf("%d", serverType)).Observe(latency.Seconds())

	// Record backend request latency (end-to-end from routing start)
	backendLatency := time.Since(backendRequestStart)
	metrics.BackendRequestLatency.WithLabelValues(backendAddr).Observe(backendLatency.Seconds())

	// Extract Trace ID and Span ID for propagation
	var traceContext map[string]string
	g.configMu.RLock()
	enableTracePropagation := g.config.Tracing.EnableTracePropagation
	g.configMu.RUnlock()

	if enableTracePropagation {
		traceContext = extractTraceContext(ctx)
	}

	// Send first message to backend
	if g.config.Server.UseGrpc {
		packet := &gateway.GamePacket{
			SessionId: sessionID,
			MsgId:     int32(clientHeader.MessageID),
			Payload:   messageData,
			Metadata:  traceContext, // Propagate trace context in gRPC mode
		}
		if err := g.grpcManager.Send(ctx, backendAddr, packet); err != nil {
			connStats.status = "error"
			connStats.errorMsg = fmt.Sprintf("grpc send failed: %v", err)
			connStats.sessionID = sessionID
			connStats.backendAddr = backendAddr
			connStats.serverType = int(serverType)
			connStats.worldID = int(worldID)
			return
		}
	} else {
		if err := protocol.WriteServerMessage(backendConn.Conn(), int32(clientHeader.MessageID), sessionID, messageData, traceContext); err != nil {
			connStats.status = "error"
			connStats.errorMsg = fmt.Sprintf("write message failed: %v", err)
			connStats.sessionID = sessionID
			connStats.backendAddr = backendAddr
			connStats.serverType = int(serverType)
			connStats.worldID = int(worldID)
			return
		}
	}

	metrics.MessagesProcessed.WithLabelValues("client_to_backend", fmt.Sprintf("%d", serverType)).Inc()

	// Create session
	sess := &session.Session{
		SessionID:    sessionID,
		ClientConn:   conn,
		ServiceType:  fmt.Sprintf("%d", serverType),
		WorldID:      int32(worldID),
		InstID:       int32(instID),
		CreatedAt:    time.Now(),
		LastActiveAt: time.Now(),
		State:        session.SessionStateConnected,
		BytesOut:     &connStats.bytesOut, // For gRPC mode: allow sendToSession to update bytesOut
	}
	if backendConn != nil {
		sess.BackendConn = backendConn.Conn()
	}

	g.sessionManager.Add(sess)
	metrics.ActiveSessions.Inc()

	// Update connection statistics for unified access log
	connStats.sessionID = sessionID
	connStats.backendAddr = backendAddr
	connStats.serverType = int(serverType)
	connStats.worldID = int(worldID)
	connStats.status = "success"

	defer func() {
		g.sessionManager.Remove(sessionID)
		metrics.ActiveSessions.Dec()
	}()

	// Forward subsequent data bidirectionally
	if g.config.Server.UseGrpc {
		sniffConn, ok := conn.(*protocol.SniffConn)
		if !ok {
			sniffConn = protocol.NewSniffConn(conn)
		}
		g.forwardGrpcConnection(ctx, sessionID, sniffConn, backendAddr, log, &connStats.bytesIn, &connStats.bytesOut)
	} else {
		g.forwardConnection(ctx, sess, log, &connStats.bytesIn, &connStats.bytesOut)
	}
}

// routeToBackend routes request to backend service using serverType + worldID + instID
func (g *Gateway) routeToBackend(ctx context.Context, serverType, worldID, instID int) (string, error) {
	return g.router.RouteByServerID(ctx, serverType, worldID, instID)
}

// extractIP extracts IP address from "host:port" format
func extractIP(addr string) string {
	if idx := strings.LastIndex(addr, ":"); idx != -1 {
		return addr[:idx]
	}
	return addr
}

// extractTraceContext extracts Trace ID and Span ID from context for propagation
func extractTraceContext(ctx context.Context) map[string]string {
	span := trace.SpanFromContext(ctx)
	if !span.SpanContext().IsValid() {
		return nil
	}

	traceID := span.SpanContext().TraceID()
	spanID := span.SpanContext().SpanID()

	if traceID.IsValid() {
		traceContext := make(map[string]string, 2)
		traceContext["trace_id"] = traceID.String()
		if spanID.IsValid() {
			traceContext["span_id"] = spanID.String()
		}
		return traceContext
	}

	return nil
}

// forwardConnection forwards data bidirectionally between client and backend
func (g *Gateway) forwardConnection(ctx context.Context, sess *session.Session, log *logger.ContextLogger, bytesIn, bytesOut *int64) {
	// Get backend address from connection
	backendAddr := sess.BackendConn.RemoteAddr().String()

	// Create span for connection forwarding
	ctx, span := tracing.StartSpan(ctx, "gateway.forward_connection")
	defer func() {
		// Record final bytes transferred
		span.SetAttributes(
			attribute.Int64("bytes.in", atomic.LoadInt64(bytesIn)),
			attribute.Int64("bytes.out", atomic.LoadInt64(bytesOut)),
		)
		span.End()
	}()

	span.SetAttributes(
		attribute.Int64("session.id", sess.SessionID),
		attribute.String("client.address", sess.ClientConn.RemoteAddr().String()),
		attribute.String("backend.address", backendAddr),
	)

	// Get timeouts from config (thread-safe read)
	g.configMu.RLock()
	readTimeout := g.config.ConnectionPool.ReadTimeout
	writeTimeout := g.config.ConnectionPool.WriteTimeout
	maxMessageSize := g.config.Security.MaxMessageSize
	g.configMu.RUnlock()

	// Create buffered readers/writers for better I/O performance
	clientReader := protocol.NewSniffConn(sess.ClientConn)
	backendWriter := sess.BackendConn

	// Use context with timeout for connection lifecycle
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Forward client -> backend
	go func() {
		defer cancel()
		defer func() {
			if sess.BackendConn != nil {
				sess.BackendConn.Close()
			}
		}()

		ticker := time.NewTicker(readTimeout / 2)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := sess.ClientConn.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
					return
				}
			default:
				clientHeader, messageData, err := protocol.ReadFullPacket(clientReader, maxMessageSize)
				if err != nil {
					if err == protocol.ErrMessageTooLarge {
						log.Warn("message too large, closing connection",
							zap.Int64("session_id", sess.SessionID),
						)
						metrics.RoutingErrors.WithLabelValues("message_too_large").Inc()
					}
					// No separate log for EOF - it's normal connection close
					return
				}

				// Track bytes
				messageSize := int64(len(messageData))
				atomic.AddInt64(bytesIn, messageSize)
				// Record backend bytes transmitted (request direction)
				metrics.BackendBytesTransmitted.WithLabelValues(backendAddr, "request").Add(float64(messageSize))

				var pooledBuf []byte
				if messageData != nil && cap(messageData) >= 8192 {
					pooledBuf = messageData[:cap(messageData)]
				}

				sess.LastActiveAt = time.Now()

				if err := backendWriter.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
					if pooledBuf != nil {
						buffer.Put(pooledBuf)
					}
					return
				}

				var traceContext map[string]string
				g.configMu.RLock()
				enableTracePropagation := g.config.Tracing.EnableTracePropagation
				g.configMu.RUnlock()

				if enableTracePropagation {
					traceContext = extractTraceContext(ctx)
				}

				if err := protocol.WriteServerMessage(backendWriter, int32(clientHeader.MessageID), sess.SessionID, messageData, traceContext); err != nil {
					if pooledBuf != nil {
						buffer.Put(pooledBuf)
					}
					return
				}

				if pooledBuf != nil {
					buffer.Put(pooledBuf)
				}

				metrics.MessagesProcessed.WithLabelValues("client_to_backend", sess.ServiceType).Inc()
			}
		}
	}()

	// Forward backend -> client
	defer sess.ClientConn.Close()

	if err := sess.BackendConn.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
			if err := sess.BackendConn.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
				return
			}

			serverHeader, err := protocol.ReadServerMessageHeader(sess.BackendConn)
			if err != nil {
				return
			}

			gateHeader, gateErr := protocol.ReadGateMsgHeader(sess.BackendConn)
			if gateErr != nil {
				return
			}
			_ = gateHeader.OpCode

			expectedDataLen := int(serverHeader.Length) - protocol.GateMsgHeaderSize
			messageDataLen := expectedDataLen
			if messageDataLen < 0 {
				log.Warn("invalid message length",
					zap.Int64("session_id", sess.SessionID),
					zap.Int32("total_length", serverHeader.Length),
				)
				return
			}

			var messageData []byte
			var pooledBuf []byte
			if messageDataLen > 0 {
				if messageDataLen <= 8192 {
					pooledBuf = buffer.Get()
					messageData = pooledBuf[:messageDataLen]
				} else {
					messageData = make([]byte, messageDataLen)
				}
				if _, err := io.ReadFull(sess.BackendConn, messageData); err != nil {
					if pooledBuf != nil {
						buffer.Put(pooledBuf)
					}
					return
				}
			}

			// Track bytes
			responseSize := int64(messageDataLen)
			atomic.AddInt64(bytesOut, responseSize)
			// Record backend bytes transmitted (response direction)
			metrics.BackendBytesTransmitted.WithLabelValues(backendAddr, "response").Add(float64(responseSize))

			if err := sess.ClientConn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
				if pooledBuf != nil {
					buffer.Put(pooledBuf)
				}
				return
			}

			// Write ClientMessageHeader (8 bytes) before sending payload
			// Use actual payload length (len(messageData)) instead of calculated messageDataLen
			// to ensure accuracy regardless of how serverHeader.Length was calculated
			payloadLen := len(messageData)
			clientHeader := &protocol.ClientMessageHeader{
				Length:    uint16(payloadLen),
				MessageID: uint16(serverHeader.Type),
				ServerID:  0, // ServerID is 0 for server-to-client messages
			}
			if err := protocol.WriteClientMessageHeader(sess.ClientConn, clientHeader); err != nil {
				if pooledBuf != nil {
					buffer.Put(pooledBuf)
				}
				return
			}
			if len(messageData) > 0 {
				if _, err := sess.ClientConn.Write(messageData); err != nil {
					if pooledBuf != nil {
						buffer.Put(pooledBuf)
					}
					return
				}
			}

			if pooledBuf != nil {
				buffer.Put(pooledBuf)
			}

			sess.LastActiveAt = time.Now()
			metrics.MessagesProcessed.WithLabelValues("backend_to_client", sess.ServiceType).Inc()
		}
	}
}

// SessionIDComponents represents the parsed components of a session ID
type SessionIDComponents struct {
	PodHash   uint16    // 12-bit pod hash
	Timestamp int64     // 40-bit timestamp in milliseconds
	Sequence  uint16    // 12-bit sequence number
	CreatedAt time.Time // Parsed time
}

// ParseSessionID parses a session ID into its components
// Useful for debugging and tracing
func ParseSessionID(sessionID int64) SessionIDComponents {
	// Convert to uint64 to avoid sign extension issues
	u := uint64(sessionID)

	podHash := uint16((u >> 52) & 0xFFF)
	timestampMs := int64((u >> 12) & 0xFFFFFFFFFF)
	sequence := uint16(u & 0xFFF)

	return SessionIDComponents{
		PodHash:   podHash,
		Timestamp: timestampMs,
		Sequence:  sequence,
		CreatedAt: time.UnixMilli(timestampMs),
	}
}

// generateSessionID generates a unique session ID
// Improved Snowflake-like algorithm with millisecond precision
// Format: (podHash << 52) + (timestamp_ms << 12) + sequence
//
// Structure (64-bit):
// ┌──────────────┬──────────────────────────────────────┬──────────────┐
// │  Pod Hash    │      Timestamp (milliseconds)        │   Sequence   │
// │   12 bits    │            40 bits                   │   12 bits    │
// └──────────────┴──────────────────────────────────────┴──────────────┘
//
// - Pod Hash: 12-bit (4096 unique pods), derived from pod name CRC32
// - Timestamp: 40-bit milliseconds since epoch (can represent ~34 years)
// - Sequence: 12-bit counter (4096 IDs per millisecond per pod)
//
// Benefits:
// - Time-ordered: sessions can be sorted by creation time
// - Distributed: no coordination needed between pods
// - High throughput: 4M IDs/second per pod
// - Traceable: can identify which pod created the session
func (g *Gateway) generateSessionID() int64 {
	for {
		// Get current timestamp in milliseconds, use lower 40 bits
		// 40-bit can represent: 2^40 ms = 1,099,511,627,776 ms ≈ 34.8 years
		timestampMs := time.Now().UnixMilli() & 0xFFFFFFFFFF

		// Get last timestamp atomically
		lastTs := atomic.LoadInt64(&g.lastTimestamp)

		if timestampMs < lastTs {
			// Clock moved backwards, wait for next millisecond
			time.Sleep(time.Millisecond)
			continue
		}

		var seq uint64
		if timestampMs == lastTs {
			// Same millisecond: increment sequence
			seq = atomic.AddUint64(&g.sessionSeq, 1) & 0xFFF // 12-bit sequence (0-4095)

			// If sequence wrapped around (back to 0 after 4095), wait for next millisecond
			// Note: seq == 0 means we've exhausted all 4096 IDs in this millisecond
			if seq == 0 {
				// Sequence exhausted for this millisecond, wait for next
				time.Sleep(time.Millisecond)
				continue
			}
		} else {
			// New millisecond: reset sequence and update timestamp atomically
			// Use CAS to ensure only one goroutine updates the timestamp
			if atomic.CompareAndSwapInt64(&g.lastTimestamp, lastTs, timestampMs) {
				// Successfully updated timestamp, reset sequence
				atomic.StoreUint64(&g.sessionSeq, 1)
				seq = 1
			} else {
				// Another goroutine updated timestamp, retry
				continue
			}
		}

		// Use lower 12 bits of podHash to avoid collision
		podHash12 := int64(g.podHash & 0xFFF)

		// Combine: [12-bit pod hash][40-bit timestamp][12-bit sequence]
		sessionID := (podHash12 << 52) | (timestampMs << 12) | int64(seq)

		return sessionID
	}
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
