// Package controlplane is a thin HTTP client for the Usenova control plane
// API. It is used by CLI commands that operate on control-plane resources
// (e.g. linked Stellar accounts) rather than the local agent.
package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultTimeout bounds a single control-plane request.
const DefaultTimeout = 30 * time.Second

// Client talks to the control plane REST API.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// New builds a control-plane client. A trailing slash on baseURL is tolerated.
// It refuses a credentialed non-loopback plaintext HTTP URL so the bearer token
// is never sent in cleartext (CWE-319); loopback and https endpoints remain
// allowed.
func New(baseURL, token string, timeout time.Duration) (*Client, error) {
	if err := checkTransport(baseURL, token); err != nil {
		return nil, err
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
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
		return fmt.Errorf("invalid control plane URL %q: %w", baseURL, err)
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

// StellarAccount mirrors the control plane's linked-account payload.
type StellarAccount struct {
	Address  string    `json:"address"`
	Network  string    `json:"network"`
	Label    string    `json:"label,omitempty"`
	LinkedAt time.Time `json:"linked_at"`
}

// StellarBalance mirrors one asset balance in a BalanceResponse.
type StellarBalance struct {
	Asset  string `json:"asset"`
	Issuer string `json:"issuer,omitempty"`
	Amount string `json:"amount"`
}

// LinkAccountResponse is the control plane's account-link response.
type LinkAccountResponse struct {
	Account *StellarAccount `json:"account"`
	Message string          `json:"message,omitempty"`
}

// AccountListResponse is the control plane's account-list response.
type AccountListResponse struct {
	Accounts []*StellarAccount `json:"accounts"`
	Total    int               `json:"total"`
}

// BalanceResponse is the control plane's balance response.
type BalanceResponse struct {
	Address string           `json:"address"`
	Network string           `json:"network"`
	Balance []StellarBalance `json:"balances"`
}

// ErrorResponse mirrors the control plane's error envelope.
type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
	// Network and Horizon details are optional enrichments returned for
	// Stellar upstream failures.
	Network       string `json:"network,omitempty"`
	HorizonURL    string `json:"horizon_url,omitempty"`
	HorizonStatus int    `json:"horizon_status,omitempty"`
}

// APIError is a non-2xx response from the control plane. It carries the HTTP
// status plus any Stellar-specific metadata so commands can render actionable
// messages without parsing free-form text.
type APIError struct {
	StatusCode    int
	Title         string
	Message       string
	Network       string
	HorizonURL    string
	HorizonStatus int
}

func (e *APIError) Error() string {
	msg := e.Title
	if e.Message != "" {
		msg += ": " + e.Message
	}
	return fmt.Sprintf("control plane error (%d): %s", e.StatusCode, msg)
}

func (c *Client) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var buf io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		buf = bytes.NewReader(b)
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
	return c.http.Do(req)
}

func (c *Client) decode(resp *http.Response, out any) error {
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		var e ErrorResponse
		if json.Unmarshal(raw, &e) == nil && e.Error != "" {
			return &APIError{
				StatusCode:    resp.StatusCode,
				Title:         e.Error,
				Message:       e.Message,
				Network:       e.Network,
				HorizonURL:    e.HorizonURL,
				HorizonStatus: e.HorizonStatus,
			}
		}
		return &APIError{
			StatusCode: resp.StatusCode,
			Title:      strings.TrimSpace(string(raw)),
		}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// LinkStellarAccount links a Stellar address with an optional label.
func (c *Client) LinkStellarAccount(ctx context.Context, address, label string) (*LinkAccountResponse, error) {
	resp, err := c.do(ctx, http.MethodPost, "/api/v1/stellar/accounts", map[string]string{
		"address": address,
		"label":   label,
	})
	if err != nil {
		return nil, err
	}
	var out LinkAccountResponse
	if err := c.decode(resp, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListStellarAccounts returns all linked Stellar accounts.
func (c *Client) ListStellarAccounts(ctx context.Context) (*AccountListResponse, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/v1/stellar/accounts", nil)
	if err != nil {
		return nil, err
	}
	var out AccountListResponse
	if err := c.decode(resp, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UnlinkStellarAccount removes a linked account.
func (c *Client) UnlinkStellarAccount(ctx context.Context, address string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/api/v1/stellar/accounts/"+address, nil)
	if err != nil {
		return err
	}
	return c.decode(resp, nil)
}

// StellarBalance fetches balances for an address, optionally filtered by asset.
func (c *Client) StellarBalance(ctx context.Context, address, asset string) (*BalanceResponse, error) {
	path := "/api/v1/stellar/accounts/" + address + "/balance"
	if asset != "" {
		path += "?asset=" + asset
	}
	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var out BalanceResponse
	if err := c.decode(resp, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
