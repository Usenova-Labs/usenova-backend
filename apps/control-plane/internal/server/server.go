package server

import (
	"net/http"

	"github.com/byoi-platform/control-plane/internal/agent"
	stellarhandler "github.com/byoi-platform/control-plane/internal/stellar"
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// Server holds the HTTP server dependencies
type Server struct {
	router            *gin.Engine
	agentHandler      *agent.Handler
	commandHandler    *agent.CommandHandler
	deploymentHandler *agent.DeploymentHandler
	stellarHandler    *stellarhandler.Handler
	logger            *logrus.Logger
}

// NewServer creates a new HTTP server with all routes configured
func NewServer(agentHandler *agent.Handler, commandHandler *agent.CommandHandler, deploymentHandler *agent.DeploymentHandler, stellarHandler *stellarhandler.Handler, logger *logrus.Logger) *Server {
	router := gin.Default()

	// Set up middleware
	router.Use(gin.LoggerWithWriter(logger.Writer()))
	router.Use(gin.Recovery())

	s := &Server{
		router:            router,
		agentHandler:      agentHandler,
		commandHandler:    commandHandler,
		deploymentHandler: deploymentHandler,
		stellarHandler:    stellarHandler,
		logger:            logger,
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
			// Registration is unauthenticated: this is where the session
			// token is first issued. The platform path additionally requires
			// the platform bootstrap token; BYOI agents use /register.
			agents.POST("/register", s.agentHandler.RegisterAgent)
			agents.POST("/register/platform", s.agentHandler.RegisterPlatformAgent)

			// Agent-scoped routes require the bearer session token issued at
			// registration.
			agents.POST("/:agentID/heartbeat", s.agentHandler.RequireAgentAuth(), s.agentHandler.HandleHeartbeat)
			agents.GET("/:agentID", s.agentHandler.RequireAgentAuth(), s.agentHandler.GetAgent) // Bonus endpoint

			agents.GET("", s.agentHandler.ListAgents) // Bonus endpoint
		}

		// Command channel: operators enqueue commands, agents claim and
		// complete them. Agents poll, the control plane never dials out.
		{
			agents.POST("/:agentID/commands", s.commandHandler.RequireOperatorToken(), s.commandHandler.EnqueueCommand)
			agents.GET("/:agentID/commands", s.commandHandler.RequireOperatorToken(), s.commandHandler.ListCommands)
			agents.GET("/:agentID/commands/next", s.commandHandler.RequireAgentSession(), s.commandHandler.ClaimCommand)
			agents.GET("/:agentID/commands/:commandID", s.commandHandler.RequireOperatorToken(), s.commandHandler.GetCommand)
			agents.POST("/:agentID/commands/:commandID/result", s.commandHandler.RequireAgentSession(), s.commandHandler.CompleteCommand)
		}

		// Deployment records and scheduler placement. Both require the
		// operator token and are disabled when it is not configured.
		if s.deploymentHandler != nil {
			deployments := v1.Group("/deployments")
			{
				deployments.POST("", s.deploymentHandler.RequireOperatorToken(), s.deploymentHandler.CreateDeployment)
				deployments.GET("", s.deploymentHandler.RequireOperatorToken(), s.deploymentHandler.ListDeployments)
				deployments.GET("/:deploymentID", s.deploymentHandler.RequireOperatorToken(), s.deploymentHandler.GetDeployment)
				deployments.PUT("/:deploymentID/escrow", s.deploymentHandler.RequireOperatorToken(), s.deploymentHandler.LinkDeploymentEscrow)
				deployments.POST("/:deploymentID/escrow", s.deploymentHandler.RequireOperatorToken(), s.deploymentHandler.LinkDeploymentEscrow)
				deployments.GET("/:deploymentID/escrow", s.deploymentHandler.RequireOperatorToken(), s.deploymentHandler.GetDeploymentEscrow)
			}

			scheduler := v1.Group("/scheduler")
			{
				scheduler.POST("/select", s.deploymentHandler.RequireOperatorToken(), s.deploymentHandler.SelectAgent)
			}
		}

		// TODO: Add other API groups
		// services := v1.Group("/services")
		// payments := v1.Group("/payments")

		// Stellar account linking and balance queries
		if s.stellarHandler != nil {
			stellarGroup := v1.Group("/stellar")
			{
				accounts := stellarGroup.Group("/accounts")
				{
					accounts.POST("", s.stellarHandler.LinkAccount)
					accounts.GET("", s.stellarHandler.ListAccounts)
					accounts.DELETE("/:address", s.stellarHandler.UnlinkAccount)
					accounts.GET("/:address/balance", s.stellarHandler.GetBalance)
				}
			}
		}
	}
}

// GetRouter returns the Gin router for HTTP server integration
func (s *Server) GetRouter() *gin.Engine {
	return s.router
}
