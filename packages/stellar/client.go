package stellar

import (
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
}

func (e *AccountNotFoundError) Error() string {
	return fmt.Sprintf("stellar: account %s not found", e.Address)
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
		return nil, fmt.Errorf("stellar: horizon request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, &AccountNotFoundError{Address: address}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("stellar: horizon returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
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
