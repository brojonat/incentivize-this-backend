package abb

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/brojonat/affiliate-bounty-board/solana"
	solanago "github.com/gagliardetto/solana-go"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// Search Attribute Keys
var (
	EnvironmentKey          = temporal.NewSearchAttributeKeyString("Environment")
	BountyOwnerWalletKey    = temporal.NewSearchAttributeKeyString("BountyOwnerWallet")
	BountyFunderWalletKey   = temporal.NewSearchAttributeKeyString("BountyFunderWallet")
	BountyPlatformKey       = temporal.NewSearchAttributeKeyString("BountyPlatform")
	BountyContentKindKey    = temporal.NewSearchAttributeKeyString("BountyContentKind")
	BountyTotalAmountKey    = temporal.NewSearchAttributeKeyFloat64("BountyTotalAmount")
	BountyPerPostAmountKey  = temporal.NewSearchAttributeKeyFloat64("BountyPerPostAmount")
	BountyCreationTimeKey   = temporal.NewSearchAttributeKeyTime("BountyCreationTime")
	BountyTimeoutTimeKey    = temporal.NewSearchAttributeKeyTime("BountyTimeoutTime")
	BountyStatusKey         = temporal.NewSearchAttributeKeyString("BountyStatus")
	BountyValueRemainingKey = temporal.NewSearchAttributeKeyFloat64("BountyValueRemaining")
)

// BountyStatus defines the possible states of a bounty workflow.
type BountyStatus string

// Bounty Status Constants
const (
	// BountyStatusAwaitingFunding indicates the workflow is waiting for the funder to deposit USDC.
	BountyStatusAwaitingFunding BountyStatus = "AwaitingFunding"
	// BountyStatusTransferringFee indicates the workflow is transferring the platform fee.
	BountyStatusTransferringFee BountyStatus = "TransferringFee"
	// BountyStatusListening indicates the workflow is actively listening for content submissions.
	BountyStatusListening BountyStatus = "Listening"
	// BountyStatusRefunded indicates the workflow has refunded the remaining bounty amount.
	BountyStatusRefunded BountyStatus = "Refunded"
	// BountyStatusPaying indicates a payment to a creator is in progress.
	BountyStatusPaying BountyStatus = "Paying"
	// BountyStatusCancelled indicates the bounty was cancelled externally.
	BountyStatusCancelled BountyStatus = "Cancelled"
)

// Signal Name Constants
const (
	AssessmentSignalName        = "assessment"
	CancelSignalName            = "cancel"
	GetPaidBountiesQueryType    = "getPaidBounties"
	DefaultPayoutTimeout        = 10 * time.Minute
	ContentReassessmentCooldown = 24 * time.Hour // Cooldown period for reassessing failed content
)

// AssessContentSignal represents a signal to assess content against bounty requirements
type AssessContentSignal struct {
	ContentID    string       `json:"content_id"`
	PayoutWallet string       `json:"payout_wallet"`
	Platform     PlatformKind `json:"platform"`
	ContentKind  ContentKind  `json:"content_kind"`
}

// CancelBountySignal represents the signal to cancel the bounty and return remaining funds
type CancelBountySignal struct {
	BountyOwnerWallet string
}

// BountyAssessmentWorkflowInput represents the input parameters for the workflow
type BountyAssessmentWorkflowInput struct {
	Requirements       []string           `json:"requirements"`
	BountyPerPost      *solana.USDCAmount `json:"bounty_per_post"`
	TotalBounty        *solana.USDCAmount `json:"total_bounty"`
	TotalCharged       *solana.USDCAmount `json:"total_charged"`
	BountyOwnerWallet  string             `json:"bounty_owner_wallet"`
	BountyFunderWallet string             `json:"bounty_funder_wallet"`
	Platform           PlatformKind       // The platform type (Reddit, YouTube, etc.)
	ContentKind        ContentKind        // The content kind (post, comment, video, etc.)
	PaymentTimeout     time.Duration      // How long to wait for funding/payment verification
	Timeout            time.Duration      // How long the bounty should remain active
	TreasuryWallet     string             `json:"treasury_wallet"`
	EscrowWallet       string             `json:"escrow_wallet"`
}

// BountyAssessmentWorkflow represents the workflow that manages bounty assessment
func BountyAssessmentWorkflow(ctx workflow.Context, input BountyAssessmentWorkflowInput) error {

	logger := workflow.GetLogger(ctx) // Moved logger initialization up for validation

	// --- Input Validation ---
	if input.BountyPerPost == nil || !input.BountyPerPost.IsPositive() {
		errMsg := fmt.Sprintf("BountyPerPost must be a positive value, got: %v", input.BountyPerPost)
		logger.Error(errMsg)
		return temporal.NewApplicationError(errMsg, "INVALID_INPUT_BOUNTY_PER_POST")
	}
	if input.TotalBounty == nil || !input.TotalBounty.IsPositive() {
		errMsg := fmt.Sprintf("TotalBounty must be a positive value, got: %v", input.TotalBounty)
		logger.Error(errMsg)
		return temporal.NewApplicationError(errMsg, "INVALID_INPUT_TOTAL_BOUNTY")
	}
	// Ensure TotalCharged is not nil before comparing
	if input.TotalCharged == nil {
		errMsg := "TotalCharged cannot be nil"
		logger.Error(errMsg)
		return temporal.NewApplicationError(errMsg, "INVALID_INPUT_NIL_TOTAL_CHARGED")
	}
	// Use Cmp for USDCAmount comparison: input.TotalCharged < input.TotalBounty
	if input.TotalCharged.Cmp(input.TotalBounty) < 0 {
		errMsg := fmt.Sprintf("TotalCharged (%v) cannot be less than TotalBounty (%v)", input.TotalCharged.ToUSDC(), input.TotalBounty.ToUSDC())
		logger.Error(errMsg)
		return temporal.NewApplicationError(errMsg, "INVALID_INPUT_TOTAL_CHARGED_LESS_THAN_TOTAL_BOUNTY")
	}
	// Add other critical input validations as needed (e.g., wallet address formats, platform supported)
	// For wallet addresses, parsing them early can also act as validation:
	if _, err := solanago.PublicKeyFromBase58(input.BountyOwnerWallet); err != nil {
		errMsg := fmt.Sprintf("Invalid BountyOwnerWallet address: %s", input.BountyOwnerWallet)
		logger.Error(errMsg, "error", err)
		return temporal.NewApplicationError(errMsg, "INVALID_OWNER_WALLET", err)
	}
	if _, err := solanago.PublicKeyFromBase58(input.BountyFunderWallet); err != nil {
		errMsg := fmt.Sprintf("Invalid BountyFunderWallet address: %s", input.BountyFunderWallet)
		logger.Error(errMsg, "error", err)
		return temporal.NewApplicationError(errMsg, "INVALID_FUNDER_WALLET", err)
	}
	if _, err := solanago.PublicKeyFromBase58(input.EscrowWallet); err != nil {
		errMsg := fmt.Sprintf("Invalid EscrowWallet address: %s", input.EscrowWallet)
		logger.Error(errMsg, "error", err)
		return temporal.NewApplicationError(errMsg, "INVALID_ESCROW_WALLET", err)
	}
	if input.TreasuryWallet != "" { // Treasury wallet is optional
		if _, err := solanago.PublicKeyFromBase58(input.TreasuryWallet); err != nil {
			errMsg := fmt.Sprintf("Invalid TreasuryWallet address: %s", input.TreasuryWallet)
			logger.Error(errMsg, "error", err)
			return temporal.NewApplicationError(errMsg, "INVALID_TREASURY_WALLET", err)
		}
	}

	// --- End Input Validation ---

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

	// --- Execute Fee Transfer (Escrow -> Treasury) ---
	logger.Info("Executing fee transfer")

	// Update status before attempting fee transfer
	err = workflow.UpsertTypedSearchAttributes(ctx, BountyStatusKey.ValueSet(string(BountyStatusTransferringFee)))
	if err != nil {
		logger.Error("Failed to update search attribute BountyStatus to TransferringFee", "error", err)
	}

	if input.TotalCharged != nil && input.TotalBounty != nil {
		feeAmount := input.TotalCharged.Sub(input.TotalBounty)
		if feeAmount.IsPositive() {
			workflowID := workflow.GetInfo(ctx).WorkflowExecution.ID
			feeTransferMemo := fmt.Sprintf("%s-fee-transfer", workflowID)
			logger.Info("Executing fee transfer from escrow to treasury", "amount", feeAmount.ToUSDC(), "memo", feeTransferMemo)
			transferOpts := workflow.ActivityOptions{
				StartToCloseTimeout: input.PaymentTimeout + (10 * time.Second),
				RetryPolicy: &temporal.RetryPolicy{
					InitialInterval:    time.Second,
					BackoffCoefficient: 2.0,
					MaximumInterval:    10 * time.Second,
					MaximumAttempts:    5,
				},
			}
			err = workflow.ExecuteActivity(
				workflow.WithActivityOptions(ctx, transferOpts),
				(*Activities).TransferUSDC,
				input.TreasuryWallet,
				feeAmount.ToUSDC(),
				feeTransferMemo,
			).Get(ctx, nil)
			if err != nil {
				logger.Error("Failed to execute fee transfer activity", "error", err)
				return fmt.Errorf("failed to execute fee transfer: %w", err)
			}
		} else {
			logger.Info("Fee amount is not positive, skipping transfer.")
		}
	} else {
		logger.Error("Cannot calculate or transfer fee: OriginalTotalBounty or TotalBounty is nil")
		// Decide if this is a fatal error for the workflow
		// return fmt.Errorf("cannot calculate fee: input bounty amounts are nil")
	}
	// ---------------------------------------------

	// Set initial search attributes for bounty values
	err = workflow.UpsertTypedSearchAttributes(ctx,
		BountyTotalAmountKey.ValueSet(input.TotalBounty.ToUSDC()),
		BountyPerPostAmountKey.ValueSet(input.BountyPerPost.ToUSDC()),
		BountyValueRemainingKey.ValueSet(input.TotalBounty.ToUSDC()), // Initially, remaining equals total
	)
	if err != nil {
		logger.Error("Failed to upsert initial bounty value search attributes", "error", err)
		// Decide if this should be fatal or just a warning
	}

	// Update status after fee transfer (or skip) before listening loop
	err = workflow.UpsertTypedSearchAttributes(ctx, BountyStatusKey.ValueSet(string(BountyStatusListening)))
	if err != nil {
		logger.Error("Failed to update search attribute BountyStatus to Listening", "error", err)
	}

	// loop to process content submissions
	return awaitLoopUntilEmptyOrTimeout(ctx, input)
}

// PlatformDependencies is an interface for platform-specific dependencies
type PlatformDependencies interface {
	// Type returns the platform type
	Type() PlatformKind
}

// ContentProvider is an interface for retrieving content from a platform
// FIXME/TODO: this interface MUST also accept a ContentKind to indicate the type of content to pull
// for the platform (comment, post, review, etc.). Right now we just infer the content kind before
// supplying the ID but this isn't a viable long-term solution. This will become abundantly clear
// when we more explicitly implement this as a graph based agentic workflow.
type ContentProvider interface {
	// PullContent pulls content from a platform given a content ID
	PullContent(ctx context.Context, contentID string, contentKind ContentKind) ([]byte, error)
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
	funderAccount, err := solanago.PublicKeyFromBase58(input.BountyFunderWallet)
	if err != nil {
		return nil, fmt.Errorf("invalid funder wallet address: %w", err)
	}
	escrowAccount, err := solanago.PublicKeyFromBase58(input.EscrowWallet)
	if err != nil {
		return nil, fmt.Errorf("invalid escrow wallet address from input: %w", err)
	}

	// Use workflow ID as the base for the memo
	workflowID := workflow.GetInfo(ctx).WorkflowExecution.ID
	fundingMemo := workflowID

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
		funderAccount,
		escrowAccount,
		input.TotalCharged,
		fundingMemo,
		input.PaymentTimeout,
	).Get(ctx, &verifyResult)
	if err != nil {
		return nil, fmt.Errorf("failed to verify bounty funding: %w", err)
	}

	if !verifyResult.Verified {
		return nil, fmt.Errorf("payment verification failed: %s", verifyResult.Error)
	}

	return verifyResult, nil
}

// awaitLoopUntilEmptyOrTimeout is the main loop for bounty assessment
func awaitLoopUntilEmptyOrTimeout(
	ctx workflow.Context,
	input BountyAssessmentWorkflowInput,
) error {
	logger := workflow.GetLogger(ctx)
	workflowID := workflow.GetInfo(ctx).WorkflowExecution.ID

	// --- State for Payout Details and Query Handler ---
	type PayoutDetail struct {
		ContentID    string             `json:"content_id"`
		PayoutWallet string             `json:"payout_wallet"`
		Amount       *solana.USDCAmount `json:"amount"`
		Timestamp    time.Time          `json:"timestamp"`
		Platform     PlatformKind       `json:"platform"`
		ContentKind  ContentKind        `json:"content_kind"`
	}
	successfullyPaidIDs := make(map[string]PayoutDetail)

	err := workflow.SetQueryHandler(ctx, GetPaidBountiesQueryType, func() ([]PayoutDetail, error) {
		// Create a slice from the map values
		payouts := make([]PayoutDetail, 0, len(successfullyPaidIDs))
		for _, pd := range successfullyPaidIDs {
			payouts = append(payouts, pd)
		}
		sort.Slice(payouts, func(i, j int) bool {
			return payouts[i].Timestamp.Before(payouts[j].Timestamp)
		})
		return payouts, nil
	})
	if err != nil {
		logger.Error("Failed to set query handler for GetPaidBountiesQueryType", "error", err)
	}
	// --- End State for Payout Details ---

	remainingBounty, err := solana.NewUSDCAmount(input.TotalBounty.ToUSDC())
	if err != nil {
		logger.Error("Failed to create mutable copy of remaining bounty", "error", err)
		return fmt.Errorf("failed to initialize remaining bounty: %w", err)
	}
	failedAttemptsCooldown := make(map[string]time.Time) // Stores timestamp of last failure for a contentID

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
				logger.Error("Timer future failed but proceeding with potential refund", "error", fErr)
			}

			// Update status before attempting timeout refund
			err := workflow.UpsertTypedSearchAttributes(ctx, BountyStatusKey.ValueSet(string(BountyStatusRefunded)))
			if err != nil {
				logger.Error("Failed to update search attribute BountyStatus to Refunded", "error", err)
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
					// Update remaining value SA
					err = workflow.UpsertTypedSearchAttributes(ctx, BountyValueRemainingKey.ValueSet(0.0))
					if err != nil {
						logger.Error("Failed to update search attribute BountyValueRemaining to 0 on timeout refund", "error", err)
					}
				}
			} else {
				logger.Info("Timeout occurred, but remaining bounty was already zero.")
				// Ensure SA is 0 if it somehow wasn't already
				err = workflow.UpsertTypedSearchAttributes(ctx, BountyValueRemainingKey.ValueSet(0.0))
				if err != nil {
					logger.Error("Failed to update search attribute BountyValueRemaining to 0 on timeout (already zero bounty)", "error", err)
				}
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
			logger.Info("Received assessment signal", "ContentID", signal.ContentID, "Platform", signal.Platform, "PayoutWallet", signal.PayoutWallet, "ContentKind", signal.ContentKind)

			// Idempotency Check & Cooldown Logic
			if _, exists := successfullyPaidIDs[signal.ContentID]; exists {
				logger.Debug("Content ID already successfully paid, skipping assessment.", "ContentID", signal.ContentID)
				return // Skip processing this signal, continue to next Select iteration
			}

			if lastFailureTime, exists := failedAttemptsCooldown[signal.ContentID]; exists {
				if workflow.Now(ctx).Before(lastFailureTime.Add(ContentReassessmentCooldown)) {
					logger.Info("Content ID failed assessment recently and is in cooldown period.", "ContentID", signal.ContentID, "CooldownUntil", lastFailureTime.Add(ContentReassessmentCooldown))
					return // Skip processing this signal, continue to next Select iteration
				}
				logger.Info("Content ID cooldown period passed, eligible for reprocessing.", "ContentID", signal.ContentID)
				delete(failedAttemptsCooldown, signal.ContentID) // Clear previous failure to allow reprocessing
			}

			// 1. Pull Content using PullContentActivity
			var contentBytes []byte
			var pullErr error
			pullActivityInput := PullContentInput{
				PlatformType: signal.Platform,
				ContentKind:  signal.ContentKind,
				ContentID:    signal.ContentID,
			}
			// Use activity options suitable for pulling content
			pullOpts := workflow.ActivityOptions{
				StartToCloseTimeout: 60 * time.Second, // Increased timeout for consolidated activity
				RetryPolicy: &temporal.RetryPolicy{
					InitialInterval:    20 * time.Second,
					BackoffCoefficient: 2.0,
					MaximumInterval:    time.Minute,
					MaximumAttempts:    3,
				},
			}
			pullContentCtx := workflow.WithActivityOptions(ctx, pullOpts)
			pullErr = workflow.ExecuteActivity(pullContentCtx, (*Activities).PullContentActivity, pullActivityInput).Get(pullContentCtx, &contentBytes)

			if pullErr != nil {
				logger.Error("PullContentActivity failed", "ContentID", signal.ContentID, "Platform", signal.Platform, "error", pullErr)
				failedAttemptsCooldown[signal.ContentID] = workflow.Now(ctx)
				logger.Debug("Marked content ID for cooldown due to PullContentActivity failure", "ContentID", signal.ContentID)
				return // Continue to next Select iteration
			}

			// 2. Image Analysis (if applicable, based on unmarshalled content)
			thumbnailAnalysisFailed := false
			var imgAnalysisResult CheckContentRequirementsResult // Stores result from AnalyzeImageURL

			// Specific activity options for image analysis (longer timeout, more retries)
			imgActOpts := workflow.ActivityOptions{
				StartToCloseTimeout: 120 * time.Second,
				RetryPolicy: &temporal.RetryPolicy{
					InitialInterval:    time.Second,
					BackoffCoefficient: 2.0,
					MaximumInterval:    time.Minute,
					MaximumAttempts:    3,
				},
			}
			imgAnalysisCtx := workflow.WithActivityOptions(ctx, imgActOpts) // Context for AnalyzeImageURL

			requirementsStringForImagePrompt := strings.Join(input.Requirements, "; ")
			if len(requirementsStringForImagePrompt) > MaxRequirementsCharsForLLMCheck {
				requirementsStringForImagePrompt = requirementsStringForImagePrompt[:MaxRequirementsCharsForLLMCheck] + "..."
				logger.Warn("Truncated requirements for image analysis prompt due to length limit", "ContentID", signal.ContentID, "limit", MaxRequirementsCharsForLLMCheck)
			}

			switch signal.Platform {
			case PlatformReddit:
				var redditContent RedditContent
				if err := json.Unmarshal(contentBytes, &redditContent); err != nil {
					logger.Error("Failed to unmarshal Reddit content after PullContentActivity", "ContentID", signal.ContentID, "error", err)
					thumbnailAnalysisFailed = true
					imgAnalysisResult.Reason = "Failed to unmarshal pulled Reddit content to extract thumbnail"
				} else {
					thumbnailURL := redditContent.Thumbnail
					if thumbnailURL != "" && thumbnailURL != "self" && thumbnailURL != "default" && thumbnailURL != "image" && thumbnailURL != "nsfw" {
						logger.Info("Reddit content has potential thumbnail/image URL, analyzing...", "ContentID", signal.ContentID, "URL", thumbnailURL)
						imageAnalysisPrompt := fmt.Sprintf(
							"Analyze this Reddit thumbnail/image based on the following bounty "+
								"requirements (you only need to consider the requirements pertaining "+
								"to the image): %s", requirementsStringForImagePrompt)
						imgErr := workflow.ExecuteActivity(imgAnalysisCtx, (*Activities).AnalyzeImageURL, thumbnailURL, imageAnalysisPrompt).Get(imgAnalysisCtx, &imgAnalysisResult)
						if imgErr != nil {
							logger.Error("Image analysis activity execution failed for Reddit content", "ContentID", signal.ContentID, "error", imgErr)
							thumbnailAnalysisFailed = true // imgAnalysisResult should have default failure values from activity
						} else if !imgAnalysisResult.Satisfies {
							logger.Info("Image analysis determined Reddit thumbnail/image does not meet requirements", "ContentID", signal.ContentID, "reason", imgAnalysisResult.Reason)
							thumbnailAnalysisFailed = true
						}
					}
				}
			case PlatformYouTube:
				var youtubeContent YouTubeContent
				if err := json.Unmarshal(contentBytes, &youtubeContent); err != nil {
					logger.Error("Failed to unmarshal YouTube content after PullContentActivity", "ContentID", signal.ContentID, "error", err)
					thumbnailAnalysisFailed = true
					imgAnalysisResult.Reason = "Failed to unmarshal pulled YouTube content to extract thumbnail"
				} else {
					thumbnailURL := youtubeContent.ThumbnailURL
					if thumbnailURL != "" {
						logger.Info("YouTube content has thumbnail, analyzing image...", "ContentID", signal.ContentID, "ThumbnailURL", thumbnailURL)
						imageAnalysisPrompt := fmt.Sprintf(
							"Analyze this YouTube thumbnail image based on the following bounty "+
								"requirements (you only need to consider the requirements pertaining "+
								"to the image): %s", requirementsStringForImagePrompt)
						imgErr := workflow.ExecuteActivity(imgAnalysisCtx, (*Activities).AnalyzeImageURL, thumbnailURL, imageAnalysisPrompt).Get(imgAnalysisCtx, &imgAnalysisResult)
						if imgErr != nil {
							logger.Error("Image analysis activity execution failed for YouTube content", "ContentID", signal.ContentID, "error", imgErr)
							thumbnailAnalysisFailed = true
						} else if !imgAnalysisResult.Satisfies {
							logger.Info("Image analysis determined YouTube thumbnail does not meet requirements", "ContentID", signal.ContentID, "reason", imgAnalysisResult.Reason)
							thumbnailAnalysisFailed = true
						}
					}
				}
			case PlatformTwitch:
				var thumbnailURL string
				var videoContent TwitchVideoContent
				if errUnVideo := json.Unmarshal(contentBytes, &videoContent); errUnVideo == nil && videoContent.ThumbnailURL != "" {
					thumbnailURL = videoContent.ThumbnailURL
				} else {
					var clipContent TwitchClipContent
					if errUnClip := json.Unmarshal(contentBytes, &clipContent); errUnClip == nil && clipContent.ThumbnailURL != "" {
						thumbnailURL = clipContent.ThumbnailURL
					} else {
						logger.Debug("Could not unmarshal Twitch content into Video or Clip to get thumbnail, or thumbnail was empty", "ContentID", signal.ContentID)
					}
				}

				if thumbnailURL != "" {
					logger.Info("Twitch content has thumbnail, analyzing image...", "ContentID", signal.ContentID, "ThumbnailURL", thumbnailURL)
					imageUrl := strings.ReplaceAll(strings.ReplaceAll(thumbnailURL, "%{width}", "100"), "%{height}", "100")
					imageAnalysisPrompt := fmt.Sprintf(
						"Analyze this Twitch thumbnail image based on the following bounty "+
							"requirements (you only need to consider the requirements pertaining "+
							"to the image): %s", requirementsStringForImagePrompt)
					imgErr := workflow.ExecuteActivity(imgAnalysisCtx, (*Activities).AnalyzeImageURL, imageUrl, imageAnalysisPrompt).Get(imgAnalysisCtx, &imgAnalysisResult)
					if imgErr != nil {
						logger.Error("Image analysis activity execution failed for Twitch content", "ContentID", signal.ContentID, "error", imgErr)
						thumbnailAnalysisFailed = true
					} else if !imgAnalysisResult.Satisfies {
						logger.Info("Image analysis determined Twitch thumbnail does not meet requirements", "ContentID", signal.ContentID, "reason", imgAnalysisResult.Reason)
						thumbnailAnalysisFailed = true
					}
				}
			case PlatformHackerNews:
				logger.Debug("No thumbnail analysis implemented for HackerNews", "ContentID", signal.ContentID)
			case PlatformBluesky:
				// Placeholder for potential future Bluesky image analysis if embeds are parsed
				logger.Debug("No explicit thumbnail analysis implemented for Bluesky yet", "ContentID", signal.ContentID)
			default:
				logger.Warn("Thumbnail analysis not implemented for platform or platform unhandled in switch", "platform", signal.Platform, "ContentID", signal.ContentID)
			}

			// 3. Check Content Requirements or skip if image analysis failed
			var checkResult CheckContentRequirementsResult // Result from CheckContentRequirements
			contentRequirementMet := false

			if thumbnailAnalysisFailed {
				logger.Info("Skipping content requirement check due to thumbnail analysis failure", "ContentID", signal.ContentID, "reason", imgAnalysisResult.Reason)
				checkResult = imgAnalysisResult // Use the reason from image analysis failure
			} else {
				checkOpts := workflow.ActivityOptions{ // Options for CheckContentRequirements
					StartToCloseTimeout: 120 * time.Second,
					RetryPolicy: &temporal.RetryPolicy{
						InitialInterval:    time.Second,
						BackoffCoefficient: 2.0,
						MaximumInterval:    time.Minute,
						MaximumAttempts:    3,
					},
				}
				checkReqCtx := workflow.WithActivityOptions(ctx, checkOpts)
				checkErr := workflow.ExecuteActivity(
					checkReqCtx,
					(*Activities).CheckContentRequirements,
					contentBytes,
					input.Requirements,
				).Get(checkReqCtx, &checkResult)

				if checkErr != nil {
					logger.Error("Failed to check content requirements", "ContentID", signal.ContentID, "error", checkErr)
					checkResult.Reason = fmt.Sprintf("Content requirement check activity failed: %s", checkErr.Error())
					// contentRequirementMet remains false
				} else {
					contentRequirementMet = checkResult.Satisfies
				}
			}

			// 4. Wallet Validation
			var walletValidationResult ValidateWalletResult
			payoutWalletAllowed := false

			if contentRequirementMet {
				walletValidateOpts := workflow.ActivityOptions{
					StartToCloseTimeout: 60 * time.Second,
					RetryPolicy: &temporal.RetryPolicy{
						InitialInterval:    time.Second,
						BackoffCoefficient: 2.0,
						MaximumInterval:    time.Minute,
						MaximumAttempts:    2,
					},
				}
				walletValCtx := workflow.WithActivityOptions(ctx, walletValidateOpts)
				walletValidateErr := workflow.ExecuteActivity(
					walletValCtx,
					(*Activities).ValidatePayoutWallet,
					signal.PayoutWallet,
					input.Requirements,
				).Get(walletValCtx, &walletValidationResult)

				if walletValidateErr != nil {
					logger.Error("Failed to validate payout wallet", "ContentID", signal.ContentID, "PayoutWallet", signal.PayoutWallet, "error", walletValidateErr)
					checkResult.Reason = fmt.Sprintf("Wallet validation activity failed: %s. Original reason: %s", walletValidateErr.Error(), checkResult.Reason)
					// payoutWalletAllowed remains false
				} else {
					payoutWalletAllowed = walletValidationResult.Satisfies
					if !payoutWalletAllowed {
						logger.Info("Payout wallet validation failed.", "ContentID", signal.ContentID, "PayoutWallet", signal.PayoutWallet, "Reason", walletValidationResult.Reason)
						checkResult.Reason = fmt.Sprintf("Payout wallet disallowed: %s. Original reason: %s", walletValidationResult.Reason, checkResult.Reason)
					}
				}
			} else {
				logger.Info("Skipping payout wallet validation as content requirements were not met.", "ContentID", signal.ContentID)
			}

			// 5. Payout Logic
			if contentRequirementMet && payoutWalletAllowed {
				logger.Info("Content and payout wallet satisfy requirements, attempting payout.", "ContentID", signal.ContentID, "ContentReason", checkResult.Reason, "WalletReason", walletValidationResult.Reason)
				payoutAmount := input.BountyPerPost
				shouldCap := false
				if remainingBounty.Cmp(payoutAmount) < 0 {
					shouldCap = true
					payoutAmount = remainingBounty
					logger.Info("Payout amount capped by remaining bounty", "capped_amount", payoutAmount.ToUSDC())
				}

				if !payoutAmount.IsZero() {
					payOpts := workflow.ActivityOptions{StartToCloseTimeout: DefaultPayoutTimeout}
					type PayoutMemo struct {
						WorkflowID  string      `json:"workflow_id"`
						ContentID   string      `json:"content_id"`
						ContentKind ContentKind `json:"content_kind,omitempty"`
					}
					memoData := PayoutMemo{WorkflowID: workflowID, ContentID: signal.ContentID, ContentKind: signal.ContentKind}
					memoBytes, mErr := json.Marshal(memoData)
					if mErr != nil {
						logger.Error("Failed to marshal payout memo", "error", mErr)
						memoBytes = []byte("{}")
					}
					memoString := string(memoBytes)

					err = workflow.UpsertTypedSearchAttributes(ctx, BountyStatusKey.ValueSet(string(BountyStatusPaying)))
					if err != nil {
						logger.Error("Failed to update search attribute BountyStatus to Paying", "error", err)
					}

					payoutActivityCtx := workflow.WithActivityOptions(ctx, payOpts)
					logger.Info("Attempting payout", "Amount", payoutAmount.ToUSDC(), "Recipient", signal.PayoutWallet, "ContentID", signal.ContentID)
					payErr := workflow.ExecuteActivity(payoutActivityCtx, (*Activities).TransferUSDC, signal.PayoutWallet, payoutAmount.ToUSDC(), memoString).Get(payoutActivityCtx, nil)
					if payErr != nil {
						logger.Error("Failed to pay bounty", "ContentID", signal.ContentID, "Wallet", signal.PayoutWallet, "Amount", payoutAmount.ToUSDC(), "error", payErr)
						errUpdate := workflow.UpsertTypedSearchAttributes(ctx, BountyStatusKey.ValueSet(string(BountyStatusListening)))
						if errUpdate != nil {
							logger.Error("Failed to update search attribute BountyStatus back to Listening after failed payout", "error", errUpdate)
						}
						failedAttemptsCooldown[signal.ContentID] = workflow.Now(ctx)
						logger.Error("Failed to pay bounty, marking for cooldown.", "ContentID", signal.ContentID)
					} else {
						logger.Info("Successfully paid bounty portion", "ContentID", signal.ContentID, "Wallet", signal.PayoutWallet, "Amount", payoutAmount.ToUSDC())
						if shouldCap {
							remainingBounty = solana.Zero()
						} else {
							remainingBounty = remainingBounty.Sub(input.BountyPerPost)
						}
						logger.Info("Remaining bounty after payout", "amount", remainingBounty.ToUSDC())
						err = workflow.UpsertTypedSearchAttributes(ctx, BountyValueRemainingKey.ValueSet(remainingBounty.ToUSDC()))
						if err != nil {
							logger.Error("Failed to update search attribute BountyValueRemaining after payout", "error", err)
						}

						// --- Record Payout Detail ---
						payoutDetailToStore := PayoutDetail{
							ContentID:    signal.ContentID,
							PayoutWallet: signal.PayoutWallet,
							Amount:       input.BountyPerPost, // The amount that was actually paid
							Timestamp:    workflow.Now(ctx),
							Platform:     signal.Platform,
							ContentKind:  signal.ContentKind,
						}
						successfullyPaidIDs[signal.ContentID] = payoutDetailToStore // Store the full detail
						logger.Debug("Recorded payout detail in successfullyPaidIDs map", "ContentID", signal.ContentID)
						// --- End Record Payout Detail ---
					}
				} else {
					logger.Info("Payout amount is zero for this content, no payout processed.", "ContentID", signal.ContentID)
					// Store a PayoutDetail with zero amount to ensure idempotency
					pd := PayoutDetail{
						ContentID:    signal.ContentID,
						PayoutWallet: signal.PayoutWallet, // Wallet that would have been paid
						Amount:       solana.Zero(),
						Timestamp:    workflow.Now(ctx),
						Platform:     signal.Platform,
						ContentKind:  signal.ContentKind,
					}
					successfullyPaidIDs[signal.ContentID] = pd
					delete(failedAttemptsCooldown, signal.ContentID) // Clear failure cooldown as it was processed.
					logger.Error("Content processed with zero payout, ContentID marked to prevent reprocessing.", "ContentID", signal.ContentID)
				}
			} else {
				logger.Info("Content does not satisfy requirements, or wallet disallowed.", "ContentID", signal.ContentID, "Reason", checkResult.Reason)
				failedAttemptsCooldown[signal.ContentID] = workflow.Now(ctx)
			}
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

			// Update status before attempting cancel refund
			err := workflow.UpsertTypedSearchAttributes(ctx, BountyStatusKey.ValueSet(string(BountyStatusCancelled)))
			if err != nil {
				logger.Error("Failed to update search attribute BountyStatus to Cancelled", "error", err)
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
					// Update remaining value SA
					err = workflow.UpsertTypedSearchAttributes(ctx, BountyValueRemainingKey.ValueSet(0.0))
					if err != nil {
						logger.Error("Failed to update search attribute BountyValueRemaining to 0 on cancellation refund", "error", err)
					}
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

// PublishBountiesWorkflow is a workflow that fetches bounties and publishes them.
func PublishBountiesWorkflow(ctx workflow.Context) error {
	logger := workflow.GetLogger(ctx)
	logger.Info("PublishBountiesWorkflow started")

	// Set activity options with a reasonable timeout for the whole publish process
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute, // Timeout for the entire activity
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    5 * time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    time.Minute,
			MaximumAttempts:    3,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	// Execute the activity to publish bounties to Reddit
	err := workflow.ExecuteActivity(ctx, (*Activities).PublishBountiesReddit).Get(ctx, nil)
	if err != nil {
		logger.Error("PublishBountiesReddit activity failed", "error", err)
		return fmt.Errorf("failed to publish bounties: %w", err)
	}

	logger.Info("PublishBountiesWorkflow completed successfully")
	return nil
}

// --- Email Token Workflow ---

// EmailTokenWorkflowInput defines the input for the email sending workflow.
type EmailTokenWorkflowInput struct {
	Email string
	Token string
}

// EmailTokenWorkflow sends an email containing a token to the specified address.
func EmailTokenWorkflow(ctx workflow.Context, input EmailTokenWorkflowInput) error {
	logger := workflow.GetLogger(ctx)
	logger.Info("EmailTokenWorkflow started", "email", input.Email)

	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 1 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    5 * time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    time.Minute,
			MaximumAttempts:    3,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	err := workflow.ExecuteActivity(ctx, (*Activities).SendTokenEmail, input.Email, input.Token).Get(ctx, nil)
	if err != nil {
		logger.Error("SendEmailActivity failed", "error", err)
		return fmt.Errorf("failed to send token email: %w", err)
	}

	logger.Info("EmailTokenWorkflow completed successfully")
	return nil
}
