package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	abbhttp "github.com/brojonat/affiliate-bounty-board/http"
	"github.com/brojonat/affiliate-bounty-board/http/api"
	solanautil "github.com/brojonat/affiliate-bounty-board/solana"
	solanago "github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/urfave/cli/v2"
)

var (
	EnvServerSecretKey  = "SERVER_SECRET_KEY"
	EnvServerEndpoint   = "SERVER_ENDPOINT"
	EnvAuthToken        = "AUTH_TOKEN"
	EnvTestOwnerWallet  = "SOLANA_TEST_OWNER_WALLET"
	EnvTestFunderWallet = "SOLANA_TEST_FUNDER_WALLET"

	// New Solana related env vars for CLI utils
	EnvSolanaRPCEndpoint     = "SOLANA_RPC_ENDPOINT"
	EnvSolanaUSDCMintAddress = "SOLANA_USDC_MINT_ADDRESS"
	EnvSolanaEscrowWallet    = "SOLANA_ESCROW_WALLET"
	EnvTestFunderPrivateKey  = "SOLANA_TEST_FUNDER_PRIVATE_KEY"
)

func getAuthToken(ctx *cli.Context) error {
	r, err := http.NewRequest(
		http.MethodPost,
		ctx.String("endpoint")+"/token",
		nil,
	)
	if err != nil {
		return err
	}
	r.SetBasicAuth(ctx.String("email"), ctx.String("secret-key"))
	res, err := http.DefaultClient.Do(r)
	if err != nil {
		return fmt.Errorf("could not do server request: %w", err)
	}
	defer res.Body.Close()

	// Read the body once
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return fmt.Errorf("could not read response body: %w", err)
	}

	var resp api.DefaultJSONResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Errorf("could not decode response: %w", err)
	}

	if resp.Error != "" {
		return fmt.Errorf("server error: %s", resp.Error)
	}

	// Handle env file update if specified
	envFile := ctx.String("env-file")
	if envFile != "" {
		content, err := os.ReadFile(envFile)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to read .env file: %w", err)
		}

		lines := strings.Split(string(content), "\n")
		found := false
		for i, line := range lines {
			if strings.HasPrefix(line, "AUTH_TOKEN=") {
				lines[i] = fmt.Sprintf("AUTH_TOKEN=%s", resp.Message)
				found = true
				break
			}
		}
		if !found {
			lines = append(lines, fmt.Sprintf("AUTH_TOKEN=%s", resp.Message))
		}

		if err := os.WriteFile(envFile, []byte(strings.Join(lines, "\n")), 0644); err != nil {
			return fmt.Errorf("failed to write .env file: %w", err)
		}
		fmt.Printf("Bearer token written to %s\n", envFile)
	}

	// Replace the body and call printServerResponse
	res.Body = io.NopCloser(bytes.NewReader(body))
	return printServerResponse(res)
}

func createBounty(ctx *cli.Context) error {
	bountyOwnerWallet := ctx.String("bounty-owner-wallet")
	bountyFunderWallet := ctx.String("bounty-funder-wallet")

	// Validate bounty-owner-wallet is a valid Base58 address
	if _, err := solanago.PublicKeyFromBase58(bountyOwnerWallet); err != nil {
		return fmt.Errorf("invalid --bounty-owner-wallet: %w", err)
	}

	// Validate bounty-funder-wallet is provided and valid
	if bountyFunderWallet == "" {
		return fmt.Errorf("must provide --bounty-funder-wallet")
	}
	if _, err := solanago.PublicKeyFromBase58(bountyFunderWallet); err != nil {
		return fmt.Errorf("invalid --bounty-funder-wallet: %w", err)
	}

	// Create a map for the request to avoid type conversion issues
	paymentTimeoutDuration := ctx.Duration("payment-timeout")
	req := map[string]interface{}{
		"requirements":         ctx.StringSlice("requirements"),
		"bounty_per_post":      ctx.Float64("per-post"),
		"total_bounty":         ctx.Float64("total"),
		"bounty_owner_wallet":  bountyOwnerWallet,
		"bounty_funder_wallet": bountyFunderWallet,
		"platform_type":        ctx.String("platform"),
		"payment_timeout":      int(paymentTimeoutDuration.Seconds()),
	}

	// Marshal to JSON
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create and send the HTTP request
	httpReq, err := http.NewRequest(
		http.MethodPost,
		ctx.String("endpoint")+"/bounties",
		bytes.NewBuffer(body),
	)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", ctx.String("token")))

	// Execute the request
	res, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}

	return printServerResponse(res)
}

func payBounty(ctx *cli.Context) error {
	// Create the request
	req := abbhttp.PayBountyRequest{
		UserID:      ctx.String("user-id"),
		Amount:      ctx.Float64("amount"),
		ToAccount:   ctx.String("to-account"),
		FromAccount: ctx.String("from-account"),
	}

	// Marshal to JSON
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create and send the HTTP request
	httpReq, err := http.NewRequest(
		http.MethodPost,
		ctx.String("endpoint")+"/bounties/pay",
		bytes.NewBuffer(body),
	)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+ctx.String("token"))

	// Execute the request
	res, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}

	return printServerResponse(res)
}

func assessContent(ctx *cli.Context) error {
	// Create a map for the request to avoid type conversion issues
	req := map[string]interface{}{
		"bounty_id":  ctx.String("bounty-id"),
		"content_id": ctx.String("content-id"),
		"user_id":    ctx.String("user-id"),
		"platform":   ctx.String("platform"),
	}

	// Marshal to JSON
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create and send the HTTP request
	httpReq, err := http.NewRequest(
		http.MethodPost,
		ctx.String("endpoint")+"/assess",
		bytes.NewBuffer(body),
	)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+ctx.String("token"))

	// Execute the request
	res, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}

	return printServerResponse(res)
}

func listBounties(ctx *cli.Context) error {
	// Create and send the HTTP request
	httpReq, err := http.NewRequest(
		http.MethodGet,
		ctx.String("endpoint")+"/bounties",
		nil,
	)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	httpReq.Header.Set("Authorization", "Bearer "+ctx.String("token"))

	// Execute the request
	res, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}

	return printServerResponse(res)
}

// getDefaultKeypairPath returns the default path to the Solana keypair, expanding the tilde.
// Returns an empty string if the home directory cannot be determined.
func getDefaultKeypairPath() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		// Log or handle the error appropriately if needed,
		// but for a default value, returning empty might be acceptable.
		return ""
	}
	return filepath.Join(homeDir, ".config", "solana", "id.json")
}

// Action function for the print-private-key command
func printPrivateKeyAction(ctx *cli.Context) error {
	keypairPath := ctx.String("keypair-path")
	if keypairPath == "" {
		// This should ideally be caught by Required: true, but double-check
		return fmt.Errorf("missing required flag: --keypair-path")
	}

	privateKey, err := solanago.PrivateKeyFromSolanaKeygenFile(keypairPath)
	if err != nil {
		return fmt.Errorf("error loading keypair file '%s': %w", keypairPath, err)
	}

	// Print the base58 encoded private key string to standard output
	fmt.Println(privateKey.String())
	return nil
}

// Action function for the fund-escrow command
func fundEscrowAction(ctx *cli.Context) error {
	fmt.Println("Initiating escrow funding...")

	// --- Get Flag Values ---
	amount := ctx.Float64("amount")
	funderSecretKey := ctx.String("from-secret")
	recipientAddrStr := ctx.String("to")
	rpcEndpoint := ctx.String("rpc-endpoint")
	usdcMintAddrStr := ctx.String("usdc-mint")
	workflowID := ctx.String("workflow-id")

	// --- Validate Inputs & Convert ---
	if amount <= 0 {
		return fmt.Errorf("amount must be positive")
	}
	usdcAmount, err := solanautil.NewUSDCAmount(amount)
	if err != nil {
		return fmt.Errorf("invalid amount %.6f: %w", amount, err)
	}
	amountLamports := usdcAmount.ToSmallestUnit().Uint64()

	funderPrivateKey, err := solanago.PrivateKeyFromBase58(funderSecretKey)
	if err != nil {
		return fmt.Errorf("error parsing funder private key: %w", err)
	}
	funderPublicKey := funderPrivateKey.PublicKey()
	fmt.Printf("  Funder Public Key: %s\n", funderPublicKey)

	recipientPublicKey, err := solanago.PublicKeyFromBase58(recipientAddrStr)
	if err != nil {
		return fmt.Errorf("invalid recipient escrow address '%s': %w", recipientAddrStr, err)
	}

	usdcMint, err := solanago.PublicKeyFromBase58(usdcMintAddrStr)
	if err != nil {
		return fmt.Errorf("invalid USDC mint address '%s': %w", usdcMintAddrStr, err)
	}

	fmt.Printf("  Amount: %.6f USDC\n", amount)
	fmt.Printf("  Funder Public Key: %s\n", funderPublicKey.String())
	fmt.Printf("  Recipient Escrow Wallet: %s\n", recipientPublicKey.String())
	fmt.Printf("  RPC Endpoint: %s\n", rpcEndpoint)
	fmt.Printf("  USDC Mint: %s\n", usdcMintAddrStr)
	fmt.Printf("  Workflow ID (Memo): %s\n", workflowID)

	// --- Initialize Solana Client ---
	client := solanautil.NewRPCClient(rpcEndpoint)
	if err := solanautil.CheckRPCHealth(context.Background(), client); err != nil {
		return fmt.Errorf("RPC health check failed for %s: %w", rpcEndpoint, err)
	}
	fmt.Println("  RPC connection successful.")

	// --- Perform Transfer ---
	fmt.Println("  Sending transaction (with memo)...")
	sig, err := solanautil.SendUSDCWithMemo(
		context.Background(),
		client,
		usdcMint,
		funderPrivateKey,
		recipientPublicKey,
		amountLamports,
		workflowID,
	)
	if err != nil {
		return fmt.Errorf("failed to send USDC with memo: %w", err)
	}
	fmt.Printf("  Transaction sent! Signature: %s\n", sig.String())

	// --- Confirm Transaction (Optional but Recommended) ---
	fmt.Println("  Waiting for final confirmation...")
	confirmCtx, cancel := context.WithTimeout(context.Background(), 90*time.Second) // 90s timeout for confirmation
	defer cancel()
	err = solanautil.ConfirmTransaction(confirmCtx, client, sig, rpc.CommitmentFinalized)
	if err != nil {
		// Log warning but don't fail the command entirely, tx might still succeed
		fmt.Printf("  Warning: Failed to confirm transaction within timeout: %v\n", err)
		fmt.Printf("  You can check the status manually: solana confirm -v %s --url %s\n", sig, rpcEndpoint)
	} else {
		fmt.Println("  Transaction confirmed successfully!")
	}

	fmt.Println("Escrow funding process complete.")
	return nil
}

func adminCommands() []*cli.Command {
	return []*cli.Command{
		{
			Name:  "auth",
			Usage: "Authentication related commands",
			Subcommands: []*cli.Command{
				{
					Name:        "get-token",
					Usage:       "Gets a new auth token with the user's email",
					Description: "This is a sudo operation and requires the server secret key for basic auth.",
					Flags: []cli.Flag{
						&cli.StringFlag{
							Name:    "endpoint",
							Aliases: []string{"end", "e"},
							Value:   "http://localhost:8080",
							Usage:   "Server endpoint",
							EnvVars: []string{EnvServerEndpoint},
						},
						&cli.StringFlag{
							Name:    "secret-key",
							Aliases: []string{"sk", "s"},
							Usage:   "Server secret key",
							EnvVars: []string{EnvServerSecretKey},
						},
						&cli.StringFlag{
							Name:     "email",
							Required: true,
							Usage:    "User's email",
						},
						&cli.StringFlag{
							Name:  "env-file",
							Usage: "Path to .env file to update with the new token",
							Value: ".env",
						},
					},
					Action: getAuthToken,
				},
			},
		},
		{
			Name:  "bounty",
			Usage: "Bounty management commands",
			Subcommands: []*cli.Command{
				{
					Name:        "create",
					Usage:       "Create a new bounty",
					Description: "Creates a new bounty with specified parameters",
					Flags: []cli.Flag{
						&cli.StringFlag{
							Name:    "endpoint",
							Aliases: []string{"end", "e"},
							Value:   "http://localhost:8080",
							Usage:   "Server endpoint",
							EnvVars: []string{EnvServerEndpoint},
						},
						&cli.StringFlag{
							Name:     "token",
							Required: true,
							Usage:    "Authorization token",
							EnvVars:  []string{EnvAuthToken},
						},
						&cli.StringSliceFlag{
							Name:     "requirements",
							Aliases:  []string{"req", "r"},
							Required: true,
							Usage:    "Description of the bounty requirements",
						},
						&cli.Float64Flag{
							Name:     "per-post",
							Required: true,
							Usage:    "Amount to pay per post (in USDC)",
						},
						&cli.Float64Flag{
							Name:     "total",
							Required: true,
							Usage:    "Total bounty amount (in USDC)",
						},
						&cli.StringFlag{
							Name:     "bounty-owner-wallet",
							Required: true,
							Usage:    "Wallet address of the bounty owner",
							EnvVars:  []string{EnvTestOwnerWallet},
						},
						&cli.StringFlag{
							Name:    "bounty-funder-wallet",
							Usage:   "Solana wallet address providing the initial bounty funds (checked for payment)",
							EnvVars: []string{EnvTestFunderWallet},
						},
						&cli.StringFlag{
							Name:     "platform",
							Required: true,
							Usage:    "Platform type (reddit, youtube, yelp, google)",
							Value:    "reddit",
						},
						&cli.DurationFlag{
							Name:  "payment-timeout",
							Usage: "Timeout duration to wait for payment verification (e.g., 10s, 1m)",
							Value: 10 * time.Second,
						},
					},
					Action: createBounty,
				},
				{
					Name:        "pay",
					Usage:       "Pay a bounty",
					Description: "Pays a bounty to a user",
					Flags: []cli.Flag{
						&cli.StringFlag{
							Name:    "endpoint",
							Aliases: []string{"end", "e"},
							Value:   "http://localhost:8080",
							Usage:   "Server endpoint",
							EnvVars: []string{EnvServerEndpoint},
						},
						&cli.StringFlag{
							Name:     "token",
							Required: true,
							Usage:    "Authorization token",
							EnvVars:  []string{EnvAuthToken},
						},
						&cli.StringFlag{
							Name:  "user-id",
							Usage: "ID of the user receiving the bounty (optional)",
						},
						&cli.Float64Flag{
							Name:     "amount",
							Required: true,
							Usage:    "Amount to pay (in USDC)",
						},
						&cli.StringFlag{
							Name:     "to-account",
							Required: true,
							Usage:    "Destination wallet address",
						},
						&cli.StringFlag{
							Name:  "from-account",
							Usage: "Source account (defaults to escrow if empty)",
						},
					},
					Action: payBounty,
				},
				{
					Name:        "assess",
					Usage:       "Signal to assess content against bounty requirements",
					Description: "Sends a signal to assess content against bounty requirements",
					Flags: []cli.Flag{
						&cli.StringFlag{
							Name:    "endpoint",
							Aliases: []string{"end", "e"},
							Value:   "http://localhost:8080",
							Usage:   "Server endpoint",
							EnvVars: []string{EnvServerEndpoint},
						},
						&cli.StringFlag{
							Name:     "token",
							Required: true,
							Usage:    "Authorization token",
							EnvVars:  []string{EnvAuthToken},
						},
						&cli.StringFlag{
							Name:     "bounty-id",
							Required: true,
							Usage:    "ID of the bounty",
						},
						&cli.StringFlag{
							Name:     "content-id",
							Required: true,
							Usage:    "ID of the content to assess",
						},
						&cli.StringFlag{
							Name:     "user-id",
							Required: true,
							Usage:    "ID of the user who created the content",
						},
						&cli.StringFlag{
							Name:     "platform",
							Required: true,
							Usage:    "Platform type (reddit, youtube, yelp, google)",
							Value:    "reddit",
						},
					},
					Action: assessContent,
				},
				{
					Name:        "list",
					Usage:       "List available bounties",
					Description: "Lists all available bounties",
					Flags: []cli.Flag{
						&cli.StringFlag{
							Name:    "endpoint",
							Aliases: []string{"end", "e"},
							Value:   "http://localhost:8080",
							Usage:   "Server endpoint",
							EnvVars: []string{EnvServerEndpoint},
						},
						&cli.StringFlag{
							Name:     "token",
							Required: true,
							Usage:    "Authorization token",
							EnvVars:  []string{EnvAuthToken},
						},
					},
					Action: listBounties,
				},
			},
		},
		{
			Name:  "util",
			Usage: "Utility commands",
			Subcommands: []*cli.Command{
				{
					Name:  "print-private-key",
					Usage: "Prints the base58 private key string from a keypair JSON file",
					Flags: []cli.Flag{
						&cli.StringFlag{
							Name:    "keypair-path",
							Aliases: []string{"k"},
							// Required: false, // Made optional to allow default
							Usage: "Path to the Solana keypair JSON file",
							Value: getDefaultKeypairPath(), // Use helper for dynamic default
						},
					},
					Action: printPrivateKeyAction,
				},
				{
					Name:        "fund-escrow",
					Usage:       "Sends USDC from a funder wallet to the escrow wallet",
					Description: "Utility to manually fund the escrow wallet for testing/development. Uses Solana transfer logic directly.",
					Flags: []cli.Flag{
						&cli.Float64Flag{
							Name:    "amount",
							Aliases: []string{"a"},
							Value:   1.0,
							Usage:   "Amount of USDC to send",
						},
						&cli.StringFlag{
							Name:     "from-secret",
							Usage:    "Base58 encoded private key string of the funder",
							EnvVars:  []string{EnvTestFunderPrivateKey},
							Required: true,
						},
						&cli.StringFlag{
							Name:     "to",
							Usage:    "Recipient escrow wallet public key address",
							EnvVars:  []string{EnvSolanaEscrowWallet},
							Required: true, // Require explicit recipient
						},
						&cli.StringFlag{
							Name:     "rpc-endpoint",
							Usage:    "Solana RPC endpoint URL",
							EnvVars:  []string{EnvSolanaRPCEndpoint},
							Required: true, // Require explicit endpoint
						},
						&cli.StringFlag{
							Name:     "usdc-mint",
							Usage:    "USDC mint public key address",
							EnvVars:  []string{EnvSolanaUSDCMintAddress},
							Required: true, // Require explicit mint
						},
						&cli.StringFlag{
							Name:     "workflow-id",
							Aliases:  []string{"id", "w"},
							Usage:    "Workflow ID to include in transaction memo",
							Required: true,
						},
					},
					Action: fundEscrowAction,
				},
			},
		},
	}
}
