package abb

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/brojonat/affiliate-bounty-board/solana"
	solanago "github.com/gagliardetto/solana-go"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// AssessContentSignal represents a signal to assess content against bounty requirements
type AssessContentSignal struct {
	ContentID string       `json:"content_id"`
	UserID    string       `json:"user_id"`
	Platform  PlatformType `json:"platform"`
}

// CancelBountySignal represents the signal to cancel the bounty and return remaining funds
type CancelBountySignal struct {
	BountyOwnerWallet string
}

// BountyAssessmentWorkflowInput represents the input parameters for the workflow
type BountyAssessmentWorkflowInput struct {
	Requirements        []string           `json:"requirements"`
	BountyPerPost       *solana.USDCAmount `json:"bounty_per_post"`
	TotalBounty         *solana.USDCAmount `json:"total_bounty"`
	OriginalTotalBounty *solana.USDCAmount `json:"original_total_bounty"`
	BountyOwnerWallet   string             `json:"bounty_owner_wallet"`
	BountyFunderWallet  string             `json:"bounty_funder_wallet"`
	PlatformType        PlatformType       // The platform type (Reddit, YouTube, etc.)
	PaymentTimeout      time.Duration      // How long to wait for funding/payment verification
	Timeout             time.Duration      // How long the bounty should remain active
	SolanaConfig        SolanaConfig       // Use local abb.SolanaConfig
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

	// Convert funder wallet address to PublicKey
	funderAccount, err := solanago.PublicKeyFromBase58(input.BountyFunderWallet)
	if err != nil {
		// Fallback removed, only funder wallet is used now
		return fmt.Errorf("invalid funder wallet address: %w", err)
	}

	// Verify payment has been received
	workflow.GetLogger(ctx).Info("Waiting for payment verification", "timeout", input.PaymentTimeout)

	// Define specific activity options for VerifyPayment
	verifyPaymentActivityOptions := workflow.ActivityOptions{
		StartToCloseTimeout: input.PaymentTimeout + (10 * time.Second), // Add buffer to internal timeout
		// Use a shorter retry policy for transient errors during the check itself,
		// but don't retry indefinitely if payment isn't seen.
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    10 * time.Second, // Keep max interval short
			MaximumAttempts:    5,                // Limit attempts for transient errors
		},
	}
	verifyPaymentCtx := workflow.WithActivityOptions(ctx, verifyPaymentActivityOptions)

	var verifyResult *VerifyPaymentResult
	err = workflow.ExecuteActivity(verifyPaymentCtx, (*Activities).VerifyPayment, funderAccount, input.OriginalTotalBounty, input.PaymentTimeout).Get(ctx, &verifyResult)
	if err != nil {
		return fmt.Errorf("failed to verify payment: %w", err)
	}

	if !verifyResult.Verified {
		return fmt.Errorf("payment verification failed: %s", verifyResult.Error)
	}

	workflow.GetLogger(ctx).Info("Payment verified successfully",
		"amount", verifyResult.Amount.ToUSDC())

	// Initialize remaining bounty using the USER-PAYABLE total
	remainingBounty := input.TotalBounty

	// Create signal channels
	assessmentChan := workflow.GetSignalChannel(ctx, "assessment")
	cancelChan := workflow.GetSignalChannel(ctx, "cancel")

	// Create selector for handling signals
	selector := workflow.NewSelector(ctx)

	// Add assessment signal handler
	selector.AddReceive(assessmentChan, func(c workflow.ReceiveChannel, more bool) {
		var assessmentSignal AssessContentSignal
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
		err = workflow.ExecuteActivity(ctx, (*Activities).CheckContentRequirements, content, input.Requirements).Get(ctx, &result)
		if err != nil {
			workflow.GetLogger(ctx).Error("Failed to check content requirements", "error", err)
			return
		}

		// If requirements are met, pay the bounty
		if result.Satisfies {
			err := workflow.ExecuteActivity(ctx, (*Activities).TransferUSDC, assessmentSignal.UserID, input.BountyPerPost.ToUSDC()).Get(ctx, nil)
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

		// Verify the owner wallet matches
		if cancelSignal.BountyOwnerWallet != input.BountyOwnerWallet {
			workflow.GetLogger(ctx).Error("Invalid owner wallet in cancellation signal")
			return
		}

		// Return remaining bounty to owner
		if !remainingBounty.IsZero() {
			err := workflow.ExecuteActivity(ctx, (*Activities).TransferUSDC, input.BountyOwnerWallet, remainingBounty.ToUSDC()).Get(ctx, nil)
			if err != nil {
				workflow.GetLogger(ctx).Error("Failed to return bounty to owner", "error", err)
				return
			}

			// Log the cancellation
			workflow.GetLogger(ctx).Info("Bounty cancelled and remaining funds returned to owner",
				"bounty_owner_wallet", input.BountyOwnerWallet,
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
			err := workflow.ExecuteActivity(ctx, (*Activities).TransferUSDC, input.BountyOwnerWallet, remainingBounty.ToUSDC()).Get(ctx, nil)
			if err != nil {
				workflow.GetLogger(ctx).Error("Failed to return remaining bounty to owner", "error", err)
			} else {
				// Log the timeout
				workflow.GetLogger(ctx).Info("Bounty timed out and remaining funds returned to owner",
					"bounty_owner_wallet", input.BountyOwnerWallet,
					"remaining_amount", remainingBounty.ToUSDC())
			}

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

// PullContentWorkflowInput represents the input parameters for the workflow
type PullContentWorkflowInput struct {
	PlatformType PlatformType
	ContentID    string
	SolanaConfig SolanaConfig // Use local abb.SolanaConfig
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

	// Pull content based on platform type
	switch input.PlatformType {
	case PlatformReddit:
		var redditContent *RedditContent
		err := workflow.ExecuteActivity(ctx, (*Activities).PullRedditContent, input.ContentID).Get(ctx, &redditContent)
		if err != nil {
			return "", fmt.Errorf("failed to pull Reddit content: %w", err)
		}
		return formatRedditContent(redditContent), nil
	case PlatformYouTube:
		var youtubeContent *YouTubeContent
		err := workflow.ExecuteActivity(ctx, (*Activities).PullYouTubeContent, input.ContentID).Get(ctx, &youtubeContent)
		if err != nil {
			return "", fmt.Errorf("failed to pull YouTube content: %w", err)
		}
		return formatYouTubeContent(youtubeContent), nil
	case PlatformYelp:
		var content string
		err := workflow.ExecuteActivity(ctx, (*Activities).PullYelpContent, input.ContentID).Get(ctx, &content)
		if err != nil {
			return "", fmt.Errorf("failed to pull Yelp content: %w", err)
		}
		return content, nil
	case PlatformGoogle:
		var content string
		err := workflow.ExecuteActivity(ctx, (*Activities).PullGoogleContent, input.ContentID).Get(ctx, &content)
		if err != nil {
			return "", fmt.Errorf("failed to pull Google content: %w", err)
		}
		return content, nil
	case PlatformAmazon:
		var content string
		err := workflow.ExecuteActivity(ctx, (*Activities).PullAmazonContent, input.ContentID).Get(ctx, &content)
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
func CheckContentRequirementsWorkflow(ctx workflow.Context, content string, requirements []string) (CheckContentRequirementsResult, error) {
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

	var result CheckContentRequirementsResult
	err := workflow.ExecuteActivity(ctx, (*Activities).CheckContentRequirements, content, requirements).Get(ctx, &result)
	if err != nil {
		return CheckContentRequirementsResult{}, err
	}
	return result, nil
}

// PayBountyWorkflowInput represents the input parameters for the pay bounty workflow
type PayBountyWorkflowInput struct {
	ToAccount    string
	Amount       *solana.USDCAmount
	SolanaConfig SolanaConfig // Use local abb.SolanaConfig
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
	toAccount, err := solanago.PublicKeyFromBase58(input.ToAccount)
	if err != nil {
		return fmt.Errorf("invalid to account: %w", err)
	}

	// Execute transfer activity using the function signature
	err = workflow.ExecuteActivity(ctx, (*Activities).TransferUSDC, toAccount.String(), input.Amount.ToUSDC()).Get(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to transfer USDC: %w", err)
	}

	return nil
}
