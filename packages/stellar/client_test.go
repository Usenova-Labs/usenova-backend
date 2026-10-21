package stellar

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// validAccount is a syntactically valid (unfunded-looking) Stellar address used
// across tests: 56 chars, starts with G, base32 alphabet only.
const validAccount = "GBRPYHIL2CI3FNQ4BXLFMNDLFJUNPU2HY3ZMFSHONUCEOASW7QC7OX2H"

func TestConfigFromEnvDefaults(t *testing.T) {
	for _, k := range []string{"STELLAR_NETWORK", "STELLAR_HORIZON_URL", "STELLAR_RPC_URL", "PAYMENT_ESCROW_CONTRACT_ID"} {
		t.Setenv(k, "")
	}

	cfg := ConfigFromEnv()
	if cfg.Network != NetworkTestnet {
		t.Errorf("network = %q, want testnet", cfg.Network)
	}
	if cfg.HorizonURL != "https://horizon-testnet.stellar.org" {
		t.Errorf("horizon = %q", cfg.HorizonURL)
	}
	if cfg.RPCURL != "https://soroban-testnet.stellar.org" {
		t.Errorf("rpc = %q", cfg.RPCURL)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

func TestConfigFromEnvOverrides(t *testing.T) {
	t.Setenv("STELLAR_NETWORK", "mainnet")
	t.Setenv("STELLAR_HORIZON_URL", "https://horizon.example.org/")
	t.Setenv("STELLAR_RPC_URL", "https://rpc.example.org/")
	t.Setenv("PAYMENT_ESCROW_CONTRACT_ID", "CABC")

	cfg := ConfigFromEnv()
	if cfg.Network != NetworkMainnet {
		t.Errorf("network = %q, want mainnet", cfg.Network)
	}
	if cfg.HorizonURL != "https://horizon.example.org" {
		t.Errorf("horizon = %q (trailing slash not trimmed)", cfg.HorizonURL)
	}
	if cfg.RPCURL != "https://rpc.example.org" {
		t.Errorf("rpc = %q", cfg.RPCURL)
	}
	if cfg.ContractID != "CABC" {
		t.Errorf("contract = %q", cfg.ContractID)
	}
}

func TestNormalizeNetwork(t *testing.T) {
	cases := map[string]Network{
		"":          NetworkTestnet,
		"testnet":   NetworkTestnet,
		"mainnet":   NetworkMainnet,
		"public":    NetworkMainnet,
		"pubnet":    NetworkMainnet,
		"futurenet": NetworkFuturenet,
		"banana":    NetworkTestnet,
	}
	for in, want := range cases {
		if got := NormalizeNetwork(in); got != want {
			t.Errorf("NormalizeNetwork(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClientAccessors(t *testing.T) {
	c := NewClient(Config{
		Network:    NetworkMainnet,
		HorizonURL: "https://horizon.example.org/",
		RPCURL:     "https://rpc.example.org/",
		ContractID: "CABC",
	})
	if c.Network() != NetworkMainnet {
		t.Errorf("Network = %q", c.Network())
	}
	if c.HorizonURL() != "https://horizon.example.org" {
		t.Errorf("HorizonURL = %q (trailing slash not trimmed)", c.HorizonURL())
	}
	if c.RPCURL() != "https://rpc.example.org" {
		t.Errorf("RPCURL = %q", c.RPCURL())
	}
	if c.ContractID() != "CABC" {
		t.Errorf("ContractID = %q", c.ContractID())
	}
}

func TestClientDerivesDefaults(t *testing.T) {
	c := NewClient(Config{})
	if c.Network() != NetworkTestnet {
		t.Errorf("default Network = %q, want testnet", c.Network())
	}
	if c.HorizonURL() != "https://horizon-testnet.stellar.org" {
		t.Errorf("default HorizonURL = %q", c.HorizonURL())
	}
	if c.RPCURL() != "https://soroban-testnet.stellar.org" {
		t.Errorf("default RPCURL = %q", c.RPCURL())
	}
}

func TestValidateAccountID(t *testing.T) {
	tests := []struct {
		name    string
		addr    string
		wantErr bool
	}{
		{"valid", validAccount, false},
		{"empty", "", true},
		{"too short", "GABC", true},
		{"wrong prefix", "SBRPYHIL2CI3FNQ4BXLFMNDLFJUNPU2HY3ZMFSHONUCEOASW7QC7OX2H", true},
		{"lowercase", "gbrpyhil2ci3fnq4bxlfmndlfjunpu2hy3zmfshonuceoasw7qc7ox2h", true},
		{"invalid char", "GBRPYHIL2CI3FNQ4BXLFMNDLFJUNPU2HY3ZMFSHONUCEOASW7QC7OX" + "0", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateAccountID(tt.addr)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateAccountID(%q) err = %v, wantErr %v", tt.addr, err, tt.wantErr)
			}
		})
	}
}

func newHorizonServer(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return NewClient(Config{
		Network:    NetworkTestnet,
		HorizonURL: srv.URL,
		RPCURL:     srv.URL,
	})
}

func TestGetAccountBalances(t *testing.T) {
	client := newHorizonServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/accounts/"+validAccount {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"account_id": validAccount,
			"balances": []map[string]any{
				{"balance": "100.0000000", "asset_type": "native"},
				{"balance": "25.5000000", "asset_type": "credit_alphanum4", "asset_code": "USDC", "asset_issuer": "GISSUER"},
			},
		})
	})

	acct, err := client.GetAccount(context.Background(), validAccount)
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	if len(acct.Balances) != 2 {
		t.Fatalf("balances = %d, want 2", len(acct.Balances))
	}
	if acct.Balances[0].Asset != "XLM" || acct.Balances[0].Amount != "100.0000000" {
		t.Errorf("XLM balance = %+v", acct.Balances[0])
	}
	if acct.Balances[1].Asset != "USDC" || acct.Balances[1].Issuer != "GISSUER" {
		t.Errorf("USDC balance = %+v", acct.Balances[1])
	}
}

func TestGetBalanceAssetSelection(t *testing.T) {
	client := newHorizonServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"account_id": validAccount,
			"balances": []map[string]any{
				{"balance": "100.0000000", "asset_type": "native"},
				{"balance": "25.5000000", "asset_type": "credit_alphanum4", "asset_code": "USDC", "asset_issuer": "GISSUER"},
			},
		})
	})

	xlm, err := client.GetBalance(context.Background(), validAccount, "XLM")
	if err != nil {
		t.Fatalf("GetBalance XLM: %v", err)
	}
	if xlm.Amount != "100.0000000" {
		t.Errorf("XLM = %q", xlm.Amount)
	}

	usdc, err := client.GetBalance(context.Background(), validAccount, "usdc")
	if err != nil {
		t.Fatalf("GetBalance USDC: %v", err)
	}
	if usdc.Amount != "25.5000000" {
		t.Errorf("USDC = %q", usdc.Amount)
	}

	// Default asset is XLM.
	def, err := client.GetBalance(context.Background(), validAccount, "")
	if err != nil {
		t.Fatalf("GetBalance default: %v", err)
	}
	if def.Asset != "XLM" {
		t.Errorf("default asset = %q, want XLM", def.Asset)
	}

	// Missing asset returns zero, no error.
	missing, err := client.GetBalance(context.Background(), validAccount, "EURT")
	if err != nil {
		t.Fatalf("GetBalance missing: %v", err)
	}
	if missing.Amount != "0" {
		t.Errorf("missing asset amount = %q, want 0", missing.Amount)
	}
}

func TestGetAccountNotFound(t *testing.T) {
	client := newHorizonServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"status":404}`, http.StatusNotFound)
	})

	_, err := client.GetAccount(context.Background(), validAccount)
	var nf *AccountNotFoundError
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.As(err, &nf) {
		t.Fatalf("err = %v, want *AccountNotFoundError", err)
	}
	if nf.Address != validAccount {
		t.Errorf("address = %q, want %q", nf.Address, validAccount)
	}
}

func TestGetAccountInvalidAddress(t *testing.T) {
	client := newHorizonServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("server should not be called for an invalid address")
	})
	if _, err := client.GetAccount(context.Background(), "nope"); err == nil {
		t.Fatal("expected validation error")
	}
}
