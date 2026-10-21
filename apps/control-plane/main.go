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
	log.WithFields(logrus.Fields{
		"path":   dbPath,
		"agents": len(agentStore.ListAgents()),
	}).Info("Agent registry initialized")

	// Initialize agent handler
	agentHandler := agent.NewHandler(agentStore, log)

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
	srv := server.NewServer(agentHandler, stellarHandler, log)

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
