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
	"net"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/byoi-platform/agent/internal/systeminfo"
)

// DockerDetector reports whether a Docker daemon is reachable.
type DockerDetector func(ctx context.Context) bool

// ServicesCounter reports how many services the agent currently manages.
type ServicesCounter func() int

// SystemInfoDetector reports the machine inventory of the host the agent
// runs on.
type SystemInfoDetector func(ctx context.Context) AgentSystemInfo

// DockerVersionDetector reports the version of the Docker engine available on
// the host. It returns an empty string when the version is unknown.
type DockerVersionDetector func(ctx context.Context) string

// FailedServicesCounter reports how many services the agent gave up on
// (they exhausted their restart budget or their deploy failed).
type FailedServicesCounter func() int

// Client handles communication with the Control Plane API
type Client struct {
	baseURL    string
	agentToken string
	agentID    string
	httpClient *http.Client
	log        *logrus.Logger
	startTime  time.Time

	// mu guards agentID and sessionToken, which are written on registration and
	// read by concurrent heartbeat sends.
	mu sync.RWMutex
	// sessionToken is the token issued by the control plane at registration.
	// While empty (before registration) requests fall back to agentToken, the
	// static bootstrap token from configuration.
	sessionToken string

	// Injected by the agent at startup so the control plane client stays
	// decoupled from Docker/runtime internals.
	dockerDetector        DockerDetector
	servicesCounter       ServicesCounter
	systemInfoDetector    SystemInfoDetector
	dockerVersionDetector DockerVersionDetector
	failedServicesCounter FailedServicesCounter
}

// NewClient creates a new Control Plane API client
func NewClient(baseURL, agentToken string, log *logrus.Logger) *Client {
	return &Client{
		baseURL:    strings.TrimSuffix(baseURL, "/"),
		agentToken: agentToken,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) == 0 {
					return nil
				}
				if len(via) > 10 {
					return http.ErrUseLastResponse
				}
				// If the original request had an Authorization header with a token,
				// reject redirects that would send the token over plaintext.
				for _, v := range via {
					if v.Header.Get("Authorization") != "" {
						// Reject if redirecting to HTTP (plaintext) from HTTPS, or to HTTP scheme
						if strings.EqualFold(req.URL.Scheme, "http") {
							// Also reject if going to non-localhost HTTP
							if !isLoopbackHost(req.URL.Hostname()) {
								return fmt.Errorf("refusing to follow token-bearing redirect to plaintext HTTP")
							}
							// Check if original was HTTPS - downgrade
							for _, vv := range via {
								if strings.EqualFold(vv.URL.Scheme, "https") {
									return fmt.Errorf("refusing to follow token-bearing redirect from HTTPS to HTTP")
								}
							}
							return fmt.Errorf("refusing to follow token-bearing redirect to plaintext HTTP")
						}
					}
				}
				return nil
			},
		},
		log:                log,
		startTime:          time.Now(),
		systemInfoDetector: detectSystemInfo,
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

	// Report the machine inventory so the control plane can reason about
	// the capacity this agent offers.
	system := c.systemInfo(ctx)

	request := RegisterRequest{
		AgentID:      agentID,
		Hostname:     hostname,
		Version:      version,
		Capabilities: capabilities,
		System:       &system,
	}

	c.log.WithFields(logrus.Fields{
		"agent_id":       agentID,
		"hostname":       hostname,
		"version":        version,
		"capabilities":   capabilities,
		"os":             system.OS,
		"arch":           system.Arch,
		"cpu_count":      system.CPUCount,
		"memory_bytes":   system.MemoryBytes,
		"docker_version": system.DockerVersion,
	}).Info("Registering agent with control plane")

	var response RegisterResponse
	err = c.makeRequest(ctx, "POST", "/api/v1/agents/register", request, &response)
	if err != nil {
		return nil, fmt.Errorf("registration failed: %w", err)
	}

	// Store the agent ID and the session token returned by the control plane.
	// Subsequent requests (heartbeat, status) must authenticate with the
	// session token, not the static bootstrap token.
	c.mu.Lock()
	c.agentID = response.AgentID
	if response.SessionToken != "" {
		c.sessionToken = response.SessionToken
	} else {
		c.log.Warn("Control plane did not return a session token; falling back to the bootstrap token")
	}
	c.mu.Unlock()

	c.log.WithFields(logrus.Fields{
		"agent_id": response.AgentID,
		"message":  response.Message,
	}).Info("Successfully registered with control plane")

	return &response, nil
}

// SendHeartbeat sends a heartbeat to the control plane
func (c *Client) SendHeartbeat(ctx context.Context) error {
	c.mu.RLock()
	agentID := c.agentID
	c.mu.RUnlock()
	if agentID == "" {
		return fmt.Errorf("agent not registered - no agent ID available")
	}

	status := AgentStatus{
		Uptime:          time.Since(c.startTime),
		RunningServices: c.getRunningServicesCount(),
		FailedServices:  c.getFailedServicesCount(),
	}

	// Refresh the machine inventory so the control plane keeps track of
	// dynamic values (e.g. a Docker engine upgrade).
	system := c.systemInfo(ctx)

	request := HeartbeatRequest{
		Status: status,
		System: &system,
	}

	var response HeartbeatResponse
	endpoint := fmt.Sprintf("/api/v1/agents/%s/heartbeat", agentID)

	err := c.makeRequest(ctx, "POST", endpoint, request, &response)
	if err != nil {
		return fmt.Errorf("heartbeat failed: %w", err)
	}

	c.log.WithFields(logrus.Fields{
		"agent_id":       agentID,
		"uptime":         status.Uptime.Round(time.Second),
		"services":       status.RunningServices,
		"failed":         status.FailedServices,
		"status":         response.Status,
		"docker_version": system.DockerVersion,
	}).Debug("Heartbeat sent successfully")

	return nil
}

// authToken returns the bearer token to send: the session token once
// registered, otherwise the static bootstrap token from configuration.
func (c *Client) authToken() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.sessionToken != "" {
		return c.sessionToken
	}
	return c.agentToken
}

// FetchCommand asks the control plane for the next command queued for this
// agent. It returns (nil, nil) when no command is pending.
func (c *Client) FetchCommand(ctx context.Context) (*Command, error) {
	c.mu.RLock()
	agentID := c.agentID
	c.mu.RUnlock()
	if agentID == "" {
		return nil, fmt.Errorf("agent not registered - no agent ID available")
	}

	endpoint := fmt.Sprintf("/api/v1/agents/%s/commands/next", agentID)

	// The control plane answers with an empty body when nothing is pending,
	// which leaves the command pointer nil.
	var command *Command
	if err := c.makeRequest(ctx, "GET", endpoint, nil, &command); err != nil {
		return nil, fmt.Errorf("failed to fetch command: %w", err)
	}

	return command, nil
}

// ReportCommandResult reports the outcome of a command this agent executed.
func (c *Client) ReportCommandResult(ctx context.Context, result CommandResult) error {
	c.mu.RLock()
	agentID := c.agentID
	c.mu.RUnlock()
	if agentID == "" {
		return fmt.Errorf("agent not registered - no agent ID available")
	}

	endpoint := fmt.Sprintf("/api/v1/agents/%s/commands/%s/result", agentID, result.CommandID)
	if err := c.makeRequest(ctx, "POST", endpoint, result, nil); err != nil {
		return fmt.Errorf("failed to report command result: %w", err)
	}

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
	req.Header.Set("Authorization", "Bearer "+c.authToken())
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
	if response != nil && len(respBody) > 0 {
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

// SetSystemInfoDetector injects the source of the machine inventory reported
// during registration and heartbeats.
func (c *Client) SetSystemInfoDetector(d SystemInfoDetector) {
	c.systemInfoDetector = d
}

// SetDockerVersionDetector injects the probe used to report the Docker engine
// version as part of the machine inventory.
func (c *Client) SetDockerVersionDetector(d DockerVersionDetector) {
	c.dockerVersionDetector = d
}

// systemInfo returns the machine inventory reported to the control plane.
//
// Detection is best-effort: an agent still registers when the host details
// cannot be determined.
func (c *Client) systemInfo(ctx context.Context) AgentSystemInfo {
	var system AgentSystemInfo

	if c.systemInfoDetector != nil {
		system = c.systemInfoDetector(ctx)
	} else {
		system = detectSystemInfo(ctx)
	}

	// The Docker version is reported by its own probe: it is only known when
	// a Docker engine answers.
	if version := c.dockerVersion(ctx); version != "" {
		system.DockerVersion = version
	}

	return system
}

// dockerVersion returns the version of the Docker engine, or an empty string
// when it is unavailable or unknown.
func (c *Client) dockerVersion(ctx context.Context) string {
	if c.dockerVersionDetector == nil {
		return ""
	}

	versionCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	return c.dockerVersionDetector(versionCtx)
}

// detectSystemInfo returns the machine inventory of the current host.
func detectSystemInfo(_ context.Context) AgentSystemInfo {
	info := systeminfo.Detect()

	return AgentSystemInfo{
		OS:          info.OS,
		Arch:        info.Arch,
		CPUCount:    info.CPUCount,
		MemoryBytes: info.MemoryBytes,
	}
}

// SetFailedServicesCounter injects the source of truth for the count of
// failed services reported in heartbeats.
func (c *Client) SetFailedServicesCounter(s FailedServicesCounter) {
	c.failedServicesCounter = s
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

// getFailedServicesCount returns the number of services this agent gave up on
func (c *Client) getFailedServicesCount() int {
	if c.failedServicesCounter != nil {
		return c.failedServicesCounter()
	}
	return 0
}

// GetAgentID returns the current agent ID
func (c *Client) GetAgentID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.agentID
}

func isLoopbackHost(host string) bool {
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}
