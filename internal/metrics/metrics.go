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

	// Backend active connections (按后端分组)
	BackendConnectionsActive = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "game_gateway_backend_connections_active",
		Help: "Number of active connections to backend services",
	}, []string{"backend"})

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

	// Connection rejection metrics (includes rate limiting)
	ConnectionRejected = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "game_gateway_connection_rejected_total",
		Help: "Total number of connections rejected",
	}, []string{"reason"})

	// Circuit breaker metrics
	CircuitBreakerState = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "game_gateway_circuit_breaker_state",
		Help: "Circuit breaker state (0=closed, 1=open, 2=half-open)",
	}, []string{"backend"})

	// Backend/Upstream metrics
	BackendRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "game_gateway_backend_requests_total",
		Help: "Total number of requests to backend services",
	}, []string{"backend", "status"})

	BackendRequestLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "game_gateway_backend_request_latency_seconds",
		Help:    "Backend request latency in seconds (end-to-end)",
		Buckets: prometheus.ExponentialBuckets(0.001, 2, 12), // 1ms to 4s
	}, []string{"backend"})

	BackendConnectionLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "game_gateway_backend_connection_latency_seconds",
		Help:    "Backend connection establishment latency in seconds",
		Buckets: prometheus.ExponentialBuckets(0.001, 2, 10), // 1ms to 1s
	}, []string{"backend"})

	BackendErrorsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "game_gateway_backend_errors_total",
		Help: "Total number of backend errors",
	}, []string{"backend", "error_type"})

	BackendTimeoutsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "game_gateway_backend_timeouts_total",
		Help: "Total number of backend request timeouts",
	}, []string{"backend"})

	BackendRetriesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "game_gateway_backend_retries_total",
		Help: "Total number of backend request retries",
	}, []string{"backend"})

	BackendBytesTransmitted = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "game_gateway_backend_bytes_transmitted_total",
		Help: "Total bytes transmitted to backend",
	}, []string{"backend", "direction"})

	// Request latency (frontend)
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

	// Configuration refresh success metrics
	ConfigRefreshSuccess = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "game_gateway_config_refresh_success_total",
		Help: "Total number of successful configuration refreshes",
	}, []string{"config_type"})
)

// IncConnectionRejected increments the connection rejected counter
func IncConnectionRejected(reason string) {
	ConnectionRejected.WithLabelValues(reason).Inc()
}

// Note: Resource utilization metrics (goroutines, memory, GC) are provided
// by the Prometheus Go client library as standard metrics:
// - go_goroutines
// - go_memstats_alloc_bytes
// - go_memstats_heap_inuse_bytes
// - go_memstats_sys_bytes
// - go_memstats_gc_pause_seconds_total
// - go_memstats_gc_count_total
// These metrics are automatically collected without STW pauses.
