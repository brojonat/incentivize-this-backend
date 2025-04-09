package abb

import (
	"context"
	"fmt"
	"net/http"
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
	PlatformType            PlatformType  // The platform type (Reddit, YouTube, etc.)
	PlatformDependencies    interface{}   // Platform-specific dependencies
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
	Dependencies interface{} // Platform-specific dependencies (will be cast to the appropriate type)
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

	// Create activities instance with empty dependencies for unused platforms
	activities := NewActivities(
		solana.SolanaConfig{}, // Empty Solana config for testing
		"",                    // Empty server URL for testing
		"",                    // Empty auth token for testing
		RedditDependencies{},  // Will be set based on platform
		YouTubeDependencies{}, // Will be set based on platform
		YelpDependencies{},    // Will be set based on platform
		GoogleDependencies{},  // Will be set based on platform
		AmazonDependencies{},  // Will be set based on platform
		LLMDependencies{},     // Empty LLM deps for testing
	)

	// Select the appropriate activity based on platform type
	var result string
	var err error

	switch input.PlatformType {
	case PlatformReddit:
		var deps RedditDependencies
		if mapDeps, ok := input.Dependencies.(map[string]interface{}); ok {
			// Convert map to RedditDependencies
			if userAgent, ok := mapDeps["UserAgent"].(string); ok {
				deps.UserAgent = userAgent
			}
			if username, ok := mapDeps["Username"].(string); ok {
				deps.Username = username
			}
			if password, ok := mapDeps["Password"].(string); ok {
				deps.Password = password
			}
			if clientID, ok := mapDeps["ClientID"].(string); ok {
				deps.ClientID = clientID
			}
			if clientSecret, ok := mapDeps["ClientSecret"].(string); ok {
				deps.ClientSecret = clientSecret
			}
		} else if typedDeps, ok := input.Dependencies.(RedditDependencies); ok {
			deps = typedDeps
		} else {
			return "", fmt.Errorf("invalid dependencies for Reddit platform: got %T, expected map[string]interface{} or RedditDependencies", input.Dependencies)
		}
		activities.redditDeps = deps
		var redditContent *RedditContent
		err = workflow.ExecuteActivity(ctx, activities.PullRedditContent, input.ContentID).Get(ctx, &redditContent)
		if err != nil {
			return "", err
		}
		result = FormatRedditContent(redditContent)
	case PlatformYouTube:
		var deps YouTubeDependencies
		if mapDeps, ok := input.Dependencies.(map[string]interface{}); ok {
			// Convert map to YouTubeDependencies
			if apiKey, ok := mapDeps["APIKey"].(string); ok {
				deps.APIKey = apiKey
			}
			if appName, ok := mapDeps["ApplicationName"].(string); ok {
				deps.ApplicationName = appName
			}
			if maxResults, ok := mapDeps["MaxResults"].(int64); ok {
				deps.MaxResults = maxResults
			}
		} else if typedDeps, ok := input.Dependencies.(YouTubeDependencies); ok {
			deps = typedDeps
		} else {
			return "", fmt.Errorf("invalid dependencies for YouTube platform: got %T, expected map[string]interface{} or YouTubeDependencies", input.Dependencies)
		}
		activities.youtubeDeps = deps
		var youtubeContent *YouTubeContent
		err = workflow.ExecuteActivity(ctx, activities.PullYouTubeContent, input.ContentID).Get(ctx, &youtubeContent)
		if err != nil {
			return "", err
		}
		result = FormatYouTubeContent(youtubeContent)
	case PlatformYelp:
		deps, ok := input.Dependencies.(YelpDependencies)
		if !ok {
			return "", fmt.Errorf("invalid dependencies for Yelp platform: got %T, expected YelpDependencies", input.Dependencies)
		}
		activities.yelpDeps = deps
		err = workflow.ExecuteActivity(ctx, activities.PullYelpContent, input.ContentID).Get(ctx, &result)
	case PlatformGoogle:
		deps, ok := input.Dependencies.(GoogleDependencies)
		if !ok {
			return "", fmt.Errorf("invalid dependencies for Google platform: got %T, expected GoogleDependencies", input.Dependencies)
		}
		activities.googleDeps = deps
		err = workflow.ExecuteActivity(ctx, activities.PullGoogleContent, input.ContentID).Get(ctx, &result)
	case PlatformAmazon:
		deps, ok := input.Dependencies.(AmazonDependencies)
		if !ok {
			return "", fmt.Errorf("invalid dependencies for Amazon platform: got %T, expected AmazonDependencies", input.Dependencies)
		}
		activities.amazonDeps = deps
		err = workflow.ExecuteActivity(ctx, activities.PullAmazonContent, input.ContentID).Get(ctx, &result)
	default:
		return "", fmt.Errorf("unsupported platform type: %s", input.PlatformType)
	}

	if err != nil {
		return "", fmt.Errorf("activity execution failed: %w", err)
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
