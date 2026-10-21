// Package stellar provides a minimal, dependency-free client for interacting
// with Stellar testnet (and mainnet) Horizon endpoints.
//
// Configuration is environment-driven; no secret keys or account addresses are
// hard-coded. Use ConfigFromEnv to build a Config from the environment:
//
//	STELLAR_NETWORK       testnet | mainnet (default: testnet)
//	STELLAR_HORIZON_URL   Horizon endpoint (default derived from network)
//	STELLAR_RPC_URL       Soroban RPC endpoint (default derived from network)
//
// The client only ever reads public ledger data (balances). Signing keys are
// intentionally never handled here.
package stellar

// Network identifies a Stellar network.
type Network string

const (
	// NetworkTestnet is the Stellar testnet.
	NetworkTestnet Network = "testnet"
	// NetworkMainnet is the Stellar public network.
	NetworkMainnet Network = "mainnet"
	// NetworkFuturenet is the Stellar futurenet.
	NetworkFuturenet Network = "futurenet"
)

// DefaultHorizonURL returns the canonical Horizon endpoint for a network.
func DefaultHorizonURL(n Network) string {
	switch n {
	case NetworkMainnet:
		return "https://horizon.stellar.org"
	case NetworkFuturenet:
		return "https://horizon-futurenet.stellar.org"
	default:
		return "https://horizon-testnet.stellar.org"
	}
}

// DefaultRPCURL returns the canonical Soroban RPC endpoint for a network.
func DefaultRPCURL(n Network) string {
	switch n {
	case NetworkMainnet:
		return "https://mainnet.sorobanrpc.com"
	case NetworkFuturenet:
		return "https://rpc-futurenet.stellar.org"
	default:
		return "https://soroban-testnet.stellar.org"
	}
}

// NormalizeNetwork maps common aliases onto a known Network, defaulting to
// testnet for empty/unknown values.
func NormalizeNetwork(s string) Network {
	switch s {
	case "mainnet", "public", "pubnet":
		return NetworkMainnet
	case "futurenet":
		return NetworkFuturenet
	default:
		return NetworkTestnet
	}
}

// IsTestnet reports whether the network is a non-production network.
func (n Network) IsTestnet() bool {
	return n != NetworkMainnet
}
