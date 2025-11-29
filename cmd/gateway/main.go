package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/SkynetNext/game-gateway/internal/config"
	"github.com/SkynetNext/game-gateway/internal/gateway"
	"github.com/SkynetNext/game-gateway/internal/logger"
	"github.com/SkynetNext/game-gateway/internal/tracing"
	"go.uber.org/zap"
)

var (
	version   = "dev"
	buildTime = "unknown"
	gitCommit = "unknown"
)

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "config/config.yaml", "Configuration file path")
	flag.Parse()

	// Get Pod name (K8s environment)
	podName := os.Getenv("POD_NAME")
	if podName == "" {
		// Fallback: use hostname
		hostname, _ := os.Hostname()
		podName = hostname
	}

	// Initialize logger with OpenTelemetry-compatible format
	logLevel := os.Getenv("LOG_LEVEL")
	if logLevel == "" {
		logLevel = "info"
	}

	// Initialize logger with service info (OTel Resource attributes)
	// Application-layer attributes: service.name, service.version
	// Infrastructure-layer attributes (service.namespace, service.instance.id) are added from K8s metadata
	if err := logger.Init(logger.Config{
		Level: logLevel,
		ServiceInfo: logger.ServiceInfo{
			Name:    "game-gateway", // Application defines its name
			Version: version,        // Build-time version
		},
	}); err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}
	defer logger.Sync()

	// Load configuration
	cfg, err := config.Load(configPath)
	if err != nil {
		logger.L.Fatal("Failed to load configuration", zap.Error(err))
	}

	// Create gateway instance
	gw, err := gateway.New(cfg, podName)
	if err != nil {
		logger.L.Fatal("Failed to create gateway", zap.Error(err))
	}

	// Start gateway
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start service
	if err := gw.Start(ctx); err != nil {
		logger.L.Fatal("Failed to start gateway", zap.Error(err))
	}

	// Initialize tracing (OTLP → OTel Collector → Tempo)
	// Set OTEL_EXPORTER_OTLP_ENDPOINT=otel-collector:4317
	otelEndpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if otelEndpoint != "" {
		if err := tracing.Init("game-gateway", version, otelEndpoint); err != nil {
			logger.Warn("Failed to initialize tracing", zap.Error(err))
		} else {
			logger.Info("Tracing initialized (OTLP)", zap.String("endpoint", otelEndpoint))
		}
	}

	logger.Info("Game Gateway started successfully",
		zap.String("version", version),
		zap.String("build_time", buildTime),
		zap.String("git_commit", gitCommit),
		zap.String("pod", podName),
	)

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	logger.Info("Received stop signal, starting graceful shutdown...")

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.GracefulShutdownTimeout)
	defer shutdownCancel()

	if err := gw.Shutdown(shutdownCtx); err != nil {
		logger.Error("Error during gateway shutdown", zap.Error(err))
	}

	// Shutdown tracing
	if err := tracing.Shutdown(shutdownCtx); err != nil {
		logger.Warn("Error during tracing shutdown", zap.Error(err))
	}

	logger.Info("Game Gateway closed")
}
