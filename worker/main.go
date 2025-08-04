package worker

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/brojonat/affiliate-bounty-board/abb"
	"go.temporal.io/sdk/client"
	sdklog "go.temporal.io/sdk/log"
	"go.temporal.io/sdk/worker"
)

// RunWorker runs the worker with the specified options
func RunWorker(ctx context.Context, l *slog.Logger, thp, tns, tq string) error {
	// Use the logger passed into the function for the Temporal client.
	temporalLogger := sdklog.NewStructuredLogger(l)

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
	w := worker.New(c, tq, worker.Options{})

	// Register all workflows
	w.RegisterWorkflow(abb.BountyAssessmentWorkflow)
	w.RegisterWorkflow(abb.OrchestratorWorkflow)
	w.RegisterWorkflow(abb.PruneStaleEmbeddingsWorkflow)
	w.RegisterWorkflow(abb.GumroadNotifyWorkflow)
	w.RegisterWorkflow(abb.ContactUsNotifyWorkflow)
	w.RegisterWorkflow(abb.EmailTokenWorkflow)
	w.RegisterWorkflow(abb.PollSolanaTransactionsWorkflow)

	// Register all activities on the activities struct
	w.RegisterActivity(activities)

	// Run the single worker
	l.Info("Starting worker", "TaskQueue", tq)
	err = w.Run(worker.InterruptCh())
	l.Info("Worker stopped")
	return err
}

// CheckConnection attempts to connect to Temporal and returns an error if it fails.
// This is intended for use in health checks.
func CheckConnection(ctx context.Context, l *slog.Logger, thp, tns string) error {
	// Use the logger passed into the function for the Temporal client.
	temporalLogger := sdklog.NewStructuredLogger(l)

	// For a health check, we just need to dial and then close.
	c, err := client.Dial(client.Options{
		Logger:    temporalLogger,
		HostPort:  thp,
		Namespace: tns,
	})
	if err != nil {
		return fmt.Errorf("health check failed to connect to Temporal: %w", err)
	}
	c.Close()

	l.Info("Health check successful: connected to Temporal")
	return nil
}
