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

	// Initialize logger
	if err := logger.Init("info"); err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}
	defer logger.Sync()

	// Load configuration
	cfg, err := config.Load(configPath)
	if err != nil {
		logger.L.Fatal("Failed to load configuration", zap.Error(err))
	}

	// Get Pod name (K8s environment)
	podName := os.Getenv("POD_NAME")
	if podName == "" {
		// Fallback: use hostname
		hostname, _ := os.Hostname()
		podName = hostname
		logger.L.Info("POD_NAME not found, using hostname",
			zap.String("hostname", podName),
		)
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

	logger.L.Info("Game Gateway started successfully",
		zap.String("version", version),
		zap.String("build_time", buildTime),
		zap.String("git_commit", gitCommit),
		zap.String("pod", podName),
	)

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	logger.L.Info("Received stop signal, starting graceful shutdown...")

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.GracefulShutdownTimeout)
	defer shutdownCancel()

	if err := gw.Shutdown(shutdownCtx); err != nil {
		logger.L.Error("Error during gateway shutdown", zap.Error(err))
	}

	logger.L.Info("Game Gateway closed")
}
