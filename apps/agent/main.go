package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

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
	}).Info("BYOI Agent starting")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		log.WithField("signal", sig).Info("Received shutdown signal")
		cancel()
	}()

	// TODO: Initialize agent components
	// - Connect to control plane
	// - Start health check loop
	// - Monitor local services
	// - Report metrics

	log.Info("BYOI Agent ready")

	<-ctx.Done()
	log.Info("BYOI Agent shutting down")

	// TODO: Graceful shutdown
	fmt.Println("Agent stopped")
}
