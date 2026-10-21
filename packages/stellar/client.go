package stellar

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client handles interactions with Stellar network and Soroban contracts
type Client struct {
	network    string
	contractID string
	rpcURL     string
	httpClient *http.Client
}

// NewClient creates a new Stellar client targeting the given network and
// Soroban contract. The RPC endpoint is derived from the network name unless
// overridden with WithRPCURL.
func NewClient(network, contractID string) *Client {
	return &Client{
		network:    network,
		contractID: contractID,
		rpcURL:     defaultRPCURL(network),
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// WithRPCURL overrides the Soroban RPC endpoint used by the client.
func (c *Client) WithRPCURL(url string) *Client {
	c.rpcURL = url
	return c
}

// WithHTTPClient overrides the underlying HTTP client.
func (c *Client) WithHTTPClient(hc *http.Client) *Client {
	c.httpClient = hc
	return c
}

// ContractID returns the configured escrow contract id.
func (c *Client) ContractID() string {
	return c.contractID
}

// Network returns the configured network name.
func (c *Client) Network() string {
	return c.network
}

func defaultRPCURL(network string) string {
	switch network {
	case "mainnet", "public":
		return "https://mainnet.sorobanrpc.com"
	case "futurenet":
		return "https://rpc-futurenet.stellar.org"
	default:
		return "https://soroban-testnet.stellar.org"
	}
}

// rpcRequest is a JSON-RPC 2.0 request envelope.
type rpcRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

// rpcResponse is a JSON-RPC 2.0 response envelope.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    string `json:"data"`
}

func (e *rpcError) Error() string {
	if e.Data != "" {
		return fmt.Sprintf("soroban rpc error %d: %s: %s", e.Code, e.Message, e.Data)
	}
	return fmt.Sprintf("soroban rpc error %d: %s", e.Code, e.Message)
}

// call performs a single JSON-RPC call against the Soroban RPC endpoint.
func (c *Client) call(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	if c.rpcURL == "" {
		return nil, fmt.Errorf("stellar: no rpc url configured")
	}

	body, err := json.Marshal(rpcRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return nil, fmt.Errorf("stellar: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.rpcURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("stellar: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("stellar: rpc call: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("stellar: read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("stellar: rpc http %d: %s", resp.StatusCode, string(raw))
	}

	var envelope rpcResponse
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("stellar: decode response: %w", err)
	}
	if envelope.Error != nil {
		return nil, envelope.Error
	}
	return envelope.Result, nil
}

// ProcessPayment processes a payment through the Stellar network by invoking
// the escrow contract's create_escrow entrypoint. See Client.CreateEscrow for
// a fully typed helper.
func (c *Client) ProcessPayment(ctx context.Context, amount int64, recipient string) error {
	_, err := c.CreateEscrow(ctx, EscrowRequest{
		Provider: recipient,
		Amount:   amount,
	})
	return err
}

// GetBalance retrieves the native balance for an account from Soroban RPC.
func (c *Client) GetBalance(ctx context.Context, address string) (int64, error) {
	result, err := c.call(ctx, "getAccount", map[string]interface{}{"accountId": address})
	if err != nil {
		return 0, err
	}

	var account struct {
		Balances []struct {
			Balance string `json:"balance"`
		} `json:"balances"`
	}
	if err := json.Unmarshal(result, &account); err != nil {
		return 0, fmt.Errorf("stellar: decode account: %w", err)
	}
	if len(account.Balances) == 0 {
		return 0, nil
	}
	var stroops int64
	if _, err := fmt.Sscanf(account.Balances[0].Balance, "%d", &stroops); err != nil {
		return 0, fmt.Errorf("stellar: parse balance: %w", err)
	}
	return stroops, nil
}

// InvokeContract invokes a Soroban smart contract function via simulateTransaction.
func (c *Client) InvokeContract(ctx context.Context, function string, args []interface{}) (interface{}, error) {
	if c.contractID == "" {
		return nil, fmt.Errorf("stellar: contract id not configured")
	}

	params := map[string]interface{}{
		"function":   function,
		"contractId": c.contractID,
		"args":       args,
	}

	result, err := c.call(ctx, "simulateTransaction", params)
	if err != nil {
		return nil, err
	}

	var out interface{}
	if err := json.Unmarshal(result, &out); err != nil {
		return nil, fmt.Errorf("stellar: decode simulation: %w", err)
	}
	return out, nil
}
