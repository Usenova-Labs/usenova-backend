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
	stellarhandler "github.com/byoi-platform/control-plane/internal/stellar"
	"github.com/byoi-platform/stellar"
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

	// Initialize persistent agent registry (SQLite)
	dbPath := os.Getenv("CONTROL_PLANE_DB_PATH")
	if dbPath == "" {
		dbPath = "data/control-plane.db"
	}

	db, err := agent.OpenSQLite(dbPath)
	if err != nil {
		log.WithError(err).Fatalf("Failed to open agent registry database")
	}
	defer db.Close()

	agentStore, err := agent.NewPersistentAgentStore(db)
	if err != nil {
		log.WithError(err).Fatalf("Failed to load agent registry")
	}
	deploymentStore, err := agent.NewPersistentDeploymentStore(db)
	if err != nil {
		log.WithError(err).Fatalf("Failed to load deployment records")
	}
	log.WithFields(logrus.Fields{
		"path":        dbPath,
		"agents":      len(agentStore.ListAgents()),
		"deployments": len(deploymentStore.List(agent.DeploymentFilter{})),
	}).Info("Agent registry initialized")

	// Initialize agent handler. Platform registration is disabled unless a
	// dedicated bootstrap token is configured, so BYOI stays the default.
	platformToken := os.Getenv("CONTROL_PLANE_PLATFORM_TOKEN")
	if platformToken == "" {
		log.Warn("CONTROL_PLANE_PLATFORM_TOKEN is not set: platform registration is disabled")
	}
	agentHandler := agent.NewHandlerWithPlatformToken(agentStore, platformToken, log)

	// Command channel. Operators enqueue commands with the operator token;
	// agents claim them with their session token. Without an operator token
	// the operator-facing part of the command API stays disabled.
	operatorToken := os.Getenv("CONTROL_PLANE_ADMIN_TOKEN")
	if operatorToken == "" {
		log.Warn("CONTROL_PLANE_ADMIN_TOKEN is not set: the command API is disabled")
	}
	commandQueue := agent.NewCommandQueue(agent.DefaultMaxCommandDepth, agent.DefaultCommandLease)
	commandHandler := agent.NewCommandHandler(agentStore, commandQueue, operatorToken, log).
		WithDeploymentStore(deploymentStore)

	// Remote deployments: the scheduler picks a healthy platform agent and the
	// deployment handler enqueues the deploy command on the existing channel.
	scheduler := agent.NewScheduler(agentStore)
	deploymentHandler := agent.NewDeploymentHandler(
		agentStore, deploymentStore, commandQueue, scheduler, operatorToken, log,
	)

	// Initialize Stellar account linking (env-driven; no keys stored).
	stCfg, err := stellarhandler.Load()
	if err != nil {
		log.Fatalf("Invalid Stellar configuration: %v", err)
	}
	log.WithField("config", stCfg.String()).Info("Stellar configuration loaded")
	stStore := stellarhandler.NewAccountStore()
	stClient := stellar.NewClient(stCfg.Stellar)
	stellarHandler := stellarhandler.NewHandler(stStore, stClient, stCfg.Stellar.Network, log)

	// Setup HTTP server with all routes
	srv := server.NewServer(agentHandler, commandHandler, deploymentHandler, stellarHandler, log)

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
		log.Info("  POST /api/v1/agents/register/platform        (platform token)")
		log.Info("  POST /api/v1/agents/:agentID/heartbeat")
		log.Info("  GET  /api/v1/agents")
		log.Info("  GET  /api/v1/agents/:agentID")
		log.Info("  POST /api/v1/agents/:agentID/commands          (operator token)")
		log.Info("  GET  /api/v1/agents/:agentID/commands          (operator token)")
		log.Info("  GET  /api/v1/agents/:agentID/commands/:id      (operator token)")
		log.Info("  GET  /api/v1/agents/:agentID/commands/next     (agent session token)")
		log.Info("  POST /api/v1/agents/:agentID/commands/:id/result (agent session token)")
		log.Info("  POST /api/v1/deployments                        (operator token)")
		log.Info("  GET  /api/v1/deployments                        (operator token)")
		log.Info("  GET  /api/v1/deployments/:deploymentID          (operator token)")
		log.Info("  POST /api/v1/scheduler/select                   (operator token)")
		log.Info("  POST /api/v1/stellar/accounts")
		log.Info("  GET  /api/v1/stellar/accounts")
		log.Info("  DELETE /api/v1/stellar/accounts/:address")
		log.Info("  GET  /api/v1/stellar/accounts/:address/balance")

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
