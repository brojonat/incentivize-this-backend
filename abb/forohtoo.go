package abb

import (
	"os"
	"strings"
)

// DetermineForohtooNetwork infers which Solana network forohtoo should target.
// Manual override via FOROHTOO_NETWORK takes precedence, otherwise we inspect the RPC endpoint.
func DetermineForohtooNetwork(rpcEndpoint string) string {
	if envNetwork := strings.TrimSpace(os.Getenv(EnvForohtooNetwork)); envNetwork != "" {
		return strings.ToLower(envNetwork)
	}

	endpoint := strings.ToLower(rpcEndpoint)
	switch {
	case strings.Contains(endpoint, "devnet"):
		return "devnet"
	case strings.Contains(endpoint, "testnet"):
		return "testnet"
	default:
		return "mainnet"
	}
}
