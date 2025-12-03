package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// Connection metrics
	ActiveConnections = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "game_gateway_connections_active",
		Help: "Number of active client connections",
	})

	TotalConnections = promauto.NewCounter(prometheus.CounterOpts{
		Name: "game_gateway_connections_total",
		Help: "Total number of client connections",
	})

	// Session metrics
	ActiveSessions = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "game_gateway_sessions_active",
		Help: "Number of active sessions",
	})

	// Routing metrics
	RoutingErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "game_gateway_routing_errors_total",
		Help: "Total number of routing errors",
	}, []string{"error_type"})

	// Rate limiting metrics
	RateLimitRejected = promauto.NewCounter(prometheus.CounterOpts{
		Name: "game_gateway_rate_limit_rejected_total",
		Help: "Total number of connections rejected by rate limiter",
	})

	// Connection rejection metrics
	ConnectionRejected = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "game_gateway_connection_rejected_total",
		Help: "Total number of connections rejected",
	}, []string{"reason"})

	// Circuit breaker metrics
	CircuitBreakerState = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "game_gateway_circuit_breaker_state",
		Help: "Circuit breaker state (0=closed, 1=open, 2=half-open)",
	}, []string{"backend"})

	// Backend connection pool metrics
	BackendPoolActive = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "game_gateway_backend_pool_active",
		Help: "Number of active backend connections in pool",
	}, []string{"backend"})

	BackendPoolIdle = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "game_gateway_backend_pool_idle",
		Help: "Number of idle backend connections in pool",
	}, []string{"backend"})

	// Request latency
	RequestLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "game_gateway_request_latency_seconds",
		Help:    "Request latency in seconds",
		Buckets: prometheus.ExponentialBuckets(0.001, 2, 10), // 1ms to 1s
	}, []string{"service_type"})

	// Message processing
	MessagesProcessed = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "game_gateway_messages_processed_total",
		Help: "Total number of messages processed",
	}, []string{"direction", "service_type"})

	// Configuration refresh metrics
	ConfigRefreshErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "game_gateway_config_refresh_errors_total",
		Help: "Total number of configuration refresh errors",
	}, []string{"config_type"})

	// Connection pool cleanup metrics
	PoolCleanupErrors = promauto.NewCounter(prometheus.CounterOpts{
		Name: "game_gateway_pool_cleanup_errors_total",
		Help: "Total number of connection pool cleanup errors",
	})
)

// IncConnectionRejected increments the connection rejected counter
func IncConnectionRejected(reason string) {
	ConnectionRejected.WithLabelValues(reason).Inc()
}
