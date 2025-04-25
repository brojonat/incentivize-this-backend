package abb

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/brojonat/affiliate-bounty-board/solana"
	solanago "github.com/gagliardetto/solana-go"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// Signal Name Constants
const (
	AssessmentSignalName = "assessment"
	CancelSignalName     = "cancel"
	DefaultPayoutTimeout = 10 * time.Minute
)

// AssessContentSignal represents a signal to assess content against bounty requirements
type AssessContentSignal struct {
	ContentID    string       `json:"content_id"`
	PayoutWallet string       `json:"payout_wallet"`
	Platform     PlatformType `json:"platform"`
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

	// await the bounty payment from the funder
	_, err := awaitBountyFund(
		ctx,
		input.BountyFunderWallet,
		input.OriginalTotalBounty,
		input.PaymentTimeout,
	)
	if err != nil {
		return err
	}

	// await the fee transfer to the treasury wallet
	err = awaitFeeTransfer(
		ctx,
		input.OriginalTotalBounty,
		input.TotalBounty,
		input.SolanaConfig,
		input.PaymentTimeout,
	)
	if err != nil {
		return err
	}

	// loop
	return awaitLoopUntilEmptyOrTimeout(
		ctx,
		input.SolanaConfig,
		input.BountyOwnerWallet,
		input.TotalBounty,
		input.Timeout,
		input.BountyPerPost,
		input.TotalBounty,
		input.Requirements,
		input.PaymentTimeout,
	)
}

// PlatformDependencies is an interface for platform-specific dependencies
type PlatformDependencies interface {
	// Type returns the platform type
	Type() PlatformType
}

// ContentProvider is an interface for retrieving content from a platform
// FIXME/TODO: this interface MUST also accept a ContentKind to indicate the type of content to pull
// for the platform (comment, post, review, etc.). Right now we just infer the content kind before
// supplying the ID but this isn't a viable long-term solution. This will become abundantly clear
// when we more explicitly implement this as a graph based agentic workflow.
type ContentProvider interface {
	// PullContent pulls content from a platform given a content ID
	PullContent(ctx context.Context, contentID string) ([]byte, error)
}

// PullContentWorkflowInput represents the input parameters for the workflow
type PullContentWorkflowInput struct {
	PlatformType PlatformType
	ContentID    string
	SolanaConfig SolanaConfig // Use local abb.SolanaConfig
}

// PullContentWorkflow represents the workflow that pulls content from a platform
func PullContentWorkflow(ctx workflow.Context, input PullContentWorkflowInput) ([]byte, error) {
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
			return nil, fmt.Errorf("failed to pull Reddit content: %w", err)
		}
		jsonBytes, err := json.Marshal(redditContent)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal Reddit content: %w", err)
		}
		return jsonBytes, nil
	case PlatformYouTube:
		var youtubeContent *YouTubeContent
		err := workflow.ExecuteActivity(ctx, (*Activities).PullYouTubeContent, input.ContentID).Get(ctx, &youtubeContent)
		if err != nil {
			return nil, fmt.Errorf("failed to pull YouTube content: %w", err)
		}
		jsonBytes, err := json.Marshal(youtubeContent)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal YouTube content: %w", err)
		}
		return jsonBytes, nil
	default:
		return nil, fmt.Errorf("unsupported platform type: %s", input.PlatformType)
	}
}

// CheckContentRequirementsResult represents the result of checking content requirements
type CheckContentRequirementsResult struct {
	Satisfies bool
	Reason    string
}

// CheckContentRequirementsWorkflow represents the workflow that checks if content satisfies requirements
func CheckContentRequirementsWorkflow(ctx workflow.Context, content []byte, requirements []string) (CheckContentRequirementsResult, error) {
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
	Wallet       string // Renamed from ToAccount
	Amount       *solana.USDCAmount
	SolanaConfig SolanaConfig // Use local abb.SolanaConfig
}

// PayBountyWorkflow represents the workflow that pays a bounty
func PayBountyWorkflow(ctx workflow.Context, input PayBountyWorkflowInput) error {
	options := workflow.ActivityOptions{
		StartToCloseTimeout: DefaultPayoutTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    time.Minute,
			MaximumAttempts:    3,
		},
	}

	ctx = workflow.WithActivityOptions(ctx, options)

	// Convert wallet address to PublicKey
	walletAddr, err := solanago.PublicKeyFromBase58(input.Wallet)
	if err != nil {
		return fmt.Errorf("invalid wallet address: %w", err)
	}

	// Execute transfer activity using the function signature
	err = workflow.ExecuteActivity(ctx, (*Activities).TransferUSDC, walletAddr.String(), input.Amount.ToUSDC()).Get(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to transfer USDC: %w", err)
	}

	return nil
}

// awaitBountyFund verifies that a payment has been received from the funder wallet
// It handles payment verification and error handling
func awaitBountyFund(
	ctx workflow.Context,
	funderWallet string,
	originalTotalBounty *solana.USDCAmount,
	paymentTimeout time.Duration,
) (*VerifyPaymentResult, error) {
	// Convert funder wallet address to PublicKey
	funderAccount, err := solanago.PublicKeyFromBase58(funderWallet)
	if err != nil {
		return nil, fmt.Errorf("invalid funder wallet address: %w", err)
	}

	// Verify payment has been received
	workflow.GetLogger(ctx).Info("Waiting for payment verification", "timeout", paymentTimeout)

	// Define specific activity options for VerifyPayment
	verifyPaymentActivityOptions := workflow.ActivityOptions{
		StartToCloseTimeout: paymentTimeout + (10 * time.Second), // Add buffer to internal timeout
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
	err = workflow.ExecuteActivity(verifyPaymentCtx, (*Activities).VerifyPayment, funderAccount, originalTotalBounty, paymentTimeout).Get(ctx, &verifyResult)
	if err != nil {
		return nil, fmt.Errorf("failed to verify payment: %w", err)
	}

	if !verifyResult.Verified {
		return nil, fmt.Errorf("payment verification failed: %s", verifyResult.Error)
	}

	workflow.GetLogger(ctx).Info("Payment verified successfully",
		"amount", verifyResult.Amount.ToUSDC())

	return verifyResult, nil
}

// awaitFeeTransfer transfers the platform fee to the treasury wallet
// It handles fee calculation, validation, and the transfer operation
func awaitFeeTransfer(
	ctx workflow.Context,
	originalTotalBounty *solana.USDCAmount,
	totalBounty *solana.USDCAmount,
	config SolanaConfig,
	paymentTimeout time.Duration,
) error {
	// Transfer the fee to the treasury wallet
	if config.TreasuryWallet == "" {
		workflow.GetLogger(ctx).Error("TreasuryWallet not configured in SolanaConfig")
		return fmt.Errorf("treasury wallet not configured")
	}
	if originalTotalBounty == nil {
		workflow.GetLogger(ctx).Error("OriginalTotalBounty is nil in workflow input")
		return fmt.Errorf("original total bounty not provided")
	}
	if totalBounty == nil {
		workflow.GetLogger(ctx).Error("TotalBounty is nil in workflow input")
		return fmt.Errorf("total bounty not provided")
	}

	// Calculate fee amount
	feeAmount := originalTotalBounty.Sub(totalBounty)

	// Check if fee is positive
	if feeAmount.IsZero() || feeAmount.Cmp(solana.Zero()) < 0 { // Check if fee <= 0
		workflow.GetLogger(ctx).Error("Calculated fee amount is zero or negative, indicating potential config error",
			"original_total", originalTotalBounty.ToUSDC(),
			"user_total", totalBounty.ToUSDC())
		return fmt.Errorf("calculated fee is zero or negative (original: %f, user_payable: %f)",
			originalTotalBounty.ToUSDC(), totalBounty.ToUSDC())
	}

	// Fee is positive and configuration is present, proceed with transfer
	treasuryWalletAddr, err := solanago.PublicKeyFromBase58(config.TreasuryWallet)
	if err != nil {
		workflow.GetLogger(ctx).Error("Invalid TreasuryWallet address in config", "address", config.TreasuryWallet, "error", err)
		return fmt.Errorf("invalid treasury wallet address '%s': %w", config.TreasuryWallet, err)
	}

	workflow.GetLogger(ctx).Info("Attempting to transfer platform fee",
		"fee_amount", feeAmount.ToUSDC(),
		"treasury_wallet", treasuryWalletAddr.String())

	feeTransferOptions := workflow.ActivityOptions{
		StartToCloseTimeout: paymentTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second * 5,
			BackoffCoefficient: 1.5,
			MaximumInterval:    time.Minute,
			MaximumAttempts:    5,
		},
	}
	feeTransferCtx := workflow.WithActivityOptions(ctx, feeTransferOptions)

	err = workflow.ExecuteActivity(feeTransferCtx, (*Activities).TransferUSDC, treasuryWalletAddr.String(), feeAmount.ToUSDC()).Get(feeTransferCtx, nil)
	if err != nil {
		workflow.GetLogger(ctx).Error("Failed to transfer platform fee to treasury wallet", "error", err)
		return fmt.Errorf("failed to transfer platform fee: %w", err)
	}
	workflow.GetLogger(ctx).Info("Platform fee transferred successfully")

	return nil
}

// awaitLoopUntilEmptyOrTimeout is a helper function that waits for a bounty to
// be paid out or the timeout to expire
func awaitLoopUntilEmptyOrTimeout(
	ctx workflow.Context,
	solanaConfig SolanaConfig,
	bountyOwnerWallet string,
	bounty *solana.USDCAmount,
	timeout time.Duration,
	bountyPerPost *solana.USDCAmount,
	totalBounty *solana.USDCAmount,
	requirements []string,
	paymentTimeout time.Duration,
) error {

	// Initialize map to track processed content IDs for idempotency (prevents re-assessment)
	processedContentIDs := make(map[string]bool)

	// Create signal channels
	assessmentChan := workflow.GetSignalChannel(ctx, AssessmentSignalName)
	cancelChan := workflow.GetSignalChannel(ctx, CancelSignalName)

	// Create selector for handling signals
	selector := workflow.NewSelector(ctx)

	// Add assessment signal handler
	selector.AddReceive(assessmentChan, func(c workflow.ReceiveChannel, more bool) {
		var assessmentSignal AssessContentSignal
		c.Receive(ctx, &assessmentSignal)

		// Idempotency Check FIRST: Has this content already been processed (paid or failed)?
		if processedContentIDs[assessmentSignal.ContentID] {
			workflow.GetLogger(ctx).Debug("Content already processed, skipping duplicate signal",
				"content_id", assessmentSignal.ContentID)
			return // Don't process this signal further
		}

		// Check if we have enough remaining bounty
		if bounty.Cmp(bountyPerPost) < 0 {
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
			SolanaConfig: solanaConfig,
		}

		var content []byte
		err := workflow.ExecuteChildWorkflow(ctx, PullContentWorkflow, contentInput).Get(ctx, &content)
		if err != nil {
			workflow.GetLogger(ctx).Error("Failed to pull content", "error", err)
			return
		}

		// Check content requirements
		var result CheckContentRequirementsResult
		err = workflow.ExecuteActivity(ctx, (*Activities).CheckContentRequirements, content, requirements).Get(ctx, &result)
		if err != nil {
			workflow.GetLogger(ctx).Error("Failed to check content requirements", "error", err)
			return
		}

		// If requirements are met, pay the bounty
		if result.Satisfies {
			// Define specific options for the transfer activity
			transferOptions := workflow.ActivityOptions{
				StartToCloseTimeout: DefaultPayoutTimeout,
				RetryPolicy: &temporal.RetryPolicy{
					InitialInterval:    time.Second * 10,
					BackoffCoefficient: 2.0,
					MaximumInterval:    time.Minute,
					MaximumAttempts:    3, // Still retry a few times for transient issues
				},
			}
			transferCtx := workflow.WithActivityOptions(ctx, transferOptions)

			err := workflow.ExecuteActivity(transferCtx, (*Activities).TransferUSDC, assessmentSignal.PayoutWallet, bountyPerPost.ToUSDC()).Get(transferCtx, nil)
			if err != nil {
				workflow.GetLogger(ctx).Error("Failed to pay bounty. You may retry.", "error", err)
				// Do NOT mark as processed if payment fails, allow retry on next signal
				return
			}

			// Update remaining bounty only AFTER successful transfer
			bounty = bounty.Sub(bountyPerPost)

			// Log the payment
			workflow.GetLogger(ctx).Info("Bounty paid successfully",
				"payout_wallet", assessmentSignal.PayoutWallet,
				"amount", bountyPerPost.ToUSDC(),
				"remaining", bounty.ToUSDC())
		} else {
			workflow.GetLogger(ctx).Debug("Content did not meet requirements",
				"content_id", assessmentSignal.ContentID,
				"payout_wallet", assessmentSignal.PayoutWallet,
				"reason", result.Reason)
		}

		// Mark content as processed (either paid or failed) AFTER assessment attempt
		// unless payment failed (in which case we returned early).
		processedContentIDs[assessmentSignal.ContentID] = true
	})

	// Add cancellation signal handler
	selector.AddReceive(cancelChan, func(c workflow.ReceiveChannel, more bool) {
		var cancelSignal CancelBountySignal
		c.Receive(ctx, &cancelSignal)

		if cancelSignal.BountyOwnerWallet != bountyOwnerWallet {
			workflow.GetLogger(ctx).Error("Invalid owner wallet in cancellation signal")
			return
		}

		if !bounty.IsZero() {
			refundOptions := workflow.ActivityOptions{
				StartToCloseTimeout: 10 * time.Minute,
				RetryPolicy: &temporal.RetryPolicy{
					InitialInterval:    time.Second * 10,
					BackoffCoefficient: 2.0,
					MaximumInterval:    time.Minute,
					MaximumAttempts:    3,
				},
			}
			refundCtx := workflow.WithActivityOptions(ctx, refundOptions)

			err := workflow.ExecuteActivity(refundCtx, (*Activities).TransferUSDC, bountyOwnerWallet, bounty.ToUSDC()).Get(refundCtx, nil)
			if err != nil {
				workflow.GetLogger(ctx).Error("Failed to return bounty to owner on cancellation", "error", err)
				// Do not set remainingBounty to zero if refund fails
			} else {
				workflow.GetLogger(ctx).Info("Bounty cancelled and remaining funds returned to owner",
					"bounty_owner_wallet", bountyOwnerWallet,
					"remaining_amount", bounty.ToUSDC())
				// Set remaining bounty to zero ONLY after successful return
				bounty = solana.Zero()
			}
		} else {
			// Bounty already zero, ensure loop condition is met
			bounty = solana.Zero()
		}
	})

	// Create a timeout future
	timeoutFuture := workflow.NewTimer(ctx, timeout)
	selector.AddFuture(timeoutFuture, func(f workflow.Future) {
		_ = f.Get(ctx, nil) // Wait for timer to fire

		if !bounty.IsZero() {
			refundOptions := workflow.ActivityOptions{
				StartToCloseTimeout: 10 * time.Minute,
				RetryPolicy: &temporal.RetryPolicy{
					InitialInterval:    time.Second * 10,
					BackoffCoefficient: 2.0,
					MaximumInterval:    time.Minute,
					MaximumAttempts:    3,
				},
			}
			refundCtx := workflow.WithActivityOptions(ctx, refundOptions)

			err := workflow.ExecuteActivity(refundCtx, (*Activities).TransferUSDC, bountyOwnerWallet, bounty.ToUSDC()).Get(refundCtx, nil)
			if err != nil {
				workflow.GetLogger(ctx).Error("Failed to return remaining bounty to owner on timeout", "error", err)
				// Do not set remainingBounty to zero if refund fails
			} else {
				workflow.GetLogger(ctx).Info("Bounty timed out and remaining funds returned to owner",
					"bounty_owner_wallet", bountyOwnerWallet,
					"remaining_amount", bounty.ToUSDC())
				// Set remaining bounty to zero ONLY after successful return
				bounty = solana.Zero()
			}
		}
		// If bounty was already zero, the loop condition will handle termination.
	})
	// Wait for signals until remaining bounty is zero (set by handlers on success)
	for bounty.IsPositive() {
		selector.Select(ctx)
	}
	return nil
}
