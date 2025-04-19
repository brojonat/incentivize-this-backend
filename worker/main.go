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
	escrowPrivateKeyStr := os.Getenv("SOLANA_ESCROW_PRIVATE_KEY")
	escrowWalletStr := os.Getenv("SOLANA_ESCROW_WALLET")
	usdcMintStr := os.Getenv("SOLANA_USDC_MINT_ADDRESS")

	var solanaConfig abb.SolanaConfig

	// Parse and validate provided credentials
	escrowPrivateKey, err := solanago.PrivateKeyFromBase58(escrowPrivateKeyStr)
	if err != nil {
		return fmt.Errorf("failed to parse escrow private key from ESCROW_PRIVATE_KEY_STR: %w", err)
	}

	escrowWallet, err := solanago.PublicKeyFromBase58(escrowWalletStr)
	if err != nil {
		return fmt.Errorf("failed to parse escrow wallet address from ESCROW_WALLET_STR: %w", err)
	}

	// Validate that the private key corresponds to the wallet address
	if !escrowPrivateKey.PublicKey().Equals(escrowWallet) {
		return fmt.Errorf("escrow private key does not match escrow wallet address")
	}

	// Create Solana config with validated credentials
	solanaConfig = abb.SolanaConfig{
		RPCEndpoint:      os.Getenv("SOLANA_RPC_ENDPOINT"),
		WSEndpoint:       os.Getenv("SOLANA_WS_ENDPOINT"),
		EscrowPrivateKey: &escrowPrivateKey,
		EscrowWallet:     escrowWallet,
		USDCMintAddress:  usdcMintStr,
	}

	// Create LLM provider
	llmConfig := abb.LLMConfig{
		Provider:    "openai",
		APIKey:      os.Getenv("OPENAI_API_KEY"),
		Model:       os.Getenv("OPENAI_MODEL"),
		MaxTokens:   1000,
		Temperature: 0.7,
	}
	llmProvider, err := abb.NewLLMProvider(llmConfig)
	if err != nil {
		return fmt.Errorf("failed to create LLM provider: %w", err)
	}

	// Create dependencies for each platform
	redditDeps := abb.RedditDependencies{
		UserAgent:    os.Getenv("REDDIT_USER_AGENT"),
		Username:     os.Getenv("REDDIT_USERNAME"),
		Password:     os.Getenv("REDDIT_PASSWORD"),
		ClientID:     os.Getenv("REDDIT_CLIENT_ID"),
		ClientSecret: os.Getenv("REDDIT_CLIENT_SECRET"),
	}

	youtubeDeps := abb.YouTubeDependencies{
		APIKey:          os.Getenv("YOUTUBE_API_KEY"),
		ApplicationName: os.Getenv("YOUTUBE_APP_NAME"),
		MaxResults:      100, // Default value, can be adjusted
	}

	yelpDeps := abb.YelpDependencies{
		APIKey:   os.Getenv("YELP_API_KEY"),
		ClientID: os.Getenv("YELP_CLIENT_ID"),
	}

	googleDeps := abb.GoogleDependencies{
		APIKey:         os.Getenv("GOOGLE_API_KEY"),
		SearchEngineID: os.Getenv("GOOGLE_SEARCH_ENGINE_ID"),
	}

	amazonDeps := abb.AmazonDependencies{
		APIKey:        os.Getenv("AMAZON_API_KEY"),
		APISecret:     os.Getenv("AMAZON_API_SECRET"),
		AssociateTag:  os.Getenv("AMAZON_ASSOCIATE_TAG"),
		MarketplaceID: os.Getenv("AMAZON_MARKETPLACE_ID"),
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
