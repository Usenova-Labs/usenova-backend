package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

// DockerDetector reports whether a Docker daemon is reachable.
type DockerDetector func(ctx context.Context) bool

// ServicesCounter reports how many services the agent currently manages.
type ServicesCounter func() int

// Client handles communication with the Control Plane API
type Client struct {
	baseURL    string
	agentToken string
	agentID    string
	httpClient *http.Client
	log        *logrus.Logger
	startTime  time.Time

	// Injected by the agent at startup so the control plane client stays
	// decoupled from Docker/runtime internals.
	dockerDetector  DockerDetector
	servicesCounter ServicesCounter
}

// NewClient creates a new Control Plane API client
func NewClient(baseURL, agentToken string, log *logrus.Logger) *Client {
	return &Client{
		baseURL:    strings.TrimSuffix(baseURL, "/"),
		agentToken: agentToken,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		log:       log,
		startTime: time.Now(),
	}
}

// Register registers this agent with the control plane
func (c *Client) Register(ctx context.Context, agentID, version string) (*RegisterResponse, error) {
	hostname, err := os.Hostname()
	if err != nil {
		c.log.WithError(err).Warn("Failed to get hostname, using fallback")
		hostname = "unknown-host"
	}

	// Detect capabilities
	capabilities := AgentCapabilities{
		Docker: c.hasDocker(),
	}

	request := RegisterRequest{
		AgentID:      agentID,
		Hostname:     hostname,
		Version:      version,
		Capabilities: capabilities,
	}

	c.log.WithFields(logrus.Fields{
		"agent_id":     agentID,
		"hostname":     hostname,
		"version":      version,
		"capabilities": capabilities,
	}).Info("Registering agent with control plane")

	var response RegisterResponse
	err = c.makeRequest(ctx, "POST", "/api/v1/agents/register", request, &response)
	if err != nil {
		return nil, fmt.Errorf("registration failed: %w", err)
	}

	// Store the agent ID returned by the control plane
	c.agentID = response.AgentID

	c.log.WithFields(logrus.Fields{
		"agent_id": response.AgentID,
		"message":  response.Message,
	}).Info("Successfully registered with control plane")

	return &response, nil
}

// SendHeartbeat sends a heartbeat to the control plane
func (c *Client) SendHeartbeat(ctx context.Context) error {
	if c.agentID == "" {
		return fmt.Errorf("agent not registered - no agent ID available")
	}

	status := AgentStatus{
		Uptime:          time.Since(c.startTime),
		RunningServices: c.getRunningServicesCount(),
	}

	request := HeartbeatRequest{
		Status: status,
	}

	var response HeartbeatResponse
	endpoint := fmt.Sprintf("/api/v1/agents/%s/heartbeat", c.agentID)

	err := c.makeRequest(ctx, "POST", endpoint, request, &response)
	if err != nil {
		return fmt.Errorf("heartbeat failed: %w", err)
	}

	c.log.WithFields(logrus.Fields{
		"agent_id": c.agentID,
		"uptime":   status.Uptime.Round(time.Second),
		"services": status.RunningServices,
		"status":   response.Status,
	}).Debug("Heartbeat sent successfully")

	return nil
}

// makeRequest is a helper function to make HTTP requests to the control plane
func (c *Client) makeRequest(ctx context.Context, method, endpoint string, request, response interface{}) error {
	url := c.baseURL + endpoint

	// Marshal request body
	var body io.Reader
	if request != nil {
		jsonData, err := json.Marshal(request)
		if err != nil {
			return fmt.Errorf("failed to marshal request: %w", err)
		}
		body = bytes.NewBuffer(jsonData)
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.agentToken)
	req.Header.Set("User-Agent", fmt.Sprintf("Usenova-Agent/1.0 (%s; %s)", runtime.GOOS, runtime.GOARCH))

	// Make the request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	// Handle non-2xx responses
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errorResp ErrorResponse
		if json.Unmarshal(respBody, &errorResp) == nil {
			return fmt.Errorf("API error (status %d): %s", resp.StatusCode, errorResp.Error)
		}
		return fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	// Unmarshal successful response
	if response != nil {
		if err := json.Unmarshal(respBody, response); err != nil {
			return fmt.Errorf("failed to unmarshal response: %w", err)
		}
	}

	return nil
}

// SetDockerDetector injects the Docker availability probe used during
// registration capability reporting.
func (c *Client) SetDockerDetector(d DockerDetector) {
	c.dockerDetector = d
}

// SetServicesCounter injects the source of truth for the running services
// count reported in heartbeats.
func (c *Client) SetServicesCounter(s ServicesCounter) {
	c.servicesCounter = s
}

// hasDocker checks if Docker is available on this system
func (c *Client) hasDocker() bool {
	if c.dockerDetector != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return c.dockerDetector(ctx)
	}

	// Fallback: check if docker CLI exists (agent was started without a
	// Docker detector wired in, e.g. in unit tests).
	_, err := os.Stat("/usr/bin/docker")
	if err == nil {
		return true
	}
	_, err = os.Stat("/usr/local/bin/docker")
	return err == nil
}

// getRunningServicesCount returns the number of services currently managed by this agent
func (c *Client) getRunningServicesCount() int {
	if c.servicesCounter != nil {
		return c.servicesCounter()
	}
	return 0
}

// GetAgentID returns the current agent ID
func (c *Client) GetAgentID() string {
	return c.agentID
}
