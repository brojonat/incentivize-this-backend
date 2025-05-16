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

// RunWorker runs the worker with the specified options
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

	// Register all activities
	w.RegisterActivity(activities.PullContentActivity)
	w.RegisterActivity(activities.CheckContentRequirements)
	w.RegisterActivity(activities.ValidatePayoutWallet)
	w.RegisterActivity(activities.VerifyPayment)
	w.RegisterActivity(activities.TransferUSDC)
	w.RegisterActivity(activities.PublishBountiesReddit)
	w.RegisterActivity(activities.AnalyzeImageURL)
	w.RegisterActivity(activities.SendTokenEmail)

	// Run the single worker
	l.Info("Starting worker", "TaskQueue", taskQueue)
	err = w.Run(worker.InterruptCh())
	l.Info("Worker stopped")
	return err
}
