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
	"net/http"
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
func New(baseURL, token string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    &http.Client{Timeout: timeout},
	}
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
			msg := e.Error
			if e.Message != "" {
				msg += ": " + e.Message
			}
			return fmt.Errorf("control plane error (%d): %s", resp.StatusCode, msg)
		}
		return fmt.Errorf("control plane error (%d): %s", resp.StatusCode, strings.TrimSpace(string(raw)))
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
