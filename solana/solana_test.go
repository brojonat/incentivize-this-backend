package solana

import (
	"context"
	"os"
	"testing"
	"time"

	solanago "github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Unit Tests ---

func TestLoadPrivateKeyFromBase58(t *testing.T) {
	// Generate a new keypair for testing
	wallet := solanago.NewWallet()
	privKey := wallet.PrivateKey
	pubKey := wallet.PublicKey()
	privKeyStr := privKey.String()

	loadedPrivKey, err := LoadPrivateKeyFromBase58(privKeyStr)
	require.NoError(t, err, "LoadPrivateKeyFromBase58 should not return an error for a valid key")
	assert.Equal(t, privKey, loadedPrivKey, "Loaded private key should match the original")
	assert.Equal(t, pubKey, loadedPrivKey.PublicKey(), "Public key derived from loaded private key should match original")

	_, err = LoadPrivateKeyFromBase58("invalid-base58-string")
	assert.Error(t, err, "LoadPrivateKeyFromBase58 should return an error for invalid input")
}

func TestPublicKeyFromBase58(t *testing.T) {
	// Generate a new keypair for testing
	wallet := solanago.NewWallet()
	pubKey := wallet.PublicKey()
	pubKeyStr := pubKey.String()

	loadedPubKey, err := PublicKeyFromBase58(pubKeyStr)
	require.NoError(t, err, "PublicKeyFromBase58 should not return an error for a valid key")
	assert.Equal(t, pubKey, loadedPubKey, "Loaded public key should match the original")

	_, err = PublicKeyFromBase58("invalid-base58-string")
	assert.Error(t, err, "PublicKeyFromBase58 should return an error for invalid input")
}

func TestNewRPCClient(t *testing.T) {
	// Test default endpoint (Devnet)
	clientDefault := NewRPCClient("")
	require.NotNil(t, clientDefault, "Client should not be nil when endpoint is empty")
	// We can't easily assert the *exact* endpoint string after creation,
	// but we can check if it connects (implicitly tests endpoint validity)
	err := CheckRPCHealth(context.Background(), clientDefault)
	// Skip test if Devnet is unavailable (network conditions may vary)
	if err != nil {
		t.Skipf("Skipping test: Devnet RPC is unavailable: %v", err)
	}

	// Test custom endpoint
	customEndpoint := rpc.TestNet_RPC // Use Testnet for variety
	clientCustom := NewRPCClient(customEndpoint)
	require.NotNil(t, clientCustom, "Client should not be nil when endpoint is specified")
	err = CheckRPCHealth(context.Background(), clientCustom)
	// Skip test if Testnet is unavailable (network conditions may vary)
	if err != nil {
		t.Skipf("Skipping test: Testnet RPC is unavailable: %v", err)
	}
}

// --- Integration Test ---

// TestIntegrationSendAndConfirmUSDC performs an end-to-end test of sending USDC on Devnet.
// It requires the following environment variables to be set:
// - SOLANA_RPC_ENDPOINT: URL for the Solana RPC endpoint (e.g., Devnet)
// - USDC_MINT_ADDRESS_DEVNET: Public key of the USDC mint on Devnet
// - TEST_SENDER_KEY_BASE58: Base58 encoded private key of the sender wallet (must have SOL and Devnet USDC)
// - TEST_RECIPIENT_ADDR_BASE58: Base58 encoded public key of the recipient wallet
func TestIntegrationSendAndConfirmUSDC(t *testing.T) {
	rpcEndpoint := os.Getenv("SOLANA_RPC_ENDPOINT")
	usdcMintStr := os.Getenv("USDC_MINT_ADDRESS_DEVNET")
	senderKeyStr := os.Getenv("TEST_SENDER_KEY_BASE58")
	recipientAddrStr := os.Getenv("TEST_RECIPIENT_ADDR_BASE58")

	if rpcEndpoint == "" || usdcMintStr == "" || senderKeyStr == "" || recipientAddrStr == "" {
		t.Skip("Skipping integration test: Required environment variables not set (SOLANA_RPC_ENDPOINT, USDC_MINT_ADDRESS_DEVNET, TEST_SENDER_KEY_BASE58, TEST_RECIPIENT_ADDR_BASE58)")
	}

	// 1. Initialize Dependencies
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second) // Generous timeout for network operations
	defer cancel()

	client := NewRPCClient(rpcEndpoint)
	err := CheckRPCHealth(ctx, client)
	require.NoError(t, err, "RPC Health Check Failed")
	t.Logf("Connected to RPC: %s", rpcEndpoint)

	usdcMintAddr, err := PublicKeyFromBase58(usdcMintStr)
	require.NoError(t, err, "Invalid USDC Mint Addr")

	senderPrivKey, err := LoadPrivateKeyFromBase58(senderKeyStr)
	require.NoError(t, err, "Failed to load sender key from Base58")
	senderPubKey := senderPrivKey.PublicKey()
	t.Logf("Sender public key: %s", senderPubKey)

	recipientPubKey, err := PublicKeyFromBase58(recipientAddrStr)
	require.NoError(t, err, "Invalid Recipient Addr")
	t.Logf("Recipient public key: %s", recipientPubKey)

	// Define amount to send (e.g., 0.001 USDC)
	amountToSend := 0.001
	usdcAmount, err := NewUSDCAmount(amountToSend)
	require.NoError(t, err, "Invalid Amount")
	amountBaseUnits := usdcAmount.ToSmallestUnit().Uint64()

	t.Logf("Attempting to send %f USDC (%d base units) from %s to %s...",
		amountToSend, amountBaseUnits, senderPubKey, recipientPubKey)

	// 2. Call SendUSDC
	signature, err := SendUSDC(
		ctx,
		client,
		usdcMintAddr,
		senderPrivKey,
		recipientPubKey,
		amountBaseUnits,
	)

	// Handle potential error during send (e.g., insufficient funds, ATA doesn't exist yet)
	// Note: This basic test assumes the recipient ATA already exists.
	// A more robust test might check/create the ATA first.
	require.NoError(t, err, "SendUSDC failed")
	require.NotEqual(t, solanago.Signature{}, signature, "SendUSDC returned an empty signature")
	t.Logf("Transaction submitted successfully! Signature: %s", signature)

	// 3. Call ConfirmTransaction
	t.Logf("Waiting for confirmation for signature: %s...", signature)
	confirmCtx, confirmCancel := context.WithTimeout(context.Background(), 90*time.Second) // Separate timeout for confirmation
	defer confirmCancel()

	err = ConfirmTransaction(confirmCtx, client, signature, rpc.CommitmentConfirmed) // Use Confirmed for faster test confirmation vs Finalized
	require.NoError(t, err, "ConfirmTransaction failed")

	t.Logf("Transaction %s confirmed successfully!", signature)

	// Optional: Add checks for balance changes if desired (more complex)
	// e.g., GetTokenAccountBalance before and after send/confirm
}
