package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultTimeout bounds a single API request. Deployments may outlive it; the
// CLI reconciles by querying service state after a timeout.
const DefaultTimeout = 120 * time.Second

type Client struct {
	baseURL string
	token   string
	http    *http.Client
	// stream is a client without an overall timeout, used for log streaming
	// where the connection may stay open indefinitely. Cancellation is driven
	// by the request context (e.g. Ctrl-C) instead of a deadline.
	stream *http.Client
}

// New builds a client for baseURL. It refuses a credentialed non-loopback
// plaintext HTTP URL so the bearer token is never sent in cleartext
// (CWE-319); loopback and https endpoints remain allowed.
func New(baseURL, token string, timeout time.Duration) (*Client, error) {
	if baseURL == "" {
		baseURL = "http://127.0.0.1:9090"
	}
	if err := checkTransport(baseURL, token); err != nil {
		return nil, err
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		token:   token,
		http: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) == 0 {
					return nil
				}
				if len(via) > 10 {
					return http.ErrUseLastResponse
				}
				// Check if any request in the chain has Authorization
				for _, v := range via {
					if v.Header.Get("Authorization") != "" {
						// Reject if redirecting to HTTP
						if strings.EqualFold(req.URL.Scheme, "http") {
							// Reject HTTPS->HTTP downgrade
							for _, vv := range via {
								if strings.EqualFold(vv.URL.Scheme, "https") {
									return fmt.Errorf("refusing to follow token-bearing redirect from HTTPS to HTTP")
								}
							}
							// Reject remote HTTP
							if !isLoopbackHost(req.URL.Hostname()) {
								return fmt.Errorf("refusing to follow token-bearing redirect to plaintext HTTP")
							}
							// Also reject in general for same-host HTTP downgrade
							return fmt.Errorf("refusing to follow token-bearing redirect to plaintext HTTP")
						}
					}
				}
				return nil
			},
		},
		stream: &http.Client{
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) == 0 {
					return nil
				}
				if len(via) > 10 {
					return http.ErrUseLastResponse
				}
				for _, v := range via {
					if v.Header.Get("Authorization") != "" {
						if strings.EqualFold(req.URL.Scheme, "http") {
							for _, vv := range via {
								if strings.EqualFold(vv.URL.Scheme, "https") {
									return fmt.Errorf("refusing to follow token-bearing redirect from HTTPS to HTTP")
								}
							}
							if !isLoopbackHost(req.URL.Hostname()) {
								return fmt.Errorf("refusing to follow token-bearing redirect to plaintext HTTP")
							}
							return fmt.Errorf("refusing to follow token-bearing redirect to plaintext HTTP")
						}
					}
				}
				return nil
			},
		},
	}, nil
}

// checkTransport rejects a bearer token bound for a non-loopback host over
// plaintext HTTP. Loopback (127.0.0.0/8, ::1, localhost) and any non-http
// scheme are accepted; with no token there is no credential to expose.
func checkTransport(baseURL, token string) error {
	if token == "" {
		return nil
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("invalid agent URL %q: %w", baseURL, err)
	}
	if !strings.EqualFold(u.Scheme, "http") {
		return nil
	}
	if host := u.Hostname(); isLoopbackHost(host) {
		return nil
	}
	return fmt.Errorf("refusing to send the API token over plaintext HTTP to non-loopback host %q: use https:// or a loopback address", u.Host)
}

// isLoopbackHost reports whether host names a loopback address.
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

// ValidateServiceName rejects names that cannot round-trip as a single path
// segment on the agent API. The agent derives the name from the decoded
// r.URL.Path and splits on "/", so an embedded "/" cannot be preserved by
// escaping alone and must be rejected outright.
func ValidateServiceName(name string) error {
	if name == "" {
		return errors.New("service name is required")
	}
	if name == "." || name == ".." || strings.ContainsRune(name, '/') {
		return fmt.Errorf("invalid service name %q: must be a single path segment (no %q)", name, "/")
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("invalid service name %q: control characters are not allowed", name)
		}
	}
	return nil
}

// servicePath builds "/services/{escapedName}" so reserved characters such as
// "?", "#" and spaces survive as part of the segment instead of being parsed
// as query/fragment delimiters.
func servicePath(name string) string {
	return "/services/" + url.PathEscape(name)
}

// IsTimeout reports whether err was caused by the configured request deadline
// (HTTP client timeout or context deadline), as opposed to a server error.
func IsTimeout(err error) bool {
	if err == nil {
		return false
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Timeout()
	}
	return errors.Is(err, context.DeadlineExceeded)
}

func (c *Client) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	return c.doWith(ctx, c.http, method, path, body)
}

// doWith issues a request using the given HTTP client. do uses the
// timeout-bounded client; streaming callers use c.stream instead.
func (c *Client) doWith(ctx context.Context, hc *http.Client, method, path string, body any) (*http.Response, error) {
	var buf io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		buf = bytes.NewBuffer(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, buf)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	return hc.Do(req)
}

func (c *Client) ListServices(ctx context.Context) (*ListResponse, error) {
	resp, err := c.do(ctx, http.MethodGet, "/services", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("list services failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var lr ListResponse
	if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
		return nil, err
	}
	return &lr, nil
}

func (c *Client) GetService(ctx context.Context, name string) (*Service, error) {
	if err := ValidateServiceName(name); err != nil {
		return nil, err
	}
	resp, err := c.do(ctx, http.MethodGet, servicePath(name), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get service failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var out Service
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) Deploy(ctx context.Context, spec map[string]any) (any, error) {
	resp, err := c.do(ctx, http.MethodPost, "/services", spec)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("deploy failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var out any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) Stop(ctx context.Context, name string) error {
	if err := ValidateServiceName(name); err != nil {
		return err
	}
	resp, err := c.do(ctx, http.MethodPost, servicePath(name)+"/stop", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("stop failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

// LogsOptions selects which logs to fetch from the agent.
type LogsOptions struct {
	// Tail is the number of lines from the end of the log, or "all". Empty
	// lets the agent apply its default (100).
	Tail string
	// Follow streams new log lines until the context is canceled.
	Follow bool
	// Timestamps prefixes each line with its RFC3339Nano timestamp.
	Timestamps bool
}

// Logs opens a log stream for a service. The caller must close the returned
// reader. Agent errors (missing service, Docker unavailable) surface as an
// error.
func (c *Client) Logs(ctx context.Context, name string, opts LogsOptions) (io.ReadCloser, error) {
	if err := ValidateServiceName(name); err != nil {
		return nil, err
	}
	q := url.Values{}
	if opts.Tail != "" {
		q.Set("tail", opts.Tail)
	}
	if opts.Follow {
		q.Set("follow", "true")
	}
	if opts.Timestamps {
		q.Set("timestamps", "true")
	}
	path := servicePath(name) + "/logs"
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}

	resp, err := c.doWith(ctx, c.stream, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("logs failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return resp.Body, nil
}

func (c *Client) Remove(ctx context.Context, name string) error {
	if err := ValidateServiceName(name); err != nil {
		return err
	}
	resp, err := c.do(ctx, http.MethodDelete, servicePath(name), nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("remove failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}
