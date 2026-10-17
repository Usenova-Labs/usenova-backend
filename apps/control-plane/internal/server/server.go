package server

import (
	"net/http"

	"github.com/byoi-platform/control-plane/internal/agent"
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// Server holds the HTTP server dependencies
type Server struct {
	router       *gin.Engine
	agentHandler *agent.Handler
	logger       *logrus.Logger
}

// NewServer creates a new HTTP server with all routes configured
func NewServer(agentHandler *agent.Handler, logger *logrus.Logger) *Server {
	router := gin.Default()

	// Set up middleware
	router.Use(gin.LoggerWithWriter(logger.Writer()))
	router.Use(gin.Recovery())

	s := &Server{
		router:       router,
		agentHandler: agentHandler,
		logger:       logger,
	}

	s.setupRoutes()
	return s
}

// setupRoutes configures all HTTP routes
func (s *Server) setupRoutes() {
	// Health check endpoint
	s.router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":  "healthy",
			"message": "Control Plane API is running",
		})
	})

	// API v1 routes
	v1 := s.router.Group("/api/v1")
	{
		// Agent management endpoints
		agents := v1.Group("/agents")
		{
			agents.POST("/register", s.agentHandler.RegisterAgent)
			agents.POST("/:agentID/heartbeat", s.agentHandler.HandleHeartbeat)
			agents.GET("", s.agentHandler.ListAgents)        // Bonus endpoint
			agents.GET("/:agentID", s.agentHandler.GetAgent) // Bonus endpoint
		}

		// TODO: Add other API groups
		// services := v1.Group("/services")
		// deployments := v1.Group("/deployments")
		// payments := v1.Group("/payments")
	}
}

// GetRouter returns the Gin router for HTTP server integration
func (s *Server) GetRouter() *gin.Engine {
	return s.router
}
