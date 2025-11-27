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
)

