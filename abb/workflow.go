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
}

// BountyAssessmentWorkflow represents the workflow that manages bounty assessment
func BountyAssessmentWorkflow(ctx workflow.Context, input BountyAssessmentWorkflowInput) error {
	// REMOVED: GetConfigurationActivity call

	// Set default activity options (can be overridden)
	options := workflow.ActivityOptions{
		StartToCloseTimeout: 1 * time.Minute, // Default timeout
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    time.Minute,
			MaximumAttempts:    3,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, options)

	// await the bounty payment from the funder
	_, err := awaitBountyFund(ctx, input) // Pass full input
	if err != nil {
		return err
	}

	// await the fee transfer to the treasury wallet
	err = awaitFeeTransfer(ctx, input) // Pass full input
	if err != nil {
		return err
	}

	// loop to process content submissions
	return awaitLoopUntilEmptyOrTimeout(ctx, input) // Pass full input
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
	// Removed SolanaConfig as it's not directly needed by this workflow's activities
}

// PullContentWorkflow represents the workflow that pulls content from a platform
func PullContentWorkflow(ctx workflow.Context, input PullContentWorkflowInput) ([]byte, error) {
	// REMOVED: GetConfigurationActivity call

	// --- Activity Options for Pulling ---
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

	// Pull content based on platform type, using original signatures
	var contentBytes []byte
	var err error
	switch input.PlatformType {
	case PlatformReddit:
		var redditContent *RedditContent
		err = workflow.ExecuteActivity(ctx, (*Activities).PullRedditContent, input.ContentID).Get(ctx, &redditContent)
		if err == nil {
			contentBytes, err = json.Marshal(redditContent)
		}
	case PlatformYouTube:
		var youtubeContent *YouTubeContent
		err = workflow.ExecuteActivity(ctx, (*Activities).PullYouTubeContent, input.ContentID).Get(ctx, &youtubeContent)
		if err == nil {
			contentBytes, err = json.Marshal(youtubeContent)
		}
	default:
		return nil, fmt.Errorf("unsupported platform type: %s", input.PlatformType)
	}

	if err != nil { // Check activity or marshalling error
		return nil, fmt.Errorf("failed to pull/marshal %s content: %w", input.PlatformType, err)
	}

	return contentBytes, nil
}

// CheckContentRequirementsResult represents the result of checking content requirements
type CheckContentRequirementsResult struct {
	Satisfies bool
	Reason    string
}

// CheckContentRequirementsWorkflow represents the workflow that checks if content satisfies requirements
func CheckContentRequirementsWorkflow(ctx workflow.Context, content []byte, requirements []string) (CheckContentRequirementsResult, error) {
	options := workflow.ActivityOptions{
		StartToCloseTimeout: 120 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    time.Minute,
			MaximumAttempts:    3,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, options)

	var result CheckContentRequirementsResult
	// Call activity with original signature (no llmCfg)
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
	SolanaConfig SolanaConfig // Re-added: Needed by TransferUSDC called within
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

	// --- Create Memo for Direct Payment --- //
	type DirectPaymentMemo struct {
		WorkflowID string `json:"workflow_id"`
	}
	memoData := DirectPaymentMemo{WorkflowID: workflow.GetInfo(ctx).WorkflowExecution.ID}
	memoBytes, err := json.Marshal(memoData)
	if err != nil {
		// Log error, but maybe proceed without memo?
		workflow.GetLogger(ctx).Error("Failed to marshal direct payment memo", "error", err)
		memoBytes = []byte("{}") // Send empty JSON object as memo
	}
	memoString := string(memoBytes)
	// --- End Memo Creation --- //

	err = workflow.ExecuteActivity(ctx, (*Activities).TransferUSDC, input.Wallet, input.Amount.ToUSDC(), memoString).Get(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to transfer USDC: %w", err)
	}

	return nil
}

// awaitBountyFund blocks until a payment has been received from the funder wallet
func awaitBountyFund(
	ctx workflow.Context,
	input BountyAssessmentWorkflowInput,
) (*VerifyPaymentResult, error) {
	logger := workflow.GetLogger(ctx)
	fw, err := solanago.PublicKeyFromBase58(input.BountyFunderWallet)
	if err != nil {
		return nil, fmt.Errorf("invalid funder wallet address: %w", err)
	}

	logger.Info("Waiting for payment verification", "timeout", input.PaymentTimeout, "funder_wallet", input.BountyFunderWallet)

	verifyPaymentActivityOptions := workflow.ActivityOptions{
		StartToCloseTimeout: input.PaymentTimeout + (10 * time.Second), // Add buffer to internal timeout
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        time.Second,
			BackoffCoefficient:     2.0,
			MaximumInterval:        10 * time.Second,
			MaximumAttempts:        5,
			NonRetryableErrorTypes: []string{"TimeoutError"},
		},
	}
	verifyPaymentCtx := workflow.WithActivityOptions(ctx, verifyPaymentActivityOptions)

	var verifyResult *VerifyPaymentResult
	err = workflow.ExecuteActivity(
		verifyPaymentCtx,
		(*Activities).VerifyPayment,
		fw,
		input.OriginalTotalBounty,
		input.PaymentTimeout,
	).Get(ctx, &verifyResult)
	if err != nil {
		return nil, fmt.Errorf("failed to verify payment: %w", err)
	}

	if !verifyResult.Verified {
		return nil, fmt.Errorf("payment verification failed: %s", verifyResult.Error)
	}

	return verifyResult, nil
}

// awaitFeeTransfer blocks until the fee has been transferred to the treasury wallet
func awaitFeeTransfer(
	ctx workflow.Context,
	input BountyAssessmentWorkflowInput,
) error {
	logger := workflow.GetLogger(ctx)
	// Convert relevant addresses
	funderAccount, err := solanago.PublicKeyFromBase58(input.BountyFunderWallet)
	if err != nil {
		return fmt.Errorf("invalid funder wallet address for fee check: %w", err)
	}

	// Calculate expected fee amount
	if input.OriginalTotalBounty == nil || input.TotalBounty == nil {
		logger.Error("OriginalTotalBounty or TotalBounty is nil, cannot calculate fee")
		// Or return a specific error, depending on desired behavior
		return fmt.Errorf("cannot calculate fee: input bounty amounts are nil")
	}
	expectedFeeAmount := input.OriginalTotalBounty.Sub(input.TotalBounty)
	if !expectedFeeAmount.IsPositive() { // Check if fee is zero or negative
		logger.Info("Calculated fee is zero or negative, skipping fee transfer verification.", "original_total", input.OriginalTotalBounty.ToUSDC(), "user_total", input.TotalBounty.ToUSDC())
		return nil // No fee to verify
	}

	// Verify fee payment using the same VerifyPayment activity
	logger.Info("Waiting for fee transfer verification", "timeout", input.PaymentTimeout, "expected_fee", expectedFeeAmount.ToUSDC())

	// Define specific activity options for fee verification
	verifyFeeActivityOptions := workflow.ActivityOptions{
		StartToCloseTimeout: input.PaymentTimeout + (10 * time.Second),
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        time.Second,
			BackoffCoefficient:     2.0,
			MaximumInterval:        10 * time.Second,
			MaximumAttempts:        5,
			NonRetryableErrorTypes: []string{"TimeoutError"},
		},
	}
	verifyFeeCtx := workflow.WithActivityOptions(ctx, verifyFeeActivityOptions)

	var verifyFeeResult *VerifyPaymentResult
	err = workflow.ExecuteActivity(
		verifyFeeCtx,
		(*Activities).VerifyPayment,
		funderAccount,
		expectedFeeAmount,
		input.PaymentTimeout,
	).Get(ctx, &verifyFeeResult)
	if err != nil {
		logger.Error("Fee transfer verification activity failed", "error", err)
		return fmt.Errorf("failed to verify fee transfer: %w", err)
	}

	if !verifyFeeResult.Verified {
		logger.Error("Fee transfer verification failed", "reason", verifyFeeResult.Error)
		return fmt.Errorf("fee transfer not verified: %s", verifyFeeResult.Error)
	}

	logger.Info("Fee transfer verification successful")
	return nil
}

// awaitLoopUntilEmptyOrTimeout is the main loop for bounty assessment
func awaitLoopUntilEmptyOrTimeout(
	ctx workflow.Context,
	// Pass the full input which contains SolanaConfig
	input BountyAssessmentWorkflowInput,
) error {
	logger := workflow.GetLogger(ctx)
	workflowID := workflow.GetInfo(ctx).WorkflowExecution.ID
	remainingBounty, err := solana.NewUSDCAmount(input.TotalBounty.ToUSDC())
	if err != nil {
		logger.Error("Failed to create mutable copy of remaining bounty", "error", err)
		return fmt.Errorf("failed to initialize remaining bounty: %w", err)
	}
	processedContentIDs := make(map[string]bool) // Map to track processed content IDs

	signalChan := workflow.GetSignalChannel(ctx, AssessmentSignalName)
	cancelSignalChan := workflow.GetSignalChannel(ctx, CancelSignalName)

	// Check for timeout
	timerCtx, cancelTimer := workflow.WithCancel(ctx)
	timerFuture := workflow.NewTimer(timerCtx, input.Timeout)
	logger.Info("Bounty assessment loop started", "timeout", input.Timeout)

	for {
		if !remainingBounty.IsPositive() {
			logger.Info("Total bounty depleted, ending workflow.")
			cancelTimer()
			return nil
		}

		selector := workflow.NewSelector(ctx)
		selector.AddFuture(timerFuture, func(f workflow.Future) {
			logger.Info("Bounty assessment period timed out.")
			fErr := f.Get(ctx, nil)
			if fErr != nil {
				// Don't return here, we still need to try refunding
				logger.Error("Timer future failed but proceeding with potential refund", "error", fErr)
			}

			// Implement refund logic similar to cancel signal
			if !remainingBounty.IsZero() {
				amountToRefund := remainingBounty
				logger.Info("Attempting to return remaining bounty to owner due to timeout", "owner_wallet", input.BountyOwnerWallet, "amount", amountToRefund.ToUSDC())
				// Use a context that won't be immediately cancelled by the timer completing
				refundCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: DefaultPayoutTimeout})

				// --- Create Memo for Timeout Refund --- //
				type RefundMemo struct {
					WorkflowID string `json:"workflow_id"`
				}
				memoData := RefundMemo{WorkflowID: workflowID}
				memoBytes, mErr := json.Marshal(memoData)
				if mErr != nil {
					logger.Error("Failed to marshal refund memo", "error", mErr)
					memoBytes = []byte("{}")
				}
				memoString := string(memoBytes)
				// --- End Memo Creation --- //

				// Call TransferUSDC activity with memo
				refundErr := workflow.ExecuteActivity(refundCtx, (*Activities).TransferUSDC, input.BountyOwnerWallet, amountToRefund.ToUSDC(), memoString).Get(refundCtx, nil)
				if refundErr != nil {
					// Log error but don't fail the workflow, timeout already happened
					logger.Error("Failed to return remaining bounty to owner on timeout", "owner_wallet", input.BountyOwnerWallet, "amount", amountToRefund.ToUSDC(), "error", refundErr)
				} else {
					logger.Info("Successfully returned remaining bounty to owner on timeout")
					remainingBounty = solana.Zero() // Set remaining bounty to zero after successful refund
				}
			} else {
				logger.Info("Timeout occurred, but remaining bounty was already zero.")
			}
			// Workflow will naturally end after this selector branch finishes
		})

		selector.AddReceive(signalChan, func(c workflow.ReceiveChannel, more bool) {
			if !more {
				logger.Info("Assessment signal channel closed.")
				return
			}
			var signal AssessContentSignal
			c.Receive(ctx, &signal)
			logger.Info("Received assessment signal", "ContentID", signal.ContentID, "Platform", signal.Platform, "PayoutWallet", signal.PayoutWallet)

			// Idempotency Check
			if processedContentIDs[signal.ContentID] {
				logger.Debug("Duplicate assessment signal received, already processed.", "ContentID", signal.ContentID)
				return // Skip processing
			}

			actOpts := workflow.ActivityOptions{
				StartToCloseTimeout: time.Minute,
				RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
			}
			loopCtx := workflow.WithActivityOptions(ctx, actOpts)

			// 1. Pull Content (using original activity signatures)
			var contentBytes []byte
			pullErr := fmt.Errorf("unsupported platform type in loop: %s", signal.Platform)
			switch signal.Platform {
			case PlatformReddit:
				var redditContent *RedditContent
				pullErr = workflow.ExecuteActivity(loopCtx, (*Activities).PullRedditContent, signal.ContentID).Get(loopCtx, &redditContent)
				if pullErr == nil {
					contentBytes, pullErr = json.Marshal(redditContent)
				}
			case PlatformYouTube:
				var youtubeContent *YouTubeContent
				pullErr = workflow.ExecuteActivity(loopCtx, (*Activities).PullYouTubeContent, signal.ContentID).Get(loopCtx, &youtubeContent)
				if pullErr == nil {
					contentBytes, pullErr = json.Marshal(youtubeContent)
				}
			default:
				pullErr = fmt.Errorf("unsupported platform type in loop: %s", signal.Platform)
			}

			if pullErr != nil {
				logger.Error("Failed to pull or marshal content", "ContentID", signal.ContentID, "Platform", signal.Platform, "error", pullErr)
				return // Continue loop
			}

			// 2. Check Requirements
			var checkResult CheckContentRequirementsResult
			checkOpts := workflow.ActivityOptions{StartToCloseTimeout: 90 * time.Second}
			// Use original CheckContentRequirements signature
			err = workflow.ExecuteActivity(
				workflow.WithActivityOptions(ctx, checkOpts),
				(*Activities).CheckContentRequirements,
				contentBytes,
				input.Requirements,
			).Get(loopCtx, &checkResult)
			if err != nil {
				logger.Error("Failed to check content requirements", "ContentID", signal.ContentID, "error", err)
				return // Continue loop
			}

			// 3. Pay Bounty if requirements met
			if checkResult.Satisfies {
				logger.Info("Content satisfies requirements, attempting payout.", "ContentID", signal.ContentID, "Reason", checkResult.Reason)

				payoutAmount := input.BountyPerPost
				shouldCap := false
				if remainingBounty.Cmp(payoutAmount) < 0 {
					shouldCap = true
					payoutAmount = remainingBounty
					logger.Info("Payout amount capped by remaining bounty", "capped_amount", payoutAmount.ToUSDC())
				}

				if !payoutAmount.IsZero() {
					payOpts := workflow.ActivityOptions{StartToCloseTimeout: DefaultPayoutTimeout}
					// --- Create Memo for Payout --- //
					type PayoutMemo struct {
						WorkflowID  string `json:"workflow_id"`
						ContentID   string `json:"content_id"`
						ContentKind string `json:"content_kind,omitempty"` // Placeholder for future use
					}
					memoData := PayoutMemo{
						WorkflowID: workflowID,
						ContentID:  signal.ContentID,
						// ContentKind: nil, // FIXME Explicitly null or omit until we introduce, but this would come from the workflow input
					}
					memoBytes, mErr := json.Marshal(memoData)
					if mErr != nil {
						logger.Error("Failed to marshal payout memo", "error", mErr)
						memoBytes = []byte("{}")
					}
					memoString := string(memoBytes)
					// --- End Memo Creation --- //

					payErr := workflow.ExecuteActivity(
						workflow.WithActivityOptions(ctx, payOpts),
						(*Activities).TransferUSDC,
						signal.PayoutWallet,
						payoutAmount.ToUSDC(),
						memoString).Get(loopCtx, nil)
					if payErr != nil {
						logger.Error("Failed to pay bounty", "ContentID", signal.ContentID, "Wallet", signal.PayoutWallet, "Amount", payoutAmount.ToUSDC(), "error", payErr)
					} else {
						logger.Info("Successfully paid bounty portion", "ContentID", signal.ContentID, "Wallet", signal.PayoutWallet, "Amount", payoutAmount.ToUSDC())
						if shouldCap {
							remainingBounty = solana.Zero()
						} else {
							remainingBounty = remainingBounty.Sub(input.BountyPerPost)
						}
						logger.Info("Remaining bounty after payout", "amount", remainingBounty.ToUSDC())
					}
				}
			} else {
				logger.Info("Content does not satisfy requirements.", "ContentID", signal.ContentID, "Reason", checkResult.Reason)
			}

			// Mark content ID as processed AFTER handling payout/failure logic
			processedContentIDs[signal.ContentID] = true
			logger.Debug("Marked content ID as processed", "ContentID", signal.ContentID)
		})

		selector.AddReceive(cancelSignalChan, func(c workflow.ReceiveChannel, more bool) {
			if !more {
				logger.Info("Cancel signal channel closed.")
				return
			}
			var cancelSignal CancelBountySignal
			c.Receive(ctx, &cancelSignal)
			logger.Info("Received cancel signal", "BountyOwnerWallet", cancelSignal.BountyOwnerWallet)
			cancelTimer()

			if cancelSignal.BountyOwnerWallet != input.BountyOwnerWallet {
				logger.Warn("Received cancel signal from incorrect owner", "received_owner", cancelSignal.BountyOwnerWallet, "expected_owner", input.BountyOwnerWallet)
				return
			}

			if !remainingBounty.IsZero() {
				amountToRefund := remainingBounty
				logger.Info("Attempting to return remaining bounty to owner", "owner_wallet", input.BountyOwnerWallet, "amount", amountToRefund.ToUSDC())
				cancelOpts := workflow.ActivityOptions{StartToCloseTimeout: DefaultPayoutTimeout}
				// --- Create Memo for Cancellation Refund --- //
				type CancelRefundMemo struct {
					WorkflowID string `json:"workflow_id"`
				}
				memoData := CancelRefundMemo{WorkflowID: workflowID}
				memoBytes, mErr := json.Marshal(memoData)
				if mErr != nil {
					logger.Error("Failed to marshal cancel refund memo", "error", mErr)
					memoBytes = []byte("{}")
				}
				memoString := string(memoBytes)
				// --- End Memo Creation --- //
				refundErr := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, cancelOpts), (*Activities).TransferUSDC, input.BountyOwnerWallet, amountToRefund.ToUSDC(), memoString).Get(ctx, nil)
				if refundErr != nil {
					logger.Error("Failed to return remaining bounty to owner", "owner_wallet", input.BountyOwnerWallet, "amount", amountToRefund.ToUSDC(), "error", refundErr)
				} else {
					logger.Info("Successfully returned remaining bounty to owner")
					remainingBounty = solana.Zero()
				}
			}
			logger.Info("Workflow cancelled by signal.")
		})

		selector.Select(ctx)

		if ctx.Err() != nil {
			logger.Warn("Workflow context done, exiting loop", "error", ctx.Err())
			return ctx.Err()
		}
	}
}
