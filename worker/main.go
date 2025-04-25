package worker

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/brojonat/affiliate-bounty-board/abb"
	solanago "github.com/gagliardetto/solana-go"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

var (
	TaskQueueName = os.Getenv("TASK_QUEUE")

	// Solana configuration keys from environment
	EnvSolanaRPCEndpoint    = "SOLANA_RPC_ENDPOINT"
	EnvSolanaWSEndpoint     = "SOLANA_WS_ENDPOINT"
	EnvSolanaPrivateKey     = "SOLANA_ESCROW_PRIVATE_KEY"
	EnvSolanaEscrowWallet   = "SOLANA_ESCROW_WALLET"
	EnvSolanaTreasuryWallet = "SOLANA_TREASURY_WALLET"
	EnvSolanaUSDCMint       = "SOLANA_USDC_MINT_ADDRESS"

	// Platform API keys from environment
	EnvRedditUserAgent      = "REDDIT_USER_AGENT"
	EnvRedditUsername       = "REDDIT_USERNAME"
	EnvRedditPassword       = "REDDIT_PASSWORD"
	EnvRedditClientID       = "REDDIT_CLIENT_ID"
	EnvRedditClientSecret   = "REDDIT_CLIENT_SECRET"
	EnvYouTubeAPIKey        = "YOUTUBE_API_KEY"
	EnvYouTubeAppName       = "YOUTUBE_APP_NAME"
	EnvYelpAPIKey           = "YELP_API_KEY"
	EnvYelpClientID         = "YELP_CLIENT_ID"
	EnvGoogleAPIKey         = "GOOGLE_API_KEY"
	EnvGoogleSearchEngineID = "GOOGLE_SEARCH_ENGINE_ID"
	EnvAmazonAPIKey         = "AMAZON_API_KEY"
	EnvAmazonAPISecret      = "AMAZON_API_SECRET"
	EnvAmazonAssociateTag   = "AMAZON_ASSOCIATE_TAG"
	EnvAmazonMarketplaceID  = "AMAZON_MARKETPLACE_ID"
	EnvOpenAIApiKey         = "OPENAI_API_KEY"
	EnvOpenAIModel          = "OPENAI_MODEL"
)

func RunWorker(ctx context.Context, l *slog.Logger, thp, tns string) error {
	return RunWorkerWithOptions(ctx, l, thp, tns, false)
}

// RunWorkerLocal runs the worker in local mode without requiring Solana configuration
func RunWorkerLocal(ctx context.Context, l *slog.Logger, thp, tns string) error {
	return RunWorkerWithOptions(ctx, l, thp, tns, true)
}

// RunWorkerWithOptions runs the worker with the specified options
func RunWorkerWithOptions(ctx context.Context, l *slog.Logger, thp, tns string, localMode bool) error {
	// connect to temporal
	c, err := client.Dial(client.Options{
		Logger:    l,
		HostPort:  thp,
		Namespace: tns,
	})
	if err != nil {
		return fmt.Errorf("couldn't initialize temporal client: %w", err)
	}
	defer c.Close()

	// Get Solana escrow details from environment
	escrowPrivateKeyStr := os.Getenv(EnvSolanaPrivateKey)
	escrowWalletStr := os.Getenv(EnvSolanaEscrowWallet)
	treasuryWalletStr := os.Getenv(EnvSolanaTreasuryWallet)
	usdcMintStr := os.Getenv(EnvSolanaUSDCMint)

	var solanaConfig abb.SolanaConfig

	// Parse and validate provided credentials
	escrowPrivateKey, err := solanago.PrivateKeyFromBase58(escrowPrivateKeyStr)
	if err != nil {
		return fmt.Errorf("failed to parse escrow private key from %s: %w", EnvSolanaPrivateKey, err)
	}

	escrowWallet, err := solanago.PublicKeyFromBase58(escrowWalletStr)
	if err != nil {
		return fmt.Errorf("failed to parse escrow wallet address from %s: %w", EnvSolanaEscrowWallet, err)
	}

	// Validate that the private key corresponds to the wallet address
	if !escrowPrivateKey.PublicKey().Equals(escrowWallet) {
		return fmt.Errorf("escrow private key does not match escrow wallet address")
	}

	// Validate treasury wallet address if provided
	if treasuryWalletStr != "" {
		if _, err := solanago.PublicKeyFromBase58(treasuryWalletStr); err != nil {
			return fmt.Errorf("failed to parse treasury wallet address from %s: %w", EnvSolanaTreasuryWallet, err)
		}
	} else {
		// Decide policy: error out, or just warn?
		// For now, let's warn, as fee transfer logic in workflow already checks.
		l.Warn("Treasury wallet not set in environment", "env_var", EnvSolanaTreasuryWallet)
	}

	// Create Solana config with validated credentials
	solanaConfig = abb.SolanaConfig{
		RPCEndpoint:      os.Getenv(EnvSolanaRPCEndpoint),
		WSEndpoint:       os.Getenv(EnvSolanaWSEndpoint),
		EscrowPrivateKey: &escrowPrivateKey,
		EscrowWallet:     escrowWallet,
		TreasuryWallet:   treasuryWalletStr,
		USDCMintAddress:  usdcMintStr,
	}

	// Create LLM provider
	llmConfig := abb.LLMConfig{
		Provider:    "openai",
		APIKey:      os.Getenv(EnvOpenAIApiKey),
		Model:       os.Getenv(EnvOpenAIModel),
		MaxTokens:   1000,
		Temperature: 0.7,
	}
	llmProvider, err := abb.NewLLMProvider(llmConfig)
	if err != nil {
		return fmt.Errorf("failed to create LLM provider: %w", err)
	}

	// Create dependencies for each platform
	redditDeps := abb.RedditDependencies{
		UserAgent:    os.Getenv(EnvRedditUserAgent),
		Username:     os.Getenv(EnvRedditUsername),
		Password:     os.Getenv(EnvRedditPassword),
		ClientID:     os.Getenv(EnvRedditClientID),
		ClientSecret: os.Getenv(EnvRedditClientSecret),
	}

	youtubeDeps := abb.YouTubeDependencies{
		APIKey:          os.Getenv(EnvYouTubeAPIKey),
		ApplicationName: os.Getenv(EnvYouTubeAppName),
		MaxResults:      100, // Default value, can be adjusted
	}

	yelpDeps := abb.YelpDependencies{
		APIKey:   os.Getenv(EnvYelpAPIKey),
		ClientID: os.Getenv(EnvYelpClientID),
	}

	googleDeps := abb.GoogleDependencies{
		APIKey:         os.Getenv(EnvGoogleAPIKey),
		SearchEngineID: os.Getenv(EnvGoogleSearchEngineID),
	}

	amazonDeps := abb.AmazonDependencies{
		APIKey:        os.Getenv(EnvAmazonAPIKey),
		APISecret:     os.Getenv(EnvAmazonAPISecret),
		AssociateTag:  os.Getenv(EnvAmazonAssociateTag),
		MarketplaceID: os.Getenv(EnvAmazonMarketplaceID),
	}

	llmDeps := abb.LLMDependencies{
		Provider: llmProvider,
	}

	// Create activities with the configured Solana client
	activities, err := abb.NewActivities(
		solanaConfig,
		os.Getenv("SERVER_URL"),
		os.Getenv("AUTH_TOKEN"),
		redditDeps,
		youtubeDeps,
		yelpDeps,
		googleDeps,
		amazonDeps,
		llmDeps,
	)
	if err != nil {
		return fmt.Errorf("failed to create activities: %w", err)
	}

	// Create the single worker
	w := worker.New(c, os.Getenv("TASK_QUEUE"), worker.Options{})

	// Register all workflows
	w.RegisterWorkflow(abb.BountyAssessmentWorkflow)
	w.RegisterWorkflow(abb.PullContentWorkflow)
	w.RegisterWorkflow(abb.CheckContentRequirementsWorkflow)
	w.RegisterWorkflow(abb.PayBountyWorkflow)

	// Register all activities
	w.RegisterActivity(activities.PullRedditContent)
	w.RegisterActivity(activities.PullYouTubeContent)
	w.RegisterActivity(activities.PullYelpContent)
	w.RegisterActivity(activities.PullGoogleContent)
	w.RegisterActivity(activities.PullAmazonContent)
	w.RegisterActivity(activities.CheckContentRequirements)
	w.RegisterActivity(activities.VerifyPayment)
	w.RegisterActivity(activities.TransferUSDC)

	// Run the single worker
	l.Info("Starting worker", "TaskQueue", os.Getenv("TASK_QUEUE"))
	err = w.Run(worker.InterruptCh())
	l.Info("Worker stopped")
	return err
}
