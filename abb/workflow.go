package abb

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/brojonat/affiliate-bounty-board/solana"
	solanago "github.com/gagliardetto/solana-go"
	"github.com/google/uuid"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// TaskQueueName is the name of the task queue for all workflows
const TaskQueueName = "affiliate-bounty-board"

// BountyAssessmentSignal represents the signal that will be sent to assess content from a platform
type BountyAssessmentSignal struct {
	ContentID string       // ID of the content on the platform
	UserID    string       // User ID who submitted the content
	Platform  PlatformType // Platform the content is from
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
	PlatformType            PlatformType        // The platform type (Reddit, YouTube, etc.)
	PlatformDependencies    interface{}         // Platform-specific dependencies
	Timeout                 time.Duration       // How long the bounty should remain active
	PaymentTimeout          time.Duration       // How long to wait for payment verification
	SolanaConfig            solana.SolanaConfig // Solana configuration
}

// BountyAssessmentWorkflow represents the workflow that manages bounty assessment
func BountyAssessmentWorkflow(ctx workflow.Context, input BountyAssessmentWorkflowInput) error {
	options := workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Second, // Shorter timeout for testing
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    time.Minute,
			MaximumAttempts:    3,
		},
	}

	ctx = workflow.WithActivityOptions(ctx, options)

	// Create activities instance
	var redditDeps RedditDependencies
	var youtubeDeps YouTubeDependencies
	var yelpDeps YelpDependencies
	var googleDeps GoogleDependencies
	var amazonDeps AmazonDependencies

	// Only cast to the specified platform type
	switch input.PlatformType {
	case PlatformReddit:
		data, err := json.Marshal(input.PlatformDependencies)
		if err != nil {
			return fmt.Errorf("failed to marshal Reddit dependencies: %w", err)
		}
		if err := json.Unmarshal(data, &redditDeps); err != nil {
			return fmt.Errorf("failed to unmarshal Reddit dependencies: %w", err)
		}
	case PlatformYouTube:
		data, err := json.Marshal(input.PlatformDependencies)
		if err != nil {
			return fmt.Errorf("failed to marshal YouTube dependencies: %w", err)
		}
		if err := json.Unmarshal(data, &youtubeDeps); err != nil {
			return fmt.Errorf("failed to unmarshal YouTube dependencies: %w", err)
		}
	case PlatformYelp:
		data, err := json.Marshal(input.PlatformDependencies)
		if err != nil {
			return fmt.Errorf("failed to marshal Yelp dependencies: %w", err)
		}
		if err := json.Unmarshal(data, &yelpDeps); err != nil {
			return fmt.Errorf("failed to unmarshal Yelp dependencies: %w", err)
		}
	case PlatformGoogle:
		data, err := json.Marshal(input.PlatformDependencies)
		if err != nil {
			return fmt.Errorf("failed to marshal Google dependencies: %w", err)
		}
		if err := json.Unmarshal(data, &googleDeps); err != nil {
			return fmt.Errorf("failed to unmarshal Google dependencies: %w", err)
		}
	case PlatformAmazon:
		data, err := json.Marshal(input.PlatformDependencies)
		if err != nil {
			return fmt.Errorf("failed to marshal Amazon dependencies: %w", err)
		}
		if err := json.Unmarshal(data, &amazonDeps); err != nil {
			return fmt.Errorf("failed to unmarshal Amazon dependencies: %w", err)
		}
	default:
		return fmt.Errorf("unsupported platform type: %s", input.PlatformType)
	}

	activities, err := NewActivities(
		input.SolanaConfig,
		input.ServerURL,
		input.AuthToken,
		redditDeps,
		youtubeDeps,
		yelpDeps,
		googleDeps,
		amazonDeps,
		LLMDependencies{},
	)
	if err != nil {
		return fmt.Errorf("failed to create activities: %w", err)
	}

	// Convert wallet address to PublicKey
	fromAccount, err := solanago.PublicKeyFromBase58(input.SolanaWallet)
	if err != nil {
		return fmt.Errorf("invalid wallet address: %w", err)
	}

	// Verify payment has been received
	workflow.GetLogger(ctx).Info("Waiting for payment verification")
	var verifyResult *VerifyPaymentResult
	err = workflow.ExecuteActivity(ctx, activities.VerifyPayment, fromAccount, input.TotalBounty, input.PaymentTimeout).Get(ctx, &verifyResult)
	if err != nil {
		return fmt.Errorf("failed to verify payment: %w", err)
	}

	if !verifyResult.Verified {
		return fmt.Errorf("payment verification failed: %s", verifyResult.Error)
	}

	workflow.GetLogger(ctx).Info("Payment verified successfully",
		"amount", verifyResult.Amount.ToUSDC())

	// Initialize remaining bounty
	remainingBounty := input.TotalBounty

	// Create signal channels
	assessmentChan := workflow.GetSignalChannel(ctx, "assessment")
	cancelChan := workflow.GetSignalChannel(ctx, "cancel")

	// Create selector for handling signals
	selector := workflow.NewSelector(ctx)

	// Add assessment signal handler
	selector.AddReceive(assessmentChan, func(c workflow.ReceiveChannel, more bool) {
		var assessmentSignal BountyAssessmentSignal
		c.Receive(ctx, &assessmentSignal)

		// Check if we have enough remaining bounty
		if remainingBounty.Cmp(input.BountyPerPost) < 0 {
			workflow.GetLogger(ctx).Error("Insufficient remaining bounty")
			return
		}

		// Validate platform type
		switch assessmentSignal.Platform {
		case PlatformReddit, PlatformYouTube:
			// Valid platform type
		default:
			workflow.GetLogger(ctx).Error("Invalid platform_type", "platform_type", assessmentSignal.Platform)
			return
		}

		// Pull content from the appropriate platform
		contentInput := PullContentWorkflowInput{
			PlatformType: assessmentSignal.Platform,
			ContentID:    assessmentSignal.ContentID,
			Dependencies: input.PlatformDependencies,
			SolanaConfig: input.SolanaConfig,
		}

		var content string
		err := workflow.ExecuteChildWorkflow(ctx, PullContentWorkflow, contentInput).Get(ctx, &content)
		if err != nil {
			workflow.GetLogger(ctx).Error("Failed to pull content", "error", err)
			return
		}

		// Check content requirements
		var result CheckContentRequirementsResult
		err = workflow.ExecuteActivity(ctx, activities.CheckContentRequirements, content, input.RequirementsDescription).Get(ctx, &result)
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

// PlatformDependencies is an interface for platform-specific dependencies
type PlatformDependencies interface {
	// Type returns the platform type
	Type() PlatformType
}

// ContentProvider is an interface for retrieving content from a platform
type ContentProvider interface {
	// PullContent pulls content from a platform given a content ID
	PullContent(ctx context.Context, contentID string) (string, error)
}

// RedditDependencies implements PlatformDependencies for Reddit
func (deps RedditDependencies) Type() PlatformType {
	return PlatformReddit
}

// PullContentWorkflowInput represents the input parameters for the workflow
type PullContentWorkflowInput struct {
	PlatformType PlatformType
	ContentID    string
	Dependencies interface{}         // Platform-specific dependencies (will be cast to the appropriate type)
	SolanaConfig solana.SolanaConfig // Solana configuration
}

// PullContentWorkflow represents the workflow that pulls content from a platform
func PullContentWorkflow(ctx workflow.Context, input PullContentWorkflowInput) (string, error) {
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
	activities, err := NewActivities(
		input.SolanaConfig,
		"http://test-server",
		"test-token",
		RedditDependencies{},
		YouTubeDependencies{},
		YelpDependencies{},
		GoogleDependencies{},
		AmazonDependencies{},
		LLMDependencies{},
	)
	if err != nil {
		return "", fmt.Errorf("failed to create activities: %w", err)
	}

	// Pull content based on platform type
	switch input.PlatformType {
	case PlatformReddit:
		var redditContent *RedditContent
		err = workflow.ExecuteActivity(ctx, activities.PullRedditContent, input.ContentID).Get(ctx, &redditContent)
		if err != nil {
			return "", fmt.Errorf("failed to pull Reddit content: %w", err)
		}
		return formatRedditContent(redditContent), nil
	case PlatformYouTube:
		var youtubeContent *YouTubeContent
		err = workflow.ExecuteActivity(ctx, activities.PullYouTubeContent, input.ContentID).Get(ctx, &youtubeContent)
		if err != nil {
			return "", fmt.Errorf("failed to pull YouTube content: %w", err)
		}
		return formatYouTubeContent(youtubeContent), nil
	case PlatformYelp:
		var content string
		err = workflow.ExecuteActivity(ctx, activities.PullYelpContent, input.ContentID).Get(ctx, &content)
		if err != nil {
			return "", fmt.Errorf("failed to pull Yelp content: %w", err)
		}
		return content, nil
	case PlatformGoogle:
		var content string
		err = workflow.ExecuteActivity(ctx, activities.PullGoogleContent, input.ContentID).Get(ctx, &content)
		if err != nil {
			return "", fmt.Errorf("failed to pull Google content: %w", err)
		}
		return content, nil
	case PlatformAmazon:
		var content string
		err = workflow.ExecuteActivity(ctx, activities.PullAmazonContent, input.ContentID).Get(ctx, &content)
		if err != nil {
			return "", fmt.Errorf("failed to pull Amazon content: %w", err)
		}
		return content, nil
	default:
		return "", fmt.Errorf("unsupported platform type: %s", input.PlatformType)
	}
}

// formatRedditContent formats a RedditContent struct into a string
func formatRedditContent(content *RedditContent) string {
	if content == nil {
		return ""
	}

	var parts []string
	if content.Title != "" {
		parts = append(parts, fmt.Sprintf("Title: %s", content.Title))
	}
	if content.Selftext != "" {
		parts = append(parts, fmt.Sprintf("Content: %s", content.Selftext))
	}
	if content.Body != "" {
		parts = append(parts, fmt.Sprintf("Body: %s", content.Body))
	}
	if content.Author != "" {
		parts = append(parts, fmt.Sprintf("Author: %s", content.Author))
	}
	if content.Subreddit != "" {
		parts = append(parts, fmt.Sprintf("Subreddit: %s", content.Subreddit))
	}
	if content.URL != "" {
		parts = append(parts, fmt.Sprintf("URL: %s", content.URL))
	}

	return strings.Join(parts, "\n")
}

// formatYouTubeContent formats a YouTubeContent struct into a string
func formatYouTubeContent(content *YouTubeContent) string {
	if content == nil {
		return ""
	}

	var parts []string
	if content.Title != "" {
		parts = append(parts, fmt.Sprintf("Title: %s", content.Title))
	}
	if content.Description != "" {
		parts = append(parts, fmt.Sprintf("Description: %s", content.Description))
	}
	if content.ChannelTitle != "" {
		parts = append(parts, fmt.Sprintf("Channel: %s", content.ChannelTitle))
	}

	// Add captions if available
	for _, caption := range content.Captions {
		if caption.Content != "" {
			parts = append(parts, fmt.Sprintf("Caption (%s): %s", caption.Language, caption.Content))
		}
	}

	return strings.Join(parts, "\n")
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
	activities, err := NewActivities(
		input.SolanaConfig,
		"http://test-server",
		"test-token",
		RedditDependencies{},
		YouTubeDependencies{},
		YelpDependencies{},
		GoogleDependencies{},
		AmazonDependencies{},
		LLMDependencies{},
	)
	if err != nil {
		return fmt.Errorf("failed to create activities: %w", err)
	}

	// Execute return bounty activity
	err = workflow.ExecuteActivity(ctx, activities.ReturnBountyToOwner, input.ToAccount, input.Amount.ToUSDC()).Get(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to return bounty: %w", err)
	}

	return nil
}
