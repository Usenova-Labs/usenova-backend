package stellar

import "testing"

func TestLoadDefaults(t *testing.T) {
	for _, k := range []string{"STELLAR_NETWORK", "STELLAR_HORIZON_URL", "STELLAR_RPC_URL", "PAYMENT_ESCROW_CONTRACT_ID", "STELLAR_DEFAULT_ASSET"} {
		t.Setenv(k, "")
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Stellar.Network != "testnet" {
		t.Errorf("network = %q, want testnet", cfg.Stellar.Network)
	}
	if cfg.DefaultAsset != "XLM" {
		t.Errorf("default asset = %q, want XLM", cfg.DefaultAsset)
	}
}

func TestLoadFromEnv(t *testing.T) {
	t.Setenv("STELLAR_NETWORK", "mainnet")
	t.Setenv("STELLAR_DEFAULT_ASSET", "usdc")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Stellar.Network != "mainnet" {
		t.Errorf("network = %q, want mainnet", cfg.Stellar.Network)
	}
	if cfg.DefaultAsset != "USDC" {
		t.Errorf("default asset = %q, want USDC", cfg.DefaultAsset)
	}
}

func TestStringRedactsNothingSensitive(t *testing.T) {
	t.Setenv("STELLAR_NETWORK", "testnet")
	cfg, _ := Load()
	if cfg.String() == "" {
		t.Error("String must be non-empty")
	}
}
