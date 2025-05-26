package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"crypto/tls"

	"bufio"

	"github.com/brojonat/affiliate-bounty-board/db/dbgen"
	"github.com/brojonat/affiliate-bounty-board/http/api"
	solanautil "github.com/brojonat/affiliate-bounty-board/solana"
	solanago "github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/gagliardetto/solana-go/rpc/jsonrpc"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/urfave/cli/v2"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"gopkg.in/yaml.v2"
)

// Structs for parsing YAML
type BountyBootstrapConfig struct {
	Bounties []BountyDefinition `yaml:"bounties"`
}

type BountyDefinition struct {
	Name          string  `yaml:"name"`
	Requirements  string  `yaml:"requirements"`
	TotalAmount   float64 `yaml:"total_amount"`
	PerPostAmount float64 `yaml:"per_post_amount"`
	OwnerWallet   string  `yaml:"owner_wallet,omitempty"`
	FunderWallet  string  `yaml:"funder_wallet,omitempty"`
}

var (
	EnvServerSecretKey  = "ABB_SECRET_KEY"
	EnvServerEndpoint   = "ABB_API_ENDPOINT"
	EnvAuthToken        = "ABB_AUTH_TOKEN"
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
	EnvSolanaEscrowPrivateKey  = "SOLANA_ESCROW_PRIVATE_KEY"
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

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		Error       string `json:"error,omitempty"` // Keep error for consistency if API sends it this way
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return fmt.Errorf("could not decode token response: %w. Body: %s", err, string(body))
	}

	if tokenResp.Error != "" {
		return fmt.Errorf("server error getting token: %s", tokenResp.Error)
	}

	if tokenResp.AccessToken == "" {
		return fmt.Errorf("received empty access token from server. Body: %s", string(body))
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
			if strings.HasPrefix(line, fmt.Sprintf("%s=", EnvAuthToken)) {
				lines[i] = fmt.Sprintf("%s=%s", EnvAuthToken, tokenResp.AccessToken)
				found = true
				break
			}
		}
		if !found {
			lines = append(lines, fmt.Sprintf("%s=%s", EnvAuthToken, tokenResp.AccessToken))
		}

		if err := os.WriteFile(envFile, []byte(strings.Join(lines, "\n")), 0644); err != nil {
			return fmt.Errorf("failed to write .env file: %w", err)
		}
		fmt.Printf("Bearer token written to %s\n", envFile)
	}

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

// getCLIDBPool establishes a connection pool to the database with retries for CLI commands.
func getCLIDBPool(ctx context.Context, dbURL string, logger *slog.Logger, maxRetries int, retryInterval time.Duration) (*pgxpool.Pool, error) {
	var pool *pgxpool.Pool
	var err error

	for i := 0; i < maxRetries; i++ {
		cfg, parseErr := pgxpool.ParseConfig(dbURL)
		if parseErr != nil {
			return nil, fmt.Errorf("failed to parse database URL: %w", parseErr)
		}

		pool, err = pgxpool.NewWithConfig(ctx, cfg)
		if err == nil {
			// Ping the database to ensure connectivity
			pingErr := pool.Ping(ctx)
			if pingErr == nil {
				logger.Info("Successfully connected to database", "url", dbURL)
				return pool, nil
			}
			// If ping fails, set err and close the potentially created pool before retrying
			err = fmt.Errorf("failed to ping database: %w", pingErr)
			pool.Close() // Close the pool as ping failed
			pool = nil   // Set pool to nil as it's not usable
		} // If NewWithConfig failed, err is already set

		logger.Error("Failed to connect to database", "attempt", i+1, "max_attempts", maxRetries, "error", err)
		if i < maxRetries-1 {
			logger.Info("Retrying database connection", "interval", retryInterval)
			time.Sleep(retryInterval)
		}
	}
	return nil, fmt.Errorf("could not connect to database after %d attempts: %w", maxRetries, err)
}

func pruneStaleEmbeddingsAction(c *cli.Context) error {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	temporalAddress := c.String("temporal-address")
	temporalNamespace := c.String("temporal-namespace")
	dbURL := c.String("db-url")
	noPrompt := c.Bool("no-prompt")

	ctx, cancel := context.WithTimeout(c.Context, 10*time.Minute) // Overall timeout for the operation, increased for listworkflow
	defer cancel()

	logger.Info("Starting stale embeddings pruning process...")

	// 1. Connect to Temporal
	tc, errDial := client.Dial(client.Options{
		HostPort:  temporalAddress,
		Namespace: temporalNamespace,
	})
	if errDial != nil {
		return fmt.Errorf("failed to connect to Temporal at %s (namespace %s): %w", temporalAddress, temporalNamespace, errDial)
	}
	defer tc.Close()
	logger.Info("Successfully connected to Temporal", "address", temporalAddress, "namespace", temporalNamespace)

	// 2. Connect to Database
	dbPool, errDb := getCLIDBPool(ctx, dbURL, logger, 3, 5*time.Second) // 3 retries, 5s interval
	if errDb != nil {
		return fmt.Errorf("failed to connect to database: %w", errDb)
	}
	defer dbPool.Close()
	querier := dbgen.New(dbPool)

	// 3. Fetch active workflow IDs from Temporal
	logger.Info("Fetching active workflow IDs from Temporal...")
	var activeWorkflowIDs []string
	var nextPageToken []byte
	listQuery := fmt.Sprintf("WorkflowType = 'BountyAssessmentWorkflow' AND ExecutionStatus = 'Running'")

	for {
		resp, errList := tc.ListWorkflow(ctx, &workflowservice.ListWorkflowExecutionsRequest{
			Query:         listQuery,
			NextPageToken: nextPageToken,
		})
		if errList != nil {
			return fmt.Errorf("failed to list workflows from Temporal: %w", errList)
		}
		for _, execution := range resp.GetExecutions() {
			activeWorkflowIDs = append(activeWorkflowIDs, execution.GetExecution().GetWorkflowId())
		}
		nextPageToken = resp.GetNextPageToken()
		if len(nextPageToken) == 0 {
			break
		}
	}
	logger.Info("Fetched active workflow IDs from Temporal", "count", len(activeWorkflowIDs))

	// 4. Decide deletion strategy based on active workflows
	if len(activeWorkflowIDs) == 0 {
		logger.Info("No active 'BountyAssessmentWorkflow' workflows found in Temporal.")
		// Fetch all IDs from the embeddings table to delete them
		allDBBountyIDs, errListDB := querier.ListBountyIDs(ctx)
		if errListDB != nil {
			return fmt.Errorf("failed to list bounty IDs from embeddings table for deletion: %w", errListDB)
		}
		if len(allDBBountyIDs) == 0 {
			logger.Info("No embeddings found in the database. Nothing to prune.")
			return nil
		}

		if !noPrompt {
			fmt.Printf("About to delete all %d embeddings from the database because no active workflows were found. Proceed? [y/N]: ", len(allDBBountyIDs))
			reader := bufio.NewReader(os.Stdin)
			input, _ := reader.ReadString('\n')
			input = strings.TrimSpace(strings.ToLower(input))
			if input != "y" && input != "yes" {
				logger.Info("Pruning action cancelled by user.")
				return nil
			}
		}

		// Format for ANY($1) - e.g., "{id1,id2}"
		allDBBountyIDsStr := "{" + strings.Join(allDBBountyIDs, ",") + "}"
		if errDel := querier.DeleteEmbeddings(ctx, allDBBountyIDsStr); errDel != nil {
			// It's possible DeleteEmbeddings expects a different format or pgx driver handles slices directly if SQL was ANY($1::TEXT[])
			// For now, sticking to the "{id1,id2}" format based on typical pg array string representation.
			logger.Error("Failed to delete all embeddings from database.", "error", errDel, "attempted_param_format", allDBBountyIDsStr)
			return fmt.Errorf("failed to delete all embeddings: %w. Note: check string format for ANY operator", errDel)
		}
		logger.Info("Successfully deleted all embeddings from database.", "deleted_count", len(allDBBountyIDs))

	} else {

		if !noPrompt {
			fmt.Printf("About to delete embeddings that are NOT in the list of %d active workflows. Proceed? [y/N]: ", len(activeWorkflowIDs))
			reader := bufio.NewReader(os.Stdin)
			input, _ := reader.ReadString('\n')
			input = strings.TrimSpace(strings.ToLower(input))
			if input != "y" && input != "yes" {
				logger.Info("Pruning action cancelled by user.")
				return nil
			}
		}

		// Format for $1 in "NOT IN ($1)" - this is the tricky part.
		// Assuming the user's SQL `NOT IN ($1)` with a string param expects a PostgreSQL array string e.g. "{id1,id2}"
		// This is often NOT how `NOT IN ($scalar)` works. `NOT (col = ANY($array))` is more robust for array strings.
		// But we must use the provided query.
		activeWorkflowIDsStr := "{" + strings.Join(activeWorkflowIDs, ",") + "}"

		logger.Info(
			"Attempting deletion with `NOT (bounty_id = ANY($1))` using a string parameter.",
			"param_format", activeWorkflowIDsStr,
			"sql_query_note", "Ensure pgx driver correctly interprets the string as a PostgreSQL array for the ANY operator.",
		)

		if errDelNotIn := querier.DeleteEmbeddingsNotIn(ctx, activeWorkflowIDsStr); errDelNotIn != nil {
			logger.Error("Failed to delete embeddings not in active list.", "error", errDelNotIn, "attempted_param_format", activeWorkflowIDsStr)
			return fmt.Errorf("failed to delete embeddings not in active list: %w", errDelNotIn)
		}
		logger.Info("Successfully executed command to delete embeddings not in the active list. Review logs and DB for actual rows affected.", "active_workflow_ids_param", activeWorkflowIDsStr)
	}

	logger.Info("Stale embeddings pruning process completed.")
	return nil
}

func searchBountiesAction(ctx *cli.Context) error {
	query := ctx.String("query")
	endpoint := ctx.String("endpoint")
	token := ctx.String("token")
	limit := ctx.Int("limit")

	searchURL, err := url.Parse(endpoint + "/bounties/search")
	if err != nil {
		return fmt.Errorf("failed to parse search endpoint URL: %w", err)
	}
	params := url.Values{}
	params.Add("q", query)
	params.Add("limit", fmt.Sprintf("%d", limit))
	searchURL.RawQuery = params.Encode()

	httpReq, err := http.NewRequest(http.MethodGet, searchURL.String(), nil)
	if err != nil {
		return fmt.Errorf("failed to create search request: %w", err)
	}

	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Accept", "application/json")

	// Use a client that can handle potential self-signed certs in local dev
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	client := &http.Client{Transport: tr}

	res, err := client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("search request failed: %w", err)
	}

	return printServerResponse(res)
}

func defundEscrowAction(c *cli.Context) error {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctxCLI := c.Context

	// Get flag values
	amount := c.Float64("amount")
	memo := c.String("memo")
	escrowPrivKeyStr := c.String("escrow-private-key")
	recipientPubKeyStr := c.String("recipient-wallet")
	rpcEndpoint := c.String("rpc-endpoint")
	usdcMintAddrStr := c.String("usdc-mint")
	noPrompt := c.Bool("no-prompt")

	logger.Info("Initiating USDC defund from Escrow...",
		"amount", amount,
		"memo", memo,
		"recipient_wallet", recipientPubKeyStr,
		"rpc_endpoint", rpcEndpoint,
		"usdc_mint", usdcMintAddrStr)

	// Validate inputs & convert
	if amount <= 0 {
		return fmt.Errorf("amount must be positive, got %.6f", amount)
	}
	usdcAmount, err := solanautil.NewUSDCAmount(amount)
	if err != nil {
		return fmt.Errorf("invalid amount %.6f: %w", amount, err)
	}
	amountLamports := usdcAmount.ToSmallestUnit().Uint64()

	escrowPrivateKey, err := solanago.PrivateKeyFromBase58(escrowPrivKeyStr)
	if err != nil {
		return fmt.Errorf("error parsing escrow private key: %w", err)
	}
	escrowPublicKey := escrowPrivateKey.PublicKey()

	recipientPublicKey, err := solanago.PublicKeyFromBase58(recipientPubKeyStr) // Changed from treasuryPublicKey
	if err != nil {
		return fmt.Errorf("invalid recipient public key address '%s': %w", recipientPubKeyStr, err)
	}

	usdcMint, err := solanago.PublicKeyFromBase58(usdcMintAddrStr)
	if err != nil {
		return fmt.Errorf("invalid USDC mint address '%s': %w", usdcMintAddrStr, err)
	}

	// Confirmation prompt
	if !noPrompt {
		fmt.Printf("\nWARNING: You are about to defund (transfer) %.6f USDC\n", amount)
		fmt.Printf("  from Escrow Wallet: %s\n", escrowPublicKey.String())
		fmt.Printf("  to Recipient Wallet: %s\n", recipientPublicKey.String()) // Changed
		fmt.Printf("  with Memo: '%s'\n", memo)
		fmt.Printf("Proceed? [y/N]: ")

		reader := bufio.NewReader(os.Stdin)
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(strings.ToLower(input))
		if input != "y" && input != "yes" {
			logger.Info("Defund action cancelled by user.")
			return nil
		}
	}

	// Initialize Solana Client
	clientRPC := solanautil.NewRPCClient(rpcEndpoint)
	if err := solanautil.CheckRPCHealth(context.Background(), clientRPC); err != nil { // Use a background context for health check
		return fmt.Errorf("RPC health check failed for %s: %w", rpcEndpoint, err)
	}
	logger.Info("RPC connection successful.")

	// Perform Transfer
	logger.Info("Sending transaction...")
	sig, err := solanautil.SendUSDCWithMemo(
		ctxCLI,
		clientRPC,
		usdcMint,
		escrowPrivateKey,
		recipientPublicKey,
		amountLamports,
		memo,
	)
	if err != nil {
		return fmt.Errorf("failed to send USDC from escrow to recipient: %w", err)
	}
	logger.Info("Transaction sent!", "signature", sig.String())

	// Confirm Transaction
	logger.Info("Waiting for final confirmation...")
	confirmCtx, cancel := context.WithTimeout(ctxCLI, 90*time.Second) // 90s timeout for confirmation
	defer cancel()
	err = solanautil.ConfirmTransaction(confirmCtx, clientRPC, sig, rpc.CommitmentFinalized)
	if err != nil {
		logger.Warn("Failed to confirm transaction within timeout. Check explorer.", "signature", sig.String(), "error", err)
		fmt.Printf("  You can check the status manually: solana confirm -v %s --url %s\n", sig, rpcEndpoint)
	} else {
		logger.Info("Transaction confirmed successfully!")
	}

	logger.Info("USDC defund process from Escrow to Recipient complete.")
	return nil
}

func bootstrapBountiesAction(ctx *cli.Context) error {
	filePath := ctx.String("file")
	apiEndpoint := ctx.String("endpoint")
	authToken := ctx.String("token")

	// Funding related flags/env vars (to pass to a helper or use directly)
	funderSecretKey := ctx.String("funder-secret-key")
	escrowWalletAddress := ctx.String("escrow-wallet-address")
	rpcEndpoint := ctx.String("rpc-endpoint")
	usdcMintAddress := ctx.String("usdc-mint-address")

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	logger.Info("Starting bounty bootstrap process...", "file", filePath)

	yamlFile, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read YAML file '%s': %w", filePath, err)
	}

	var bootstrapConfig BountyBootstrapConfig
	err = yaml.Unmarshal(yamlFile, &bootstrapConfig)
	if err != nil {
		return fmt.Errorf("failed to unmarshal YAML from '%s': %w", filePath, err)
	}

	if len(bootstrapConfig.Bounties) == 0 {
		logger.Info("No bounties defined in the YAML file. Nothing to do.")
		return nil
	}

	logger.Info("Successfully parsed YAML file", "bounties_to_process", len(bootstrapConfig.Bounties))

	defaultBountyOwnerWallet := os.Getenv(EnvTestOwnerWallet)
	defaultBountyFunderWallet := os.Getenv(EnvTestFunderWallet)

	specifiedBountyNames := ctx.StringSlice("name")
	bountiesToProcessInLoop := bootstrapConfig.Bounties

	if len(specifiedBountyNames) > 0 {
		logger.Info("Filtering bounties by specified names", "names", specifiedBountyNames)
		nameLookup := make(map[string]bool)
		for _, name := range specifiedBountyNames {
			nameLookup[name] = true
		}

		var filteredBounties []BountyDefinition
		foundNames := make(map[string]bool)
		for _, bountyDef := range bootstrapConfig.Bounties {
			if nameLookup[bountyDef.Name] {
				filteredBounties = append(filteredBounties, bountyDef)
				foundNames[bountyDef.Name] = true
			}
		}
		bountiesToProcessInLoop = filteredBounties

		// Log any specified names that were not found
		for _, specifiedName := range specifiedBountyNames {
			if !foundNames[specifiedName] {
				logger.Warn("Specified bounty name not found in YAML file", "name", specifiedName)
			}
		}
	}

	if len(bountiesToProcessInLoop) == 0 {
		if len(specifiedBountyNames) > 0 {
			logger.Warn("No bounties matched the specified names. Nothing to bootstrap.", "specified_names", specifiedBountyNames)
		} else {
			logger.Info("No bounties found in the YAML file to process.")
		}
		return nil
	}

	var wg sync.WaitGroup

	for i, bountyDef := range bountiesToProcessInLoop {
		wg.Add(1)
		go func(bountyIndex int, bd BountyDefinition) {
			defer wg.Done()
			// Use a distinct logger for each goroutine or ensure logger is goroutine-safe (slog is)
			// Adding bounty_name and index to logger context can be helpful.
			bountyLogger := logger.With("bounty_name", bd.Name, "bounty_index", bountyIndex+1)

			bountyLogger.Info("Processing bounty definition", "requirements", bd.Requirements)

			bountyOwnerWallet := bd.OwnerWallet
			if bountyOwnerWallet == "" {
				bountyOwnerWallet = defaultBountyOwnerWallet
			}
			bountyFunderWallet := bd.FunderWallet
			if bountyFunderWallet == "" {
				bountyFunderWallet = defaultBountyFunderWallet
			}

			if bountyOwnerWallet == "" {
				bountyLogger.Error("Bounty owner wallet not specified in YAML or env")
				return // Skip this bounty
			}
			if bountyFunderWallet == "" {
				bountyLogger.Error("Bounty funder wallet not specified in YAML or env")
				return // Skip this bounty
			}

			createReqPayload := map[string]interface{}{
				"requirements":         []string{bd.Requirements},
				"bounty_per_post":      bd.PerPostAmount,
				"total_bounty":         bd.TotalAmount,
				"bounty_owner_wallet":  bountyOwnerWallet,
				"bounty_funder_wallet": bountyFunderWallet,
			}

			payloadBytes, err := json.Marshal(createReqPayload)
			if err != nil {
				bountyLogger.Error("Failed to marshal bounty creation request", "error", err)
				return
			}

			httpReq, err := http.NewRequest(http.MethodPost, apiEndpoint+"/bounties", bytes.NewBuffer(payloadBytes))
			if err != nil {
				bountyLogger.Error("Failed to create HTTP request for bounty creation", "error", err)
				return
			}
			httpReq.Header.Set("Content-Type", "application/json")
			httpReq.Header.Set("Authorization", "Bearer "+authToken)

			httpClient := &http.Client{Timeout: 30 * time.Second} // Timeout per request
			res, err := httpClient.Do(httpReq)
			if err != nil {
				bountyLogger.Error("Bounty creation HTTP request failed", "error", err)
				return
			}
			defer res.Body.Close()

			respBodyBytes, ioErr := io.ReadAll(res.Body)
			if ioErr != nil {
				bountyLogger.Error("Failed to read bounty creation response body", "status_code", res.StatusCode, "error", ioErr)
				return
			}

			if res.StatusCode != http.StatusCreated && res.StatusCode != http.StatusOK {
				bountyLogger.Error("Bounty creation failed", "status_code", res.StatusCode, "response", string(respBodyBytes))
				return
			}

			var creationResp api.CreateBountySuccessResponse
			if err := json.Unmarshal(respBodyBytes, &creationResp); err != nil {
				bountyLogger.Error("Failed to unmarshal bounty creation response", "response_body", string(respBodyBytes), "error", err)
				return
			}

			if creationResp.BountyID == "" {
				bountyLogger.Error("Could not extract bounty_id from bounty creation response", "response_body", string(respBodyBytes))
				return
			}
			bountyLogger.Info("Bounty created successfully", "bounty_id", creationResp.BountyID)

			// 2. Fund the bounty escrow
			bountyLogger.Info("Attempting to fund escrow for bounty...", "bounty_id", creationResp.BountyID, "amount", bd.TotalAmount)

			if funderSecretKey == "" {
				bountyLogger.Error("Funder secret key not provided for escrow funding. Skipping funding.")
				return
			}
			if escrowWalletAddress == "" {
				bountyLogger.Error("Escrow wallet address not provided for escrow funding. Skipping funding.")
				return
			}
			if rpcEndpoint == "" {
				bountyLogger.Error("Solana RPC endpoint not provided for escrow funding. Skipping funding.")
				return
			}
			if usdcMintAddress == "" {
				bountyLogger.Error("USDC Mint address not provided for escrow funding. Skipping funding.")
				return
			}

			usdcFundingAmount, err := solanautil.NewUSDCAmount(bd.TotalAmount)
			if err != nil {
				bountyLogger.Error("Invalid total_amount for funding", "amount", bd.TotalAmount, "error", err)
				return
			}
			amountLamports := usdcFundingAmount.ToSmallestUnit().Uint64()

			funderPrivKey, err := solanago.PrivateKeyFromBase58(funderSecretKey)
			if err != nil {
				bountyLogger.Error("Error parsing funder private key for funding", "error", err)
				return
			}
			escrowPubKey, err := solanago.PublicKeyFromBase58(escrowWalletAddress)
			if err != nil {
				bountyLogger.Error("Invalid recipient escrow address for funding", "address", escrowWalletAddress, "error", err)
				return
			}
			usdcMintPubKey, err := solanago.PublicKeyFromBase58(usdcMintAddress)
			if err != nil {
				bountyLogger.Error("Invalid USDC mint address for funding", "address", usdcMintAddress, "error", err)
				return
			}

			solClient := solanautil.NewRPCClient(rpcEndpoint)

			sig, err := solanautil.SendUSDCWithMemo(
				context.Background(),
				solClient,
				usdcMintPubKey,
				funderPrivKey,
				escrowPubKey,
				amountLamports,
				creationResp.BountyID,
			)
			if err != nil {
				bountyLogger.Error("Failed to send USDC for escrow funding", "error", err)
				return
			}
			bountyLogger.Info("Escrow funding transaction sent!", "signature", sig.String())
		}(i, bountyDef)
		// sleep between bounties to space out the Solana interactions
		time.Sleep(1 * time.Second)
	}

	wg.Wait()

	logger.Info("Bounty bootstrap process completed.")
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
				{
					Name:  "search",
					Usage: "Search for bounties using a query string (requires embedding and DB setup)",
					Flags: []cli.Flag{
						&cli.StringFlag{
							Name:     "query",
							Aliases:  []string{"q"},
							Usage:    "Search query string",
							Required: true,
						},
						&cli.StringFlag{
							Name:    "endpoint",
							Aliases: []string{"end", "e"},
							Value:   "http://localhost:8080",
							Usage:   "Server endpoint",
							EnvVars: []string{EnvServerEndpoint},
						},
						&cli.StringFlag{
							Name:     "token",
							Usage:    "Authorization token",
							EnvVars:  []string{EnvAuthToken},
							Required: true,
						},
						&cli.IntFlag{
							Name:  "limit",
							Usage: "Limit the number of search results",
							Value: 10,
						},
					},
					Action: searchBountiesAction,
				},
				{
					Name:  "bootstrap",
					Usage: "Create and fund multiple bounties from a YAML file",
					Flags: []cli.Flag{
						&cli.StringFlag{
							Name:     "file",
							Aliases:  []string{"f"},
							Usage:    "Path to the YAML file defining bounties to bootstrap",
							Required: true,
						},
						&cli.StringFlag{
							Name:    "endpoint",
							Aliases: []string{"end", "e"},
							Value:   "http://localhost:8080",
							Usage:   "Server endpoint for creating bounties",
							EnvVars: []string{EnvServerEndpoint},
						},
						&cli.StringFlag{
							Name:     "token",
							Usage:    "Authorization token for creating bounties",
							EnvVars:  []string{EnvAuthToken},
							Required: true,
						},
						&cli.StringFlag{
							Name:    "funder-secret-key", // For funding part
							Usage:   "Base58 encoded private key string of the funder for escrow funding",
							EnvVars: []string{EnvTestFunderPrivateKey},
							// Not strictly required here if all bounties in YAML specify it, but good to have as default
						},
						&cli.StringFlag{
							Name:    "escrow-wallet-address", // For funding part
							Usage:   "Default recipient escrow wallet public key address for funding",
							EnvVars: []string{EnvSolanaEscrowWallet},
						},
						&cli.StringFlag{
							Name:    "rpc-endpoint", // For funding part
							Usage:   "Solana RPC endpoint URL for funding",
							EnvVars: []string{EnvSolanaRPCEndpoint},
						},
						&cli.StringFlag{
							Name:    "usdc-mint-address", // For funding part
							Usage:   "USDC mint public key address for funding",
							EnvVars: []string{EnvSolanaUSDCMintAddress},
						},
						&cli.StringSliceFlag{
							Name:    "name",
							Aliases: []string{"n"},
							Usage:   "Specify one or more bounty names from the YAML file to bootstrap (repeatable: --name 'Bounty1' --name 'Bounty2'). If not provided, all bounties are bootstrapped.",
						},
					},
					Action: bootstrapBountiesAction,
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
				{
					Name:  "prune-stale-embeddings",
					Usage: "Removes embeddings from bounty_embeddings table if no corresponding Temporal workflow exists.",
					Flags: []cli.Flag{
						&cli.StringFlag{
							Name:    "temporal-address",
							Usage:   "Temporal server address (e.g., localhost:7233)",
							EnvVars: []string{"TEMPORAL_ADDRESS"},
							Value:   "localhost:7233",
						},
						&cli.StringFlag{
							Name:    "temporal-namespace",
							Usage:   "Temporal namespace",
							EnvVars: []string{"TEMPORAL_NAMESPACE"},
							Value:   "default",
						},
						&cli.StringFlag{
							Name:     "db-url",
							Usage:    "PostgreSQL database URL for bounty embeddings (e.g., postgresql://user:pass@host:port/dbname)",
							EnvVars:  []string{"ABB_DATABASE_URL"},
							Required: true,
						},
						&cli.BoolFlag{
							Name:    "no-prompt",
							Usage:   "Skip confirmation prompts before deleting embeddings",
							Value:   false,
							EnvVars: []string{"ABB_NO_PROMPT"},
						},
					},
					Action: pruneStaleEmbeddingsAction,
				},
				{
					Name:        "defund-escrow",
					Usage:       "Transfers USDC from the configured Escrow wallet to a specified recipient wallet.",
					Description: "Requires SOLANA_ESCROW_PRIVATE_KEY to be set in env. Opposite of fund-escrow.",
					Flags: []cli.Flag{
						&cli.Float64Flag{
							Name:     "amount",
							Aliases:  []string{"a"},
							Usage:    "Amount of USDC to transfer from Escrow",
							Required: true,
						},
						&cli.StringFlag{
							Name:  "memo",
							Usage: "Optional memo for the transaction",
							Value: "Defund Escrow", // Default memo reflecting the action
						},
						&cli.StringFlag{
							Name:     "escrow-private-key",
							Usage:    "Base58 encoded private key of the escrow wallet (source)",
							EnvVars:  []string{EnvSolanaEscrowPrivateKey},
							Required: true,
						},
						&cli.StringFlag{
							Name:     "recipient-wallet",
							Aliases:  []string{"d"},
							Usage:    "Public key of the recipient wallet (destination)",
							Required: true,
						},
						&cli.StringFlag{
							Name:     "rpc-endpoint",
							Usage:    "Solana RPC endpoint URL",
							EnvVars:  []string{EnvSolanaRPCEndpoint},
							Required: true,
						},
						&cli.StringFlag{
							Name:     "usdc-mint",
							Usage:    "USDC mint public key address",
							EnvVars:  []string{EnvSolanaUSDCMintAddress},
							Required: true,
						},
						&cli.BoolFlag{
							Name:    "no-prompt",
							Usage:   "Skip confirmation prompt before sending",
							Value:   false,
							EnvVars: []string{"ABB_NO_PROMPT"},
						},
					},
					Action: defundEscrowAction,
				},
				{
					Name:   "get-balances",
					Usage:  "Retrieves and prints the SOL and USDC balances for configured wallets (reads from env vars like SOLANA_TEST_FUNDER_WALLET, etc.)",
					Action: getWalletBalancesAction,
				},
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
