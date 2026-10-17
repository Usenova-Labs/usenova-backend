package client

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

type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

func New(baseURL, token string) *Client {
	if baseURL == "" {
		baseURL = "http://127.0.0.1:9090"
	}
	return &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		token:   token,
		http: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

func (c *Client) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
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
	return c.http.Do(req)
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

func (c *Client) GetService(ctx context.Context, name string) (any, error) {
	resp, err := c.do(ctx, http.MethodGet, "/services/"+name, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get service failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var out any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
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
	resp, err := c.do(ctx, http.MethodPost, "/services/"+name+"/stop", nil)
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

func (c *Client) Remove(ctx context.Context, name string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/services/"+name, nil)
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
