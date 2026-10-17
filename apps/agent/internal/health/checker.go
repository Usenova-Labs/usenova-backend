package health

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"
)

// CheckType represents the type of health check
type CheckType string

const (
	HTTP CheckType = "http"
	TCP  CheckType = "tcp"
)

// CheckResult represents the result of a health check
type CheckResult struct {
	Healthy bool
	Error   error
	Latency time.Duration
	Time    time.Time
}

// CheckConfig holds configuration for a health check
type CheckConfig struct {
	Type             CheckType
	Endpoint         string // For HTTP: e.g., "http://localhost:8080/health"
	Host             string // For TCP: e.g., "localhost"
	Port             int    // For TCP
	Timeout          time.Duration
	Interval         time.Duration
	Retries          int
	SuccessThreshold int // Number of consecutive successes needed
	FailureThreshold int // Number of consecutive failures needed
}

// Checker performs health checks
type Checker struct {
	client *http.Client
	log    *logrus.Logger
}

// NewChecker creates a new health checker
func NewChecker(log *logrus.Logger) *Checker {
	return &Checker{
		client: &http.Client{
			Timeout: 10 * time.Second,
			// Never follow redirects. A probe must only ever hit the exact
			// endpoint we configured: following a redirect would let a workload
			// steer the agent into probing an arbitrary host (SSRF), and a 3xx
			// is not a healthy status anyway.
			// TODO: support custom CA / mTLS for probes to self-signed endpoints
			// via an explicit, opt-in config flag.
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		log: log,
	}
}

// Check performs a single health check
func (c *Checker) Check(ctx context.Context, config CheckConfig) CheckResult {
	start := time.Now()
	result := CheckResult{
		Time: start,
	}

	switch config.Type {
	case HTTP:
		result = c.checkHTTP(ctx, config, start)
	case TCP:
		result = c.checkTCP(ctx, config, start)
	default:
		result.Error = fmt.Errorf("unsupported check type: %s", config.Type)
		result.Healthy = false
		result.Latency = time.Since(start)
	}

	if result.Healthy {
		c.log.WithFields(logrus.Fields{
			"type":    config.Type,
			"latency": result.Latency,
		}).Debug("Health check passed")
	} else {
		c.log.WithFields(logrus.Fields{
			"type":  config.Type,
			"error": result.Error,
		}).Debug("Health check failed")
	}

	return result
}

// checkHTTP performs an HTTP health check
func (c *Checker) checkHTTP(ctx context.Context, config CheckConfig, start time.Time) CheckResult {
	if config.Endpoint == "" {
		return CheckResult{
			Healthy: false,
			Error:   fmt.Errorf("HTTP endpoint not specified"),
			Latency: time.Since(start),
			Time:    start,
		}
	}

	timeout := config.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	checkCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(checkCtx, "GET", config.Endpoint, nil)
	if err != nil {
		return CheckResult{
			Healthy: false,
			Error:   fmt.Errorf("failed to create HTTP request: %w", err),
			Latency: time.Since(start),
			Time:    start,
		}
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return CheckResult{
			Healthy: false,
			Error:   err,
			Latency: time.Since(start),
			Time:    start,
		}
	}
	defer resp.Body.Close()

	// Consider 2xx as healthy
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return CheckResult{
			Healthy: true,
			Error:   nil,
			Latency: time.Since(start),
			Time:    start,
		}
	}

	return CheckResult{
		Healthy: false,
		Error:   fmt.Errorf("HTTP check returned status %d", resp.StatusCode),
		Latency: time.Since(start),
		Time:    start,
	}
}

// checkTCP performs a TCP health check
func (c *Checker) checkTCP(ctx context.Context, config CheckConfig, start time.Time) CheckResult {
	if config.Host == "" || config.Port <= 0 {
		return CheckResult{
			Healthy: false,
			Error:   fmt.Errorf("TCP host/port not specified"),
			Latency: time.Since(start),
			Time:    start,
		}
	}

	timeout := config.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	address := fmt.Sprintf("%s:%d", config.Host, config.Port)

	dialer := &net.Dialer{
		Timeout: timeout,
	}

	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return CheckResult{
			Healthy: false,
			Error:   err,
			Latency: time.Since(start),
			Time:    start,
		}
	}
	defer conn.Close()

	return CheckResult{
		Healthy: true,
		Error:   nil,
		Latency: time.Since(start),
		Time:    start,
	}
}
