package worker

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/brojonat/reddit-bounty-board/rbb"
	"github.com/brojonat/reddit-bounty-board/solana"
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

	// Get Solana private key from environment
	privateKeyStr := os.Getenv("SOLANA_ESCROW_PRIVATE_KEY")
	keypairPath := os.Getenv("SOLANA_ESCROW_KEYPAIR")
	tokenAccountStr := os.Getenv("SOLANA_ESCROW_TOKEN_ACCOUNT")

	var solanaConfig solana.SolanaConfig

	// Check if Solana credentials are provided
	if (privateKeyStr == "" && keypairPath == "") || tokenAccountStr == "" {
		l.Warn("Solana credentials not provided. Solana functionality will be limited.",
			"private_key", privateKeyStr != "",
			"keypair_file", keypairPath != "",
			"token_account", tokenAccountStr != "")

		// Use dummy values for fields that require non-nil values
		dummyKey := solanago.NewWallet().PrivateKey
		dummyAccount := dummyKey.PublicKey()

		solanaConfig = solana.SolanaConfig{
			RPCEndpoint:        os.Getenv("SOLANA_RPC_ENDPOINT"),
			WSEndpoint:         os.Getenv("SOLANA_WS_ENDPOINT"),
			EscrowPrivateKey:   &dummyKey,
			EscrowTokenAccount: dummyAccount,
		}
	} else {
		var escrowPrivateKey solanago.PrivateKey

		// If privateKeyStr is provided, use it
		if privateKeyStr != "" {
			escrowPrivateKey, err = solanago.PrivateKeyFromBase58(privateKeyStr)
			if err != nil {
				return fmt.Errorf("failed to parse escrow private key: %w", err)
			}
		} else {
			// Otherwise, load from keypair file
			keypairData, err := os.ReadFile(keypairPath)
			if err != nil {
				return fmt.Errorf("failed to read keypair file: %w", err)
			}

			// Parse the keypair file (expected format: JSON array of numbers)
			keypairStr := string(keypairData)
			// Remove brackets, commas, and trailing characters
			keypairStr = strings.ReplaceAll(keypairStr, "[", "")
			keypairStr = strings.ReplaceAll(keypairStr, "]", "")
			keypairStr = strings.ReplaceAll(keypairStr, ",", " ")
			keypairStr = strings.SplitN(keypairStr, "%", 2)[0] // Remove trailing % if present

			// Split into numbers and convert to bytes
			var keypairBytes []byte
			for _, numStr := range strings.Fields(keypairStr) {
				num := 0
				_, err := fmt.Sscanf(numStr, "%d", &num)
				if err != nil {
					return fmt.Errorf("failed to parse keypair data: %w", err)
				}
				keypairBytes = append(keypairBytes, byte(num))
			}

			// Use first 32 bytes as private key
			if len(keypairBytes) < 32 {
				return fmt.Errorf("invalid keypair data: too short")
			}

			// Create private key
			escrowPrivateKey = solanago.PrivateKey(keypairBytes[:32])
		}

		escrowTokenAccount, err := solanago.PublicKeyFromBase58(tokenAccountStr)
		if err != nil {
			return fmt.Errorf("failed to parse escrow token account: %w", err)
		}

		// Create Solana config
		solanaConfig = solana.SolanaConfig{
			RPCEndpoint:        os.Getenv("SOLANA_RPC_ENDPOINT"),
			WSEndpoint:         os.Getenv("SOLANA_WS_ENDPOINT"),
			EscrowPrivateKey:   &escrowPrivateKey,
			EscrowTokenAccount: escrowTokenAccount,
		}
	}

	// Create LLM provider
	llmConfig := rbb.LLMConfig{
		Provider:    "openai",
		APIKey:      os.Getenv("OPENAI_API_KEY"),
		Model:       os.Getenv("OPENAI_MODEL"),
		MaxTokens:   1000,
		Temperature: 0.7,
	}
	llmProvider, err := rbb.NewLLMProvider(llmConfig)
	if err != nil {
		return fmt.Errorf("failed to create LLM provider: %w", err)
	}

	// Create dependencies for each platform
	redditDeps := rbb.RedditDependencies{
		UserAgent:    os.Getenv("REDDIT_USER_AGENT"),
		Username:     os.Getenv("REDDIT_USERNAME"),
		Password:     os.Getenv("REDDIT_PASSWORD"),
		ClientID:     os.Getenv("REDDIT_CLIENT_ID"),
		ClientSecret: os.Getenv("REDDIT_CLIENT_SECRET"),
	}

	youtubeDeps := rbb.YouTubeDependencies{
		APIKey:          os.Getenv("YOUTUBE_API_KEY"),
		ApplicationName: os.Getenv("YOUTUBE_APP_NAME"),
		MaxResults:      100, // Default value, can be adjusted
	}

	yelpDeps := rbb.YelpDependencies{
		APIKey:   os.Getenv("YELP_API_KEY"),
		ClientID: os.Getenv("YELP_CLIENT_ID"),
	}

	googleDeps := rbb.GoogleDependencies{
		APIKey:         os.Getenv("GOOGLE_API_KEY"),
		SearchEngineID: os.Getenv("GOOGLE_SEARCH_ENGINE_ID"),
	}

	llmDeps := rbb.LLMDependencies{
		Provider: llmProvider,
	}

	// Create activities
	activities := rbb.NewActivities(
		solanaConfig,
		os.Getenv("SERVER_URL"),
		os.Getenv("AUTH_TOKEN"),
		redditDeps,
		youtubeDeps,
		yelpDeps,
		googleDeps,
		llmDeps,
	)

	// create worker
	w := worker.New(c, rbb.TaskQueueName, worker.Options{})

	// register workflows and activities
	w.RegisterWorkflow(rbb.BountyAssessmentWorkflow)
	w.RegisterWorkflow(rbb.PullContentWorkflow)
	w.RegisterWorkflow(rbb.CheckContentRequirementsWorkflow)
	w.RegisterWorkflow(rbb.PayBountyWorkflow)
	w.RegisterWorkflow(rbb.ReturnBountyToOwnerWorkflow)

	// register activities
	w.RegisterActivity(activities.PullRedditContent)
	w.RegisterActivity(activities.PullYouTubeContent)
	w.RegisterActivity(activities.PullYelpContent)
	w.RegisterActivity(activities.PullGoogleContent)
	w.RegisterActivity(activities.CheckContentRequirements)

	// Only register Solana-related activities if not in local mode
	if !localMode {
		w.RegisterActivity(activities.TransferUSDC)
		w.RegisterActivity(activities.ReleaseEscrow)
		w.RegisterActivity(activities.PayBounty)
		w.RegisterActivity(activities.ReturnBountyToOwner)
	}

	// run worker
	return w.Run(worker.InterruptCh())
}
