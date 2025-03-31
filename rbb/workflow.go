package rbb

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/brojonat/reddit-bounty-board/solana"
	solanago "github.com/gagliardetto/solana-go"
	"github.com/google/uuid"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// TaskQueueName is the name of the task queue for all workflows
const TaskQueueName = "reddit-bounty-board"

// BountyAssessmentSignal represents the signal that will be sent to assess a Reddit post
type BountyAssessmentSignal struct {
	RedditContentID string
	UserID          string
}

// CancelBountySignal represents the signal to cancel the bounty and return remaining funds
type CancelBountySignal struct {
	OwnerID string
}

// BountyAssessmentWorkflowInput represents the input parameters for the workflow
type BountyAssessmentWorkflowInput struct {
	RequirementsDescription string
	BountyPerPost           *solana.USDCAmount
	TotalBounty             *solana.USDCAmount
	OwnerID                 string
	SolanaWallet            string
	USDCAccount             string
	ServerURL               string
	AuthToken               string
	Timeout                 time.Duration // How long the bounty should remain active
}

// BountyAssessmentWorkflow represents the workflow that manages bounty assessment
func BountyAssessmentWorkflow(ctx workflow.Context, input BountyAssessmentWorkflowInput) error {
	// Set activity options
	options := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    time.Minute,
			MaximumAttempts:    3,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, options)

	// Create signal channels
	assessmentChan := workflow.GetSignalChannel(ctx, "assessment")
	cancelChan := workflow.GetSignalChannel(ctx, "cancel")

	// Create selector for handling signals
	selector := workflow.NewSelector(ctx)

	// Initialize remaining bounty
	remainingBounty := input.TotalBounty

	// Create activities instance
	activities := &Activities{
		httpClient: &http.Client{Timeout: 10 * time.Second},
		serverURL:  input.ServerURL,
		authToken:  input.AuthToken,
	}

	// Add assessment signal handler
	selector.AddReceive(assessmentChan, func(c workflow.ReceiveChannel, more bool) {
		var assessmentSignal BountyAssessmentSignal
		c.Receive(ctx, &assessmentSignal)

		// Check if we have enough remaining bounty
		if remainingBounty.Cmp(input.BountyPerPost) < 0 {
			workflow.GetLogger(ctx).Error("Insufficient remaining bounty")
			return
		}

		// Check content requirements
		var result CheckContentRequirementsResult
		err := workflow.ExecuteActivity(ctx, activities.CheckContentRequirements, assessmentSignal.RedditContentID, input.RequirementsDescription).Get(ctx, &result)
		if err != nil {
			workflow.GetLogger(ctx).Error("Failed to check content requirements", "error", err)
			return
		}

		// If requirements are met, pay the bounty
		if result.Satisfies {
			err := workflow.ExecuteActivity(ctx, activities.PayBounty, assessmentSignal.UserID, input.BountyPerPost.ToUSDC()).Get(ctx, nil)
			if err != nil {
				workflow.GetLogger(ctx).Error("Failed to pay bounty", "error", err)
				return
			}

			// Update remaining bounty
			remainingBounty = remainingBounty.Sub(input.BountyPerPost)

			// Log the payment
			workflow.GetLogger(ctx).Info("Bounty paid successfully",
				"user_id", assessmentSignal.UserID,
				"amount", input.BountyPerPost.ToUSDC(),
				"remaining", remainingBounty.ToUSDC())
		} else {
			workflow.GetLogger(ctx).Info("Content did not meet requirements",
				"user_id", assessmentSignal.UserID,
				"reason", result.Reason)
		}
	})

	// Add cancellation signal handler
	selector.AddReceive(cancelChan, func(c workflow.ReceiveChannel, more bool) {
		var cancelSignal CancelBountySignal
		c.Receive(ctx, &cancelSignal)

		// Verify the owner ID matches
		if cancelSignal.OwnerID != input.OwnerID {
			workflow.GetLogger(ctx).Error("Invalid owner ID in cancellation signal")
			return
		}

		// Return remaining bounty to owner
		if !remainingBounty.IsZero() {
			err := workflow.ExecuteActivity(ctx, activities.ReturnBountyToOwner, input.OwnerID, remainingBounty.ToUSDC()).Get(ctx, nil)
			if err != nil {
				workflow.GetLogger(ctx).Error("Failed to return bounty to owner", "error", err)
				return
			}

			// Log the cancellation
			workflow.GetLogger(ctx).Info("Bounty cancelled and remaining funds returned to owner",
				"owner_id", input.OwnerID,
				"remaining_amount", remainingBounty.ToUSDC())

			// Set remaining bounty to zero to exit the loop
			remainingBounty = solana.Zero()
		}
	})

	// Create a timeout future
	timeoutFuture := workflow.NewTimer(ctx, input.Timeout)
	selector.AddFuture(timeoutFuture, func(f workflow.Future) {
		_ = f.Get(ctx, nil)
		if !remainingBounty.IsZero() {
			err := workflow.ExecuteActivity(ctx, activities.ReturnBountyToOwner, input.OwnerID, remainingBounty.ToUSDC()).Get(ctx, nil)
			if err != nil {
				workflow.GetLogger(ctx).Error("Failed to return remaining bounty to owner", "error", err)
				return
			}

			// Log the timeout
			workflow.GetLogger(ctx).Info("Bounty timed out and remaining funds returned to owner",
				"owner_id", input.OwnerID,
				"remaining_amount", remainingBounty.ToUSDC())

			// Set remaining bounty to zero to exit the loop
			remainingBounty = solana.Zero()
		}
	})

	// Wait for signals until remaining bounty is zero
	for !remainingBounty.IsZero() {
		selector.Select(ctx)
	}

	return nil
}

// PullRedditContentWorkflow represents the workflow that pulls content from Reddit
func PullRedditContentWorkflow(ctx workflow.Context, deps RedditDependencies, contentID string) (string, error) {
	options := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    time.Minute,
			MaximumAttempts:    3,
		},
	}

	ctx = workflow.WithActivityOptions(ctx, options)

	// Create activities instance
	activities := &Activities{
		redditDeps: deps,
	}

	var result string
	err := workflow.ExecuteActivity(ctx, activities.PullRedditContent, contentID).Get(ctx, &result)
	if err != nil {
		return "", err
	}
	return result, nil
}

// CheckContentRequirementsResult represents the result of checking content requirements
type CheckContentRequirementsResult struct {
	Satisfies bool
	Reason    string
}

// CheckContentRequirementsWorkflow represents the workflow that checks if content satisfies requirements
func CheckContentRequirementsWorkflow(ctx workflow.Context, content, requirements string) (CheckContentRequirementsResult, error) {
	options := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    time.Minute,
			MaximumAttempts:    3,
		},
	}

	ctx = workflow.WithActivityOptions(ctx, options)

	// Create activities instance
	activities := &Activities{}

	var result CheckContentRequirementsResult
	err := workflow.ExecuteActivity(ctx, activities.CheckContentRequirements, content, requirements).Get(ctx, &result)
	if err != nil {
		return CheckContentRequirementsResult{}, err
	}
	return result, nil
}

// SolanaDependencies contains the Solana client and configuration
type SolanaDependencies struct {
	Client solana.SolanaClient
}

// Workflow represents a workflow instance
type Workflow struct {
	client client.Client
}

// NewWorkflow creates a new workflow instance
func NewWorkflow(c client.Client) *Workflow {
	return &Workflow{client: c}
}

// PayBountyWorkflowInput represents the input parameters for the pay bounty workflow
type PayBountyWorkflowInput struct {
	FromAccount  string
	ToAccount    string
	Amount       *solana.USDCAmount
	SolanaConfig solana.SolanaConfig
}

// ReturnBountyToOwnerWorkflowInput represents the input parameters for the return bounty workflow
type ReturnBountyToOwnerWorkflowInput struct {
	ToAccount    string
	Amount       *solana.USDCAmount
	SolanaConfig solana.SolanaConfig
}

// PayBounty executes the pay bounty workflow
func (w *Workflow) PayBounty(ctx context.Context, input PayBountyWorkflowInput) error {
	workflowID := fmt.Sprintf("pay-bounty-%s", uuid.New().String())
	workflowOptions := client.StartWorkflowOptions{
		ID:        workflowID,
		TaskQueue: TaskQueueName,
	}

	we, err := w.client.ExecuteWorkflow(ctx, workflowOptions, PayBountyWorkflow, input)
	if err != nil {
		return fmt.Errorf("failed to start workflow: %w", err)
	}

	// Wait for workflow completion
	if err := we.Get(ctx, nil); err != nil {
		return fmt.Errorf("workflow failed: %w", err)
	}

	return nil
}

// ReturnBountyToOwner executes the return bounty workflow
func (w *Workflow) ReturnBountyToOwner(ctx context.Context, input ReturnBountyToOwnerWorkflowInput) error {
	workflowID := fmt.Sprintf("return-bounty-%s", uuid.New().String())
	workflowOptions := client.StartWorkflowOptions{
		ID:        workflowID,
		TaskQueue: TaskQueueName,
	}

	we, err := w.client.ExecuteWorkflow(ctx, workflowOptions, ReturnBountyToOwnerWorkflow, input)
	if err != nil {
		return fmt.Errorf("failed to start workflow: %w", err)
	}

	// Wait for workflow completion
	if err := we.Get(ctx, nil); err != nil {
		return fmt.Errorf("workflow failed: %w", err)
	}

	return nil
}

// PayBountyWorkflow represents the workflow that pays a bounty
func PayBountyWorkflow(ctx workflow.Context, input PayBountyWorkflowInput) error {
	options := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    time.Minute,
			MaximumAttempts:    3,
		},
	}

	ctx = workflow.WithActivityOptions(ctx, options)

	// Convert addresses to PublicKey
	fromAccount, err := solanago.PublicKeyFromBase58(input.FromAccount)
	if err != nil {
		return fmt.Errorf("invalid from account: %w", err)
	}

	toAccount, err := solanago.PublicKeyFromBase58(input.ToAccount)
	if err != nil {
		return fmt.Errorf("invalid to account: %w", err)
	}

	// Create activities instance
	activities := &Activities{
		solanaConfig: input.SolanaConfig,
	}

	// Execute transfer activity
	err = workflow.ExecuteActivity(ctx, activities.TransferUSDC, fromAccount, toAccount, input.Amount).Get(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to transfer USDC: %w", err)
	}

	return nil
}

// ReturnBountyToOwnerWorkflow represents the workflow that returns a bounty to its owner
func ReturnBountyToOwnerWorkflow(ctx workflow.Context, input ReturnBountyToOwnerWorkflowInput) error {
	options := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    time.Minute,
			MaximumAttempts:    3,
		},
	}

	ctx = workflow.WithActivityOptions(ctx, options)

	// Create activities instance
	activities := &Activities{
		httpClient: &http.Client{Timeout: 10 * time.Second},
		serverURL:  input.SolanaConfig.RPCEndpoint,
		authToken:  "test-token", // TODO: Get this from input
	}

	// Execute return bounty activity
	err := workflow.ExecuteActivity(ctx, activities.ReturnBountyToOwner, input.ToAccount, input.Amount.ToUSDC()).Get(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to return bounty: %w", err)
	}

	return nil
}
