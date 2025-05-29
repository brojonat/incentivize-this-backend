package worker

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/brojonat/affiliate-bounty-board/abb"
	"go.temporal.io/sdk/client"
	sdklog "go.temporal.io/sdk/log"
	"go.temporal.io/sdk/worker"
)

// RunWorker runs the worker with the specified options
func RunWorker(ctx context.Context, l *slog.Logger, thp, tns string) error {
	// --- Create a new slog.Logger for Temporal, configured to LevelWarn ---
	temporalSlogHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelWarn,
	})
	temporalLogger := sdklog.NewStructuredLogger(slog.New(temporalSlogHandler))
	// --- End Temporal logger setup ---

	// connect to temporal with retries
	var c client.Client
	var err error
	maxRetries := 5
	retryInterval := 5 * time.Second

	for i := 0; i < maxRetries; i++ {
		c, err = client.Dial(client.Options{
			Logger:    temporalLogger,
			HostPort:  thp,
			Namespace: tns,
		})
		if err == nil {
			l.Info("Successfully connected to Temporal", "address", thp, "namespace", tns)
			break
		}
		l.Error("Failed to connect to Temporal", "attempt", i+1, "max_attempts", maxRetries, "error", err)
		if i < maxRetries-1 {
			l.Info("Retrying Temporal connection", "interval", retryInterval)
			time.Sleep(retryInterval)
		}
	}
	if err != nil {
		return fmt.Errorf("couldn't initialize temporal client after %d attempts: %w", maxRetries, err)
	}
	defer c.Close()

	// Create activities struct (now parameterless)
	activities, err := abb.NewActivities()
	if err != nil {
		return fmt.Errorf("failed to create activities: %w", err)
	}

	// Create the single worker
	taskQueue := os.Getenv("TASK_QUEUE")
	if taskQueue == "" {
		return fmt.Errorf("TASK_QUEUE environment variable not set")
	}
	w := worker.New(c, taskQueue, worker.Options{})

	// Register all workflows
	w.RegisterWorkflow(abb.BountyAssessmentWorkflow)
	w.RegisterWorkflow(abb.CheckContentRequirementsWorkflow)
	w.RegisterWorkflow(abb.PayBountyWorkflow)
	w.RegisterWorkflow(abb.PublishBountiesWorkflow)
	w.RegisterWorkflow(abb.EmailTokenWorkflow)
	w.RegisterWorkflow(abb.PruneStaleEmbeddingsWorkflow)

	// Register all activities
	w.RegisterActivity(activities.GenerateAndStoreBountyEmbeddingActivity)
	w.RegisterActivity(activities.PullContentActivity)
	w.RegisterActivity(activities.CheckContentRequirements)
	w.RegisterActivity(activities.ValidatePayoutWallet)
	w.RegisterActivity(activities.VerifyPayment)
	w.RegisterActivity(activities.TransferUSDC)
	w.RegisterActivity(activities.PublishBountiesReddit)
	w.RegisterActivity(activities.PublishBountiesDiscord)
	w.RegisterActivity(activities.AnalyzeImageURL)
	w.RegisterActivity(activities.ShouldPerformImageAnalysisActivity)
	w.RegisterActivity(activities.SendTokenEmail)
	w.RegisterActivity(activities.SummarizeAndStoreBountyActivity)
	w.RegisterActivity(activities.PruneStaleEmbeddingsActivity)

	// Run the single worker
	l.Info("Starting worker", "TaskQueue", taskQueue)
	err = w.Run(worker.InterruptCh())
	l.Info("Worker stopped")
	return err
}
