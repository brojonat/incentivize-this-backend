package worker

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/brojonat/affiliate-bounty-board/abb"
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
	w.RegisterWorkflow(abb.PullContentWorkflow)
	w.RegisterWorkflow(abb.CheckContentRequirementsWorkflow)
	w.RegisterWorkflow(abb.PayBountyWorkflow)
	w.RegisterWorkflow(abb.PublishBountiesWorkflow)

	// Register all activities
	w.RegisterActivity(activities.PullRedditContent)
	w.RegisterActivity(activities.PullYouTubeContent)
	w.RegisterActivity(activities.CheckContentRequirements)
	w.RegisterActivity(activities.VerifyPayment)
	w.RegisterActivity(activities.TransferUSDC)
	w.RegisterActivity(activities.PublishBountiesReddit)
	w.RegisterActivity(activities.PullTwitchContent)
	w.RegisterActivity(activities.AnalyzeImageURL)

	// Run the single worker
	l.Info("Starting worker", "TaskQueue", taskQueue)
	err = w.Run(worker.InterruptCh())
	l.Info("Worker stopped")
	return err
}
