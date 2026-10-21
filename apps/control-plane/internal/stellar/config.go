package stellar

import (
	"fmt"
	"os"
	"strings"

	"github.com/byoi-platform/stellar"
)

// Config holds Stellar-related configuration for the control plane. Every
// value is environment-driven; no account addresses or secret keys are stored
// in the repository.
type Config struct {
	// Stellar is the resolved network client configuration.
	Stellar stellar.Config
	// DefaultAsset is the asset code used when a balance query omits one
	// (defaults to XLM).
	DefaultAsset string
}

// Load reads Stellar configuration from the environment. It never fails on
// missing optional variables: the network defaults to testnet and endpoints
// are derived. Returning an error keeps the signature consistent with other
// config loaders and allows future validation.
func Load() (Config, error) {
	cfg := Config{
		Stellar:      stellar.ConfigFromEnv(),
		DefaultAsset: strings.ToUpper(strings.TrimSpace(os.Getenv("STELLAR_DEFAULT_ASSET"))),
	}
	if cfg.DefaultAsset == "" {
		cfg.DefaultAsset = "XLM"
	}
	if err := cfg.Stellar.Validate(); err != nil {
		return Config{}, fmt.Errorf("stellar config: %w", err)
	}
	return cfg, nil
}

// String returns a log-safe summary (no secret material).
func (c Config) String() string {
	return fmt.Sprintf("Config{Stellar=%s, DefaultAsset=%s}", c.Stellar, c.DefaultAsset)
}
