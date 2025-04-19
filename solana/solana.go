package solana

import (
	"context"
	"fmt"
	"time"

	// Solana Go SDK packages
	solanago "github.com/gagliardetto/solana-go"

	spltoken "github.com/gagliardetto/solana-go/programs/token"
	"github.com/gagliardetto/solana-go/rpc"
)

// SendUSDC sends a specified amount of USDC to a recipient's main wallet address.
// Configuration (RPC endpoint, mint address, sender key) must be provided as arguments.
//
// NOTE: callers are responsible for confirming the transaction with ConfirmTransaction
// using the signature returned by this function!
//
// Args:
//   - ctx:            Context for cancellation and timeouts.
//   - client:         An initialized Solana RPC client (*rpc.Client).
//   - usdcMintAddress: The public key of the USDC token mint.
//   - senderPrivateKey: The sender's private key.
//   - recipientPublicKey: The recipient's main wallet address (as PublicKey).
//   - amount:       The amount of USDC to send *in its smallest unit* (e.g., for 6 decimals, 1 USDC = 1,000,000 units).
func SendUSDC(
	ctx context.Context,
	client *rpc.Client,
	usdcMintAddress solanago.PublicKey,
	senderPrivateKey solanago.PrivateKey,
	recipientPublicKey solanago.PublicKey,
	amount uint64,
) (solanago.Signature, error) {

	// Standard USDC typically has 6 decimal places.
	// For production, you might want to fetch this dynamically from the mint account info,
	// or pass it as an argument if supporting tokens with different decimals.
	const usdcDecimals = 6

	senderPublicKey := senderPrivateKey.PublicKey()

	// Sender's ATA for USDC
	senderAtaAddress, _, err := solanago.FindAssociatedTokenAddress(
		senderPublicKey, usdcMintAddress,
	)
	if err != nil {
		return solanago.Signature{}, fmt.Errorf("failed to find sender ATA: %w", err)
	}

	// Recipient's ATA for USDC
	recipientAtaAddress, _, err := solanago.FindAssociatedTokenAddress(
		recipientPublicKey, usdcMintAddress,
	)
	if err != nil {
		return solanago.Signature{}, fmt.Errorf("failed to find recipient ATA: %w", err)
	}

	// Optional Check: Does the recipient ATA exist? If not, it needs creation.
	// This basic transfer assumes the recipient ATA already exists or will be
	// created by another mechanism (like the recipient wallet auto-creating it).
	// For robustness, check and potentially add a create ATA instruction.
	_, err = client.GetAccountInfo(ctx, recipientAtaAddress)
	if err != nil {
		if err == rpc.ErrNotFound {
			// Need to add associatedtokenaccount.NewCreateInstruction(...)
			return solanago.Signature{}, fmt.Errorf("recipient ATA %s does not exist and creation is not implemented", recipientAtaAddress)
		}
		return solanago.Signature{}, fmt.Errorf("failed to check recipient ATA %s: %w", recipientAtaAddress, err)
	}

	// Build the Transaction
	recentBlockhashResult, err := client.GetRecentBlockhash(ctx, rpc.CommitmentFinalized)
	if err != nil {
		return solanago.Signature{}, fmt.Errorf("failed to get recent blockhash: %w", err)
	}
	blockhash := recentBlockhashResult.Value.Blockhash

	// Create the SPL Token Transfer instruction (Checked version handles decimals)
	transferInstruction, err := spltoken.NewTransferCheckedInstruction(
		amount,                 // Amount in base units
		usdcDecimals,           // Decimals of the token mint
		senderAtaAddress,       // Source ATA
		usdcMintAddress,        // Token Mint address
		recipientAtaAddress,    // Destination ATA
		senderPublicKey,        // Authority (owner of source ATA)
		[]solanago.PublicKey{}, // Additional signers (none needed here)
	).ValidateAndBuild()
	if err != nil {
		return solanago.Signature{}, fmt.Errorf("failed to build SPL Token transfer instruction: %w", err)
	}

	// Create the transaction
	tx, err := solanago.NewTransaction(
		[]solanago.Instruction{transferInstruction},
		blockhash,
		solanago.TransactionPayer(senderPublicKey), // Sender pays the transaction fee
	)
	if err != nil {
		return solanago.Signature{}, fmt.Errorf("failed to create new transaction: %w", err)
	}

	// Sign the Transaction
	_, err = tx.Sign(
		func(key solanago.PublicKey) *solanago.PrivateKey {
			if senderPublicKey.Equals(key) {
				return &senderPrivateKey // Provide the private key when requested
			}
			return nil
		},
	)
	if err != nil {
		return solanago.Signature{}, fmt.Errorf("failed to sign transaction: %w", err)
	}

	// Send the Transaction
	signature, err := client.SendTransactionWithOpts(ctx, tx, rpc.TransactionOpts{
		SkipPreflight:       false,
		PreflightCommitment: rpc.CommitmentFinalized,
	})
	if err != nil {
		// Consider attempting to parse the RPC error for more specific details
		// e.g., using jsonrpc.RPCError or checking the error string
		return solanago.Signature{}, fmt.Errorf("failed to send transaction: %w", err)
	}

	return signature, nil // Return the signature upon successful submission
}

// ConfirmTransaction waits for a transaction signature to reach a specified commitment level.
// It's separated to allow callers to decide whether/how long to wait.
func ConfirmTransaction(ctx context.Context, client *rpc.Client, sig solanago.Signature, desiredCommitment rpc.CommitmentType) error {
	// Loop until confirmed or timeout/context cancellation
	// Use a reasonable timeout, e.g., 60-90 seconds, managed by the caller via ctx.
	ticker := time.NewTicker(3 * time.Second) // Check every 3 seconds
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled or timed out while waiting for confirmation: %w", ctx.Err())
		case <-ticker.C:
			// Try calling GetSignatureStatuses with a single signature, not a slice
			searchHistory := false // Typically false unless searching old txs
			statusResult, err := client.GetSignatureStatuses(ctx, searchHistory, sig)
			if err != nil {
				// Transient network errors are possible, log or potentially retry slightly?
				// For now, we return the error.
				return fmt.Errorf("failed to get signature status for %s: %w", sig, err)
			}

			if statusResult == nil || len(statusResult.Value) == 0 || statusResult.Value[0] == nil {
				// Signature not yet found or processed, continue waiting
				continue
			}

			status := statusResult.Value[0]
			if status.Err != nil {
				// Transaction failed
				return fmt.Errorf("transaction %s failed: %v", sig, status.Err)
			}

			// Check commitment level
			// Use ConfirmationStatus field for commitment and compare with desired level's string representation
			currentCommitmentStatus := status.ConfirmationStatus
			confirmed := false
			switch desiredCommitment {
			case rpc.CommitmentProcessed:
				confirmed = string(currentCommitmentStatus) == string(rpc.CommitmentProcessed) ||
					string(currentCommitmentStatus) == string(rpc.CommitmentConfirmed) ||
					string(currentCommitmentStatus) == string(rpc.CommitmentFinalized)
			case rpc.CommitmentConfirmed:
				confirmed = string(currentCommitmentStatus) == string(rpc.CommitmentConfirmed) ||
					string(currentCommitmentStatus) == string(rpc.CommitmentFinalized)
			case rpc.CommitmentFinalized:
				confirmed = string(currentCommitmentStatus) == string(rpc.CommitmentFinalized)
			}

			if confirmed {
				return nil // Success! Transaction confirmed to the desired level.
			}
			// Otherwise, still waiting for desired commitment level, continue loop.
		}
	}
}

// Helper function to parse a Base58 private key string.
// Externalized for the same reason as above.
func LoadPrivateKeyFromBase58(keyStr string) (solanago.PrivateKey, error) {
	privateKey, err := solanago.PrivateKeyFromBase58(keyStr)
	if err != nil {
		return solanago.PrivateKey{}, fmt.Errorf("failed to parse base58 private key: %w", err)
	}
	return privateKey, nil
}

// Helper function to parse a Base58 public key string.
func PublicKeyFromBase58(keyStr string) (solanago.PublicKey, error) {
	pubKey, err := solanago.PublicKeyFromBase58(keyStr)
	if err != nil {
		return solanago.PublicKey{}, fmt.Errorf("invalid base58 public key string '%s': %w", keyStr, err)
	}
	return pubKey, nil
}

// Helper function to create an RPC client.
func NewRPCClient(endpoint string) *rpc.Client {
	// Default to Devnet if not specified or empty
	if endpoint == "" {
		endpoint = rpc.DevNet_RPC
	}
	return rpc.New(endpoint)
}

// Helper function to check RPC client health.
func CheckRPCHealth(ctx context.Context, client *rpc.Client) error {
	_, err := client.GetHealth(ctx)
	if err != nil {
		// Attempt to get the endpoint from the client for a better error message
		endpoint := "unknown"
		// Note: rpc.Client doesn't directly expose the endpoint easily after creation.
		// If needed, wrap the client creation or store the endpoint alongside the client.
		return fmt.Errorf("failed to connect to RPC endpoint %s: %w", endpoint, err)
	}
	return nil
}

// == Example Usage (Conceptual) ==
/*
func main() {
	// 1. Load Config (from env, file, secrets manager, etc.)
	rpcEndpoint := os.Getenv("SOLANA_RPC_ENDPOINT") // Or load from config file
	usdcMintStr := os.Getenv("USDC_MINT_ADDRESS")
	senderKeyStr := os.Getenv("SOLANA_SENDER_PRIVATE_KEY") // Or use LoadPrivateKeyFromKeygenFile
	recipientAddrStr := "RecipientWalletAddressHere" // e.g., from API request
	amountToSend := 1.5 // User-friendly amount

	// 2. Initialize Dependencies
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second) // Example timeout
	defer cancel()

	client := solana.NewRPCClient(rpcEndpoint)
	err := solana.CheckRPCHealth(ctx, client)
	if err != nil {
		log.Fatalf("RPC Health Check Failed: %v", err)
	}
	log.Printf("Connected to RPC: %s", rpcEndpoint) // Log endpoint used

	usdcMintAddr, err := solana.PublicKeyFromBase58(usdcMintStr)
	if err != nil {
		log.Fatalf("Invalid USDC Mint Addr: %v", err)
	}

	senderPrivKey, err := solana.LoadPrivateKeyFromBase58(senderKeyStr)
	if err != nil {
		// Try loading from file as fallback if needed
		// senderKeyPath := os.Getenv("SOLANA_SENDER_KEYPAIR_PATH")
		// senderPrivKey, err = solana.LoadPrivateKeyFromKeygenFile(senderKeyPath)
		// if err != nil {
		// 	 log.Fatalf("Failed to load sender key: %v", err)
		// }
		log.Fatalf("Failed to load sender key from Base58: %v", err)
	}

	recipientPubKey, err := solana.PublicKeyFromBase58(recipientAddrStr)
	if err != nil {
		log.Fatalf("Invalid Recipient Addr: %v", err)
	}

	// Convert float amount to smallest unit (micro-USDC)
	usdcAmount, err := solana.NewUSDCAmount(amountToSend) // Assumes amount.go is in the same package or imported
	if err != nil {
		log.Fatalf("Invalid Amount: %v", err)
	}
	amountBaseUnits := usdcAmount.ToSmallestUnit().Uint64() // Use Uint64 for the API

	log.Printf("Sending %f USDC (%d base units) from %s to %s via %s (Mint: %s)",
		amountToSend, amountBaseUnits, senderPrivKey.PublicKey(), recipientPubKey, rpcEndpoint, usdcMintAddr)


	// 3. Call the Refactored Function
	signature, err := solana.SendUSDC(
		ctx,
		client,
		usdcMintAddr,
		senderPrivKey,
		recipientPubKey,
		amountBaseUnits,
	)
	if err != nil {
		log.Fatalf("Failed to send USDC: %v", err)
	}
	log.Printf("Transaction submitted successfully! Signature: %s", signature)

	// 4. Optionally Confirm Transaction
	log.Println("Waiting for confirmation...")
	err = solana.ConfirmTransaction(ctx, client, signature, rpc.CommitmentFinalized)
	if err != nil {
		// Log error but don't necessarily fail hard, tx might confirm later
		log.Printf("Warning: Failed to confirm transaction %s: %v", signature, err)
		log.Println("Check explorer for final status.")
	} else {
		log.Printf("Transaction %s confirmed!", signature)
	}
}
*/
