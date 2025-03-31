package worker

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/brojonat/reddit-bounty-board/rbb"
	"github.com/brojonat/reddit-bounty-board/solana"
	solanago "github.com/gagliardetto/solana-go"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

func RunWorker(ctx context.Context, l *slog.Logger, thp, tns string) error {
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

	// Parse Solana keys
	escrowPrivateKey, err := solanago.PrivateKeyFromBase58(os.Getenv("SOLANA_ESCROW_PRIVATE_KEY"))
	if err != nil {
		return fmt.Errorf("failed to parse escrow private key: %w", err)
	}

	escrowTokenAccount, err := solanago.PublicKeyFromBase58(os.Getenv("SOLANA_ESCROW_TOKEN_ACCOUNT"))
	if err != nil {
		return fmt.Errorf("failed to parse escrow token account: %w", err)
	}

	// Create Solana config
	solanaConfig := solana.SolanaConfig{
		RPCEndpoint:        os.Getenv("SOLANA_RPC_URL"),
		WSEndpoint:         os.Getenv("SOLANA_WS_URL"),
		EscrowPrivateKey:   &escrowPrivateKey,
		EscrowTokenAccount: escrowTokenAccount,
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

	// Create activities
	activities := rbb.NewActivities(
		solanaConfig,
		os.Getenv("SERVER_URL"),
		os.Getenv("AUTH_TOKEN"),
		rbb.RedditDependencies{
			UserAgent:    os.Getenv("REDDIT_USER_AGENT"),
			Username:     os.Getenv("REDDIT_USERNAME"),
			Password:     os.Getenv("REDDIT_PASSWORD"),
			ClientID:     os.Getenv("REDDIT_CLIENT_ID"),
			ClientSecret: os.Getenv("REDDIT_CLIENT_SECRET"),
		},
		rbb.LLMDependencies{
			Provider: llmProvider,
		},
	)

	// create worker
	w := worker.New(c, rbb.TaskQueueName, worker.Options{})

	// register workflows and activities
	w.RegisterWorkflow(rbb.BountyAssessmentWorkflow)
	w.RegisterWorkflow(rbb.PullRedditContentWorkflow)
	w.RegisterWorkflow(rbb.CheckContentRequirementsWorkflow)
	w.RegisterWorkflow(rbb.PayBountyWorkflow)
	w.RegisterWorkflow(rbb.ReturnBountyToOwnerWorkflow)

	// register activities
	w.RegisterActivity(activities.PullRedditContent)
	w.RegisterActivity(activities.CheckContentRequirements)
	w.RegisterActivity(activities.TransferUSDC)
	w.RegisterActivity(activities.ReleaseEscrow)

	// run worker
	return w.Run(worker.InterruptCh())
}
