package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/byoi-platform/control-plane/internal/agent"
	"github.com/byoi-platform/control-plane/internal/server"
	"github.com/sirupsen/logrus"
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
	}).Info("Usenova Control Plane starting")

	// Initialize agent store
	agentStore := agent.NewAgentStore()
	log.Info("Agent store initialized")

	// Initialize agent handler
	agentHandler := agent.NewHandler(agentStore, log)

	// Command channel. Operators enqueue commands with the operator token;
	// agents claim them with their session token. Without an operator token
	// the operator-facing part of the command API stays disabled.
	operatorToken := os.Getenv("CONTROL_PLANE_ADMIN_TOKEN")
	if operatorToken == "" {
		log.Warn("CONTROL_PLANE_ADMIN_TOKEN is not set: the command API is disabled")
	}
	commandQueue := agent.NewCommandQueue(agent.DefaultMaxCommandDepth, agent.DefaultCommandLease)
	commandHandler := agent.NewCommandHandler(agentStore, commandQueue, operatorToken, log)

	// Setup HTTP server with all routes
	srv := server.NewServer(agentHandler, commandHandler, log)

	httpServer := &http.Server{
		Addr:    ":8080",
		Handler: srv.GetRouter(),
	}

	// Start server in goroutine
	go func() {
		log.Info("Control Plane listening on :8080")
		log.Info("Available endpoints:")
		log.Info("  GET  /health")
		log.Info("  POST /api/v1/agents/register")
		log.Info("  POST /api/v1/agents/:agentID/heartbeat")
		log.Info("  GET  /api/v1/agents")
		log.Info("  GET  /api/v1/agents/:agentID")
		log.Info("  POST /api/v1/agents/:agentID/commands          (operator token)")
		log.Info("  GET  /api/v1/agents/:agentID/commands          (operator token)")
		log.Info("  GET  /api/v1/agents/:agentID/commands/:id      (operator token)")
		log.Info("  GET  /api/v1/agents/:agentID/commands/next     (agent session token)")
		log.Info("  POST /api/v1/agents/:agentID/commands/:id/result (agent session token)")

		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Failed to start server: %v", err)
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info("Shutting down server...")

	// Graceful shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	log.Info("Server stopped")
	fmt.Println("Control Plane stopped")
}
