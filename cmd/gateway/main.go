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

	// Load configuration
	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Get Pod name (K8s environment)
	podName := os.Getenv("POD_NAME")
	if podName == "" {
		// Fallback: use hostname
		hostname, _ := os.Hostname()
		podName = hostname
		log.Printf("POD_NAME environment variable not found, using hostname: %s", podName)
	}

	// Create gateway instance
	gw, err := gateway.New(cfg, podName)
	if err != nil {
		log.Fatalf("Failed to create gateway: %v", err)
	}

	// Start gateway
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start service
	if err := gw.Start(ctx); err != nil {
		log.Fatalf("Failed to start gateway: %v", err)
	}

	log.Printf("Game Gateway started successfully (version: %s, build: %s, commit: %s, pod: %s)",
		version, buildTime, gitCommit, podName)

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("Received stop signal, starting graceful shutdown...")

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.GracefulShutdownTimeout)
	defer shutdownCancel()

	if err := gw.Shutdown(shutdownCtx); err != nil {
		log.Printf("Error during gateway shutdown: %v", err)
	}

	log.Println("Game Gateway closed")
}
