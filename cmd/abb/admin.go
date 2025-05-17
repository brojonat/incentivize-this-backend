package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"crypto/tls"

	"github.com/brojonat/affiliate-bounty-board/http/api"
	solanautil "github.com/brojonat/affiliate-bounty-board/solana"
	solanago "github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/gagliardetto/solana-go/rpc/jsonrpc"
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
	// Additional wallet env vars for the balances command
	EnvSolanaTestCreatorWallet = "SOLANA_TEST_CREATOR_WALLET"
	EnvSolanaTreasuryWallet    = "SOLANA_TREASURY_WALLET"
)

func getAuthToken(ctx *cli.Context) error {
	// Prepare form data
	formData := url.Values{}
	formData.Set("username", ctx.String("email"))
	formData.Set("password", ctx.String("secret-key"))

	r, err := http.NewRequest(
		http.MethodPost,
		ctx.String("endpoint")+"/token",
		bytes.NewBufferString(formData.Encode()), // Use encoded form data
	)
	if err != nil {
		return err
	}
	// Set Content-Type header for form data
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

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
	req := map[string]interface{}{
		"requirements":         []string{ctx.String("requirements")},
		"bounty_per_post":      ctx.Float64("per-post"),
		"total_bounty":         ctx.Float64("total"),
		"bounty_owner_wallet":  bountyOwnerWallet,
		"bounty_funder_wallet": bountyFunderWallet,
		"platform_type":        ctx.String("platform"),
		"content_kind":         ctx.String("content-kind"),
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
	// Create the request using a map for flexibility
	req := map[string]interface{}{
		"amount": ctx.Float64("amount"),
		"wallet": ctx.String("wallet"),
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
	httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", ctx.String("token")))

	// Execute the request
	res, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}

	return printServerResponse(res)
}

func assessContent(ctx *cli.Context) error {
	// Get flags
	payoutWalletStr := ctx.String("payout-wallet")
	platform := ctx.String("platform")
	kind := ctx.String("kind")
	contentID := ctx.String("content-id")

	// Prepend prefix based on platform and kind for Reddit
	if platform == "reddit" {
		if kind == "post" && !strings.HasPrefix(contentID, "t3_") {
			contentID = "t3_" + contentID
		} else if kind == "comment" && !strings.HasPrefix(contentID, "t1_") {
			contentID = "t1_" + contentID
		}
	}

	// Create a map for the request (without kind)
	req := map[string]interface{}{
		"bounty_id":     ctx.String("bounty-id"),
		"content_id":    contentID,
		"payout_wallet": payoutWalletStr,
		"platform":      platform,
		"content_kind":  kind,
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
	// Create a custom HTTP client that skips TLS verification
	// WARNING: Use with caution, bypasses security checks.
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	client := &http.Client{Transport: tr}

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

	// Execute the request using the custom client
	res, err := client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}

	return printServerResponse(res)
}

// Action function for listing paid bounties
func listPaidBountiesAction(ctx *cli.Context) error {
	limit := ctx.Int("limit")
	requestURL := fmt.Sprintf("%s/bounties/paid?limit=%d", ctx.String("endpoint"), limit)

	httpReq, err := http.NewRequest(
		http.MethodGet,
		requestURL,
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
	fmt.Println("WARNING: this doesn't check to see if the bounty exists or if you've already funded it! ")

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

	fmt.Println("Escrow funding process complete. ")
	return nil
}

// --- Start of new code for get-balances command ---

type walletInfo struct {
	Name    string
	EnvVar  string
	Address solanago.PublicKey
}

func getWalletBalancesAction(c *cli.Context) error {
	rpcEndpoint := os.Getenv(EnvSolanaRPCEndpoint)
	if rpcEndpoint == "" {
		return fmt.Errorf("%s is not set", EnvSolanaRPCEndpoint)
	}
	usdcMintAddressStr := os.Getenv(EnvSolanaUSDCMintAddress)
	if usdcMintAddressStr == "" {
		return fmt.Errorf("%s is not set", EnvSolanaUSDCMintAddress)
	}

	usdcMintPk, err := solanago.PublicKeyFromBase58(usdcMintAddressStr)
	if err != nil {
		return fmt.Errorf("invalid USDC mint address %s: %w", usdcMintAddressStr, err)
	}

	rpcClient := solanautil.NewRPCClient(rpcEndpoint)
	ctx := context.Background()

	wallets := []walletInfo{
		{Name: "Test Funder", EnvVar: EnvTestFunderWallet},
		{Name: "Test Owner", EnvVar: EnvTestOwnerWallet},
		{Name: "Test Creator", EnvVar: EnvSolanaTestCreatorWallet},
		{Name: "Escrow", EnvVar: EnvSolanaEscrowWallet},
		{Name: "Treasury", EnvVar: EnvSolanaTreasuryWallet},
	}

	fmt.Println("Fetching balances...")
	fmt.Println("-----------------------------------------------------")
	fmt.Printf("%-15s | %-15s | %-15s\n", "Wallet", "SOL Balance", "USDC Balance")
	fmt.Println("-----------------------------------------------------")

	for i, w := range wallets {
		walletAddrStr := os.Getenv(w.EnvVar)
		if walletAddrStr == "" {
			fmt.Printf("%-15s | Error: %s not set\n", w.Name, w.EnvVar)
			continue
		}
		walletPk, err := solanago.PublicKeyFromBase58(walletAddrStr)
		if err != nil {
			fmt.Printf("%-15s | Error: Invalid address %s (%s): %v\n", w.Name, walletAddrStr, w.EnvVar, err)
			continue
		}
		wallets[i].Address = walletPk // Store valid public key

		// Get SOL Balance
		solBalance, err := getSolBalance(ctx, rpcClient, walletPk)
		if err != nil {
			fmt.Printf("%-15s | SOL: Error: %v | USDC: N/A\n", w.Name, err)
			continue
		}

		// Get USDC Balance
		usdcBalance, err := getUsdcBalance(ctx, rpcClient, walletPk, usdcMintPk)
		if err != nil {
			fmt.Printf("%-15s | SOL: %.6f | USDC: Error: %v\n", w.Name, solBalance, err)
			continue
		}
		fmt.Printf("%-15s | %.6f SOL   | %.6f USDC\n", w.Name, solBalance, usdcBalance)
	}
	fmt.Println("-----------------------------------------------------")

	return nil
}

func getSolBalance(ctx context.Context, rpcClient *rpc.Client, wallet solanago.PublicKey) (float64, error) {
	out, err := rpcClient.GetBalance(ctx, wallet, rpc.CommitmentFinalized)
	if err != nil {
		return 0, fmt.Errorf("failed to get SOL balance for %s: %w", wallet, err)
	}
	return float64(out.Value) / float64(solanago.LAMPORTS_PER_SOL), nil
}

func getUsdcBalance(ctx context.Context, rpcClient *rpc.Client, wallet solanago.PublicKey, usdcMint solanago.PublicKey) (float64, error) {
	ata, _, err := solanago.FindAssociatedTokenAddress(wallet, usdcMint)
	if err != nil {
		return 0, fmt.Errorf("failed to find ATA for %s (mint %s): %w", wallet, usdcMint, err)
	}

	balanceResult, err := rpcClient.GetTokenAccountBalance(ctx, ata, rpc.CommitmentFinalized)
	if err != nil {
		var rpcErr *jsonrpc.RPCError
		if errors.As(err, &rpcErr) {
			// Error code -32602: "Invalid param: could not find account"
			// This indicates the ATA does not exist, hence 0 balance.
			if rpcErr.Code == -32602 || strings.Contains(rpcErr.Message, "could not find account") {
				return 0, nil // Gracefully handle as 0 balance
			}
		}
		// For other errors, or if it's not an RPCError of the expected type, return the original error.
		return 0, fmt.Errorf("failed to get token balance for ATA %s: %w", ata, err)
	}

	if balanceResult.Value == nil {
		return 0, fmt.Errorf("token balance result value is nil for ATA %s", ata)
	}
	if balanceResult.Value.UiAmount == nil {
		// This case implies the balance is 0 if UiAmount itself is nil after a successful call.
		return 0, nil
	}
	return *balanceResult.Value.UiAmount, nil
}

// --- End of new code for get-balances command ---

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
						&cli.StringFlag{
							Name:     "requirements",
							Aliases:  []string{"req", "r"},
							Required: true,
							Usage:    "String describing the bounty requirements",
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
						&cli.StringFlag{
							Name:     "content-kind",
							Required: true,
							Usage:    "Kind of content for the platform (e.g., post, comment, video, clip)",
						},
					},
					Action: createBounty,
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
							Name:     "payout-wallet",
							Required: true,
							Usage:    "Public key of the wallet to receive the payout",
						},
						&cli.StringFlag{
							Name:     "content-id",
							Required: true,
							Usage:    "ID of the content to assess",
						},
						&cli.StringFlag{
							Name:     "platform",
							Required: true,
							Usage:    "Platform type (reddit, youtube, yelp, google)",
							Value:    "reddit",
						},
						&cli.StringFlag{
							Name:  "kind",
							Usage: "Kind of content (e.g., post, comment, clip) (optional, depends on platform)",
							Value: "",
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
				{
					Name:        "list-paid",
					Usage:       "List recently paid bounties",
					Description: "Lists recently paid bounties from the /bounties/paid endpoint",
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
						&cli.IntFlag{
							Name:    "limit",
							Aliases: []string{"l"},
							Value:   20, // Default limit
							Usage:   "Maximum number of recent paid transactions to fetch",
						},
					},
					Action: listPaidBountiesAction,
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
		{
			Name:   "get-balances",
			Usage:  "Retrieves and prints the SOL and USDC balances for configured wallets (reads from env vars like SOLANA_TEST_FUNDER_WALLET, etc.)",
			Action: getWalletBalancesAction,
			Flags:  []cli.Flag{
				// No specific flags needed for this command as it reads from env.
				// Common flags like rpc-endpoint could be added if desired for override.
			},
		},
	}
}

func runAdminCmd(ctx *cli.Context) error {
	// This function is a placeholder or was truncated.
	// Based on typical urfave/cli structure, it might parse subcommands
	// or print help if no subcommand is given. For now, returning nil.
	// If this command had specific top-level action, it would go here.
	// Refer to the original file if this function had more complex logic.
	fmt.Println("Admin command executed. Use a subcommand like 'get-balances'.")
	cli.ShowSubcommandHelp(ctx) // Show help for subcommands
	return nil
}
