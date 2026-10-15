package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/byoi-platform/agent/internal/agent"
	"github.com/byoi-platform/agent/internal/config"
)

var (
	version = "dev"
	commit  = "none"
	log     = logrus.New()
)

func main() {
	log.SetFormatter(&logrus.JSONFormatter{})
	log.SetLevel(logrus.InfoLevel)

	log.WithFields(logrus.Fields{
		"version": version,
		"commit":  commit,
	}).Info("Usenova Agent starting")

	// Load configuration
	cfg, err := config.Load(version)
	if err != nil {
		log.WithError(err).Fatal("Failed to load configuration")
	}

	// Set log level based on environment
	if os.Getenv("DEBUG") != "" {
		log.SetLevel(logrus.DebugLevel)
		log.Debug("Debug logging enabled")
	}

	// Create main context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create and start the agent
	agentInstance := agent.New(cfg, log)

	// Start the agent
	if err := agentInstance.Start(ctx); err != nil {
		log.WithError(err).Fatal("Failed to start agent")
	}

	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Wait for shutdown signal
	sig := <-sigChan
	log.WithField("signal", sig).Info("Received shutdown signal, starting graceful shutdown...")

	// Cancel context to signal shutdown
	cancel()

	// Create shutdown context with timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	// Stop the agent
	if err := agentInstance.Stop(shutdownCtx); err != nil {
		log.WithError(err).Error("Error during agent shutdown")
		os.Exit(1)
	}

	log.Info("Agent shutdown complete")
	fmt.Println("Agent stopped")
}
