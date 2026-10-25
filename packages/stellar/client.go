package stellar

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Config holds the connection settings for a Stellar network. All fields are
// derived from the environment; never from committed secrets.
type Config struct {
	// Network is the resolved network (testnet by default).
	Network Network
	// HorizonURL is the Horizon endpoint used for account/balance queries.
	HorizonURL string
	// RPCURL is the Soroban RPC endpoint (used by escrow helpers).
	RPCURL string
	// ContractID is the optional payment-escrow contract id.
	ContractID string
}

// ConfigFromEnv reads configuration from the environment. It is safe to call
// with no variables set: network defaults to testnet and URLs are derived.
func ConfigFromEnv() Config {
	network := NormalizeNetwork(strings.ToLower(strings.TrimSpace(os.Getenv("STELLAR_NETWORK"))))

	horizon := strings.TrimSpace(os.Getenv("STELLAR_HORIZON_URL"))
	if horizon == "" {
		horizon = DefaultHorizonURL(network)
	}

	rpc := strings.TrimSpace(os.Getenv("STELLAR_RPC_URL"))
	if rpc == "" {
		rpc = DefaultRPCURL(network)
	}

	return Config{
		Network:    network,
		HorizonURL: strings.TrimRight(horizon, "/"),
		RPCURL:     strings.TrimRight(rpc, "/"),
		ContractID: strings.TrimSpace(os.Getenv("PAYMENT_ESCROW_CONTRACT_ID")),
	}
}

// Validate ensures the configuration is internally consistent.
func (c Config) Validate() error {
	if c.Network == "" {
		return fmt.Errorf("stellar: network is required")
	}
	if c.HorizonURL == "" {
		return fmt.Errorf("stellar: horizon url is required")
	}
	return nil
}

// String returns a log-safe summary (contains no secret material).
func (c Config) String() string {
	contract := c.ContractID
	if contract == "" {
		contract = "unset"
	}
	return fmt.Sprintf("Config{Network=%s, HorizonURL=%s, RPCURL=%s, ContractID=%s}",
		c.Network, c.HorizonURL, c.RPCURL, contract)
}

// Client handles interactions with a Stellar network's Horizon API.
type Client struct {
	network    Network
	horizonURL string
	rpcURL     string
	contractID string
	httpClient *http.Client
}

// NewClient builds a client from a Config. Use ConfigFromEnv to source the
// configuration from environment variables.
func NewClient(cfg Config) *Client {
	network := cfg.Network
	if network == "" {
		network = NetworkTestnet
	}
	horizon := cfg.HorizonURL
	if horizon == "" {
		horizon = DefaultHorizonURL(network)
	}
	rpc := cfg.RPCURL
	if rpc == "" {
		rpc = DefaultRPCURL(network)
	}
	return &Client{
		network:    network,
		horizonURL: strings.TrimRight(horizon, "/"),
		rpcURL:     strings.TrimRight(rpc, "/"),
		contractID: cfg.ContractID,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

// Network returns the resolved network.
func (c *Client) Network() Network { return c.network }

// HorizonURL returns the configured Horizon endpoint.
func (c *Client) HorizonURL() string { return c.horizonURL }

// RPCURL returns the configured Soroban RPC endpoint.
func (c *Client) RPCURL() string { return c.rpcURL }

// ContractID returns the configured escrow contract id (may be empty).
func (c *Client) ContractID() string { return c.contractID }

// WithRPCURL overrides the Soroban RPC endpoint used by the client.
func (c *Client) WithRPCURL(url string) *Client {
	c.rpcURL = url
	return c
}

// WithHTTPClient overrides the underlying HTTP client (used in tests).
func (c *Client) WithHTTPClient(hc *http.Client) *Client {
	c.httpClient = hc
	return c
}

// accountResponse is the subset of the Horizon account payload we consume.
type accountResponse struct {
	Balances []struct {
		Balance     string `json:"balance"`
		AssetType   string `json:"asset_type"`
		AssetCode   string `json:"asset_code"`
		AssetIssuer string `json:"asset_issuer"`
	} `json:"balances"`
}

// Account represents a linked Stellar account with its balances.
type Account struct {
	Address  string    `json:"address"`
	Network  Network   `json:"network"`
	Balances []Balance `json:"balances"`
}

// Balance is a single asset balance for an account.
type Balance struct {
	// Asset is "XLM" for the native asset, otherwise the asset code (e.g. USDC).
	Asset string `json:"asset"`
	// Issuer is empty for XLM.
	Issuer string `json:"issuer,omitempty"`
	// Amount is the human-readable amount as reported by Horizon (7dp strings
	// preserved exactly; no float rounding).
	Amount string `json:"amount"`
}

// AccountNotFoundError indicates Horizon returned 404 for an address.
type AccountNotFoundError struct {
	Address string
	// Network is the network the lookup ran against (e.g. testnet).
	Network Network
}

func (e *AccountNotFoundError) Error() string {
	return fmt.Sprintf("stellar: account %s not found", e.Address)
}

// HorizonUnreachableError indicates the Horizon endpoint could not be reached,
// for example because of a connection refusal, DNS failure, or timeout.
type HorizonUnreachableError struct {
	// URL is the Horizon endpoint that was queried.
	URL string
	// Err is the underlying transport error.
	Err error
}

func (e *HorizonUnreachableError) Error() string {
	if e.URL == "" {
		return fmt.Sprintf("stellar: horizon request: %v", e.Err)
	}
	return fmt.Sprintf("stellar: horizon request to %s: %v", e.URL, e.Err)
}

// Unwrap exposes the underlying transport error for errors.Is/errors.As.
func (e *HorizonUnreachableError) Unwrap() error { return e.Err }

// HorizonStatusError indicates Horizon returned a non-2xx HTTP status. The
// StatusCode lets callers distinguish a missing account (404) from a server
// failure (5xx).
type HorizonStatusError struct {
	// URL is the Horizon endpoint that was queried.
	URL string
	// StatusCode is the HTTP status returned by Horizon.
	StatusCode int
	// Body is a truncated copy of the response body, if any.
	Body string
}

func (e *HorizonStatusError) Error() string {
	return fmt.Sprintf("stellar: horizon returned %d: %s", e.StatusCode, e.Body)
}

// GetAccount fetches an account and all of its balances from Horizon.
func (c *Client) GetAccount(ctx context.Context, address string) (*Account, error) {
	if err := ValidateAccountID(address); err != nil {
		return nil, err
	}

	endpoint := c.horizonURL + "/accounts/" + address
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("stellar: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, &HorizonUnreachableError{URL: c.horizonURL, Err: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, &AccountNotFoundError{Address: address, Network: c.network}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, &HorizonStatusError{
			URL:        c.horizonURL,
			StatusCode: resp.StatusCode,
			Body:       strings.TrimSpace(string(body)),
		}
	}

	var acct accountResponse
	if err := json.NewDecoder(resp.Body).Decode(&acct); err != nil {
		return nil, fmt.Errorf("stellar: decode account: %w", err)
	}

	out := &Account{
		Address:  address,
		Network:  c.network,
		Balances: make([]Balance, 0, len(acct.Balances)),
	}
	for _, b := range acct.Balances {
		bal := Balance{Amount: b.Balance}
		if b.AssetType == "native" {
			bal.Asset = "XLM"
		} else {
			bal.Asset = b.AssetCode
			bal.Issuer = b.AssetIssuer
		}
		out.Balances = append(out.Balances, bal)
	}
	return out, nil
}

// GetBalance returns the balance for a single asset ("XLM" for native, or an
// asset code such as "USDC"). It returns a zero balance (without error) when
// the account exists but holds none of the asset. An unknown account yields
// *AccountNotFoundError.
func (c *Client) GetBalance(ctx context.Context, address, asset string) (Balance, error) {
	acct, err := c.GetAccount(ctx, address)
	if err != nil {
		return Balance{}, err
	}
	want := strings.ToUpper(strings.TrimSpace(asset))
	if want == "" {
		want = "XLM"
	}
	for _, b := range acct.Balances {
		if strings.EqualFold(b.Asset, want) {
			return b, nil
		}
	}
	return Balance{Asset: want, Amount: "0"}, nil
}

// ValidateAccountID performs a lightweight structural check on a Stellar
// account id (ed25519 public key, StrKey "G...", 56 chars). It does not verify
// the checksum to avoid pulling in a StrKey dependency; Horizon remains the
// authority.
func ValidateAccountID(address string) error {
	a := strings.TrimSpace(address)
	if a == "" {
		return fmt.Errorf("stellar: account address is required")
	}
	if len(a) != 56 {
		return fmt.Errorf("stellar: invalid account address %q: expected 56 characters", a)
	}
	if a[0] != 'G' {
		return fmt.Errorf("stellar: invalid account address %q: expected to start with 'G'", a)
	}
	for _, r := range a {
		if !(r >= 'A' && r <= 'Z') && !(r >= '2' && r <= '7') {
			return fmt.Errorf("stellar: invalid account address %q: contains non-base32 characters", a)
		}
	}
	return nil
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
