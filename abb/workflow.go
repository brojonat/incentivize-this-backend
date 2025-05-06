package abb

import (
	"context"
	"encoding/json"
	"fmt"
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
	AssessmentSignalName = "assessment"
	CancelSignalName     = "cancel"
	DefaultPayoutTimeout = 10 * time.Minute
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
	logger := workflow.GetLogger(ctx)

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

// PullContentWorkflowInput represents the input parameters for the workflow
type PullContentWorkflowInput struct {
	PlatformType PlatformKind
	ContentKind  ContentKind
	ContentID    string
	// Removed SolanaConfig as it's not directly needed by this workflow's activities
}

// PullContentWorkflow represents the workflow that pulls content from a platform
func PullContentWorkflow(ctx workflow.Context, input PullContentWorkflowInput) ([]byte, error) {
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
		err = workflow.ExecuteActivity(ctx, (*Activities).PullRedditContent, input.ContentID, input.ContentKind).Get(ctx, &redditContent)
		if err == nil {
			contentBytes, err = json.Marshal(redditContent)
		}
	case PlatformYouTube:
		var youtubeContent *YouTubeContent
		err = workflow.ExecuteActivity(ctx, (*Activities).PullYouTubeContent, input.ContentID, input.ContentKind).Get(ctx, &youtubeContent)
		if err == nil {
			contentBytes, err = json.Marshal(youtubeContent)
		}
	case PlatformTwitch:
		var twitchContent interface{}
		err = workflow.ExecuteActivity(ctx, (*Activities).PullTwitchContent, input.ContentID, input.ContentKind).Get(ctx, &twitchContent)
		if err == nil {
			contentBytes, err = json.Marshal(twitchContent)
		}
	case PlatformHackerNews:
		var hackerNewsContent *HackerNewsContent
		err = workflow.ExecuteActivity(ctx, (*Activities).PullHackerNewsContent, input.ContentID, input.ContentKind).Get(ctx, &hackerNewsContent)
		if err == nil {
			contentBytes, err = json.Marshal(hackerNewsContent)
		}
	case PlatformBluesky:
		var blueskyContent *BlueskyContent
		err = workflow.ExecuteActivity(ctx, (*Activities).PullBlueskyContent, input.ContentID, input.ContentKind).Get(ctx, &blueskyContent)
		if err == nil {
			contentBytes, err = json.Marshal(blueskyContent)
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

			// Idempotency Check
			if processedContentIDs[signal.ContentID] {
				logger.Debug("Duplicate assessment signal received, already processed.", "ContentID", signal.ContentID)
				return // Skip processing
			}

			thumbnailAnalysisFailed := false                     // Declare flag here, before the switch
			var imgAnalysisResult CheckContentRequirementsResult // Declare result struct here

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
				pullErr = workflow.ExecuteActivity(loopCtx, (*Activities).PullRedditContent, signal.ContentID, signal.ContentKind).Get(loopCtx, &redditContent)
				if pullErr == nil {
					// Marshal the received content to bytes first
					marshaledDataForCheck, marshalErr := json.Marshal(redditContent)
					if marshalErr != nil {
						pullErr = fmt.Errorf("failed to marshal reddit content: %w", marshalErr)
					} else {
						// Attempt to use the URL field as the potential thumbnail URL
						// thumbnailURL := redditContent.URL // Assuming URL might be the thumbnail or image itself
						// Use the actual Thumbnail field now
						thumbnailURL := redditContent.Thumbnail
						contentBytes = marshaledDataForCheck // Start with original marshaled data

						// --- Image Analysis (if thumbnail URL exists and seems relevant) ---
						// Simple check: only analyze if URL is not empty and likely an image/direct link
						// if thumbnailURL != "" && (strings.HasSuffix(thumbnailURL, ".jpg") || strings.HasSuffix(thumbnailURL, ".png") || strings.HasSuffix(thumbnailURL, ".gif") || !strings.Contains(thumbnailURL, "reddit.com")) {
						// Check if thumbnail URL is present and not a default/placeholder like "self" or "default"
						if thumbnailURL != "" && thumbnailURL != "self" && thumbnailURL != "default" && thumbnailURL != "image" && thumbnailURL != "nsfw" {
							logger.Info("Reddit content has potential thumbnail/image URL, analyzing...", "ContentID", signal.ContentID, "URL", thumbnailURL)
							imageUrl := thumbnailURL // Use the URL directly

							// --- Truncate Requirements for Image Prompt ---
							requirementsString := strings.Join(input.Requirements, "; ")
							if len(requirementsString) > MaxRequirementsCharsForLLMCheck { // Reuse constant
								requirementsString = requirementsString[:MaxRequirementsCharsForLLMCheck] + "..."
								logger.Warn("Truncated requirements for image analysis prompt due to length limit", "ContentID", signal.ContentID, "limit", MaxRequirementsCharsForLLMCheck)
							}
							// --- End Truncation ---

							imageAnalysisPrompt := fmt.Sprintf(
								"Analyze this Reddit thumbnail/image based on the following bounty "+
									"requirements (you only need to consider the requirements pertaining "+
									"to the image): %s", requirementsString) // Use potentially truncated string

							imgActOpts := workflow.ActivityOptions{
								StartToCloseTimeout: 120 * time.Second,
								RetryPolicy: &temporal.RetryPolicy{
									InitialInterval:    time.Second,
									BackoffCoefficient: 2.0,
									MaximumInterval:    time.Minute,
									MaximumAttempts:    3,
								},
							}
							imgAnalysisErr := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, imgActOpts), (*Activities).AnalyzeImageURL, imageUrl, imageAnalysisPrompt).Get(loopCtx, &imgAnalysisResult)

							if imgAnalysisErr != nil {
								logger.Error("Image analysis activity execution failed for Reddit content", "ContentID", signal.ContentID, "error", imgAnalysisErr)
								thumbnailAnalysisFailed = true
							} else if !imgAnalysisResult.Satisfies {
								logger.Info("Image analysis determined Reddit thumbnail/image does not meet requirements", "ContentID", signal.ContentID, "reason", imgAnalysisResult.Reason)
								thumbnailAnalysisFailed = true
							}
						}
					}
				}
			case PlatformYouTube:
				var youtubeContent *YouTubeContent
				pullErr = workflow.ExecuteActivity(loopCtx, (*Activities).PullYouTubeContent, signal.ContentID, signal.ContentKind).Get(loopCtx, &youtubeContent)
				if pullErr == nil {
					// Marshal the received content to bytes first
					marshaledDataForCheck, marshalErr := json.Marshal(youtubeContent)
					if marshalErr != nil {
						pullErr = fmt.Errorf("failed to marshal youtube content: %w", marshalErr)
					} else {
						thumbnailURL := youtubeContent.ThumbnailURL
						contentBytes = marshaledDataForCheck // Start with original marshaled data

						// --- Image Analysis (if thumbnail exists) ---
						if thumbnailURL != "" {
							logger.Info("YouTube content has thumbnail, analyzing image...", "ContentID", signal.ContentID, "ThumbnailURL", thumbnailURL)
							imageUrl := thumbnailURL // Use the URL directly (YouTube thumbnails don't need formatting like Twitch)

							// --- Truncate Requirements for Image Prompt ---
							requirementsString := strings.Join(input.Requirements, "; ")
							if len(requirementsString) > MaxRequirementsCharsForLLMCheck { // Reuse constant
								requirementsString = requirementsString[:MaxRequirementsCharsForLLMCheck] + "..."
								logger.Warn("Truncated requirements for image analysis prompt due to length limit", "ContentID", signal.ContentID, "limit", MaxRequirementsCharsForLLMCheck)
							}
							// --- End Truncation ---

							imageAnalysisPrompt := fmt.Sprintf(
								"Analyze this YouTube thumbnail image based on the following bounty "+
									"requirements (you only need to consider the requirements pertaining "+
									"to the image): %s", requirementsString) // Use potentially truncated string

							imgActOpts := workflow.ActivityOptions{
								StartToCloseTimeout: 120 * time.Second,
								RetryPolicy: &temporal.RetryPolicy{
									InitialInterval:    time.Second,
									BackoffCoefficient: 2.0,
									MaximumInterval:    time.Minute,
									MaximumAttempts:    3,
								},
							}
							imgAnalysisErr := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, imgActOpts), (*Activities).AnalyzeImageURL, imageUrl, imageAnalysisPrompt).Get(loopCtx, &imgAnalysisResult)

							if imgAnalysisErr != nil {
								logger.Error("Image analysis activity execution failed for YouTube content", "ContentID", signal.ContentID, "error", imgAnalysisErr)
								thumbnailAnalysisFailed = true
							} else if !imgAnalysisResult.Satisfies {
								logger.Info("Image analysis determined YouTube thumbnail does not meet requirements", "ContentID", signal.ContentID, "reason", imgAnalysisResult.Reason)
								thumbnailAnalysisFailed = true
							}
						}
					}
				}
			case PlatformTwitch:
				var twitchContentInterface interface{}
				pullErr = workflow.ExecuteActivity(loopCtx, (*Activities).PullTwitchContent, signal.ContentID, signal.ContentKind).Get(loopCtx, &twitchContentInterface)
				if pullErr == nil {

					// Marshal the received interface to bytes first for robust extraction
					marshaledDataForCheck, marshalErr := json.Marshal(twitchContentInterface)
					if marshalErr != nil {
						pullErr = fmt.Errorf("failed to marshal twitch content interface: %w", marshalErr)
					} else {
						// Attempt to extract thumbnail URL by unmarshalling into specific types
						thumbnailURL := ""
						var videoContent TwitchVideoContent
						if err := json.Unmarshal(marshaledDataForCheck, &videoContent); err == nil {
							thumbnailURL = videoContent.ThumbnailURL
						} else {
							var clipContent TwitchClipContent
							if err := json.Unmarshal(marshaledDataForCheck, &clipContent); err == nil {
								thumbnailURL = clipContent.ThumbnailURL
							}
						}
						// thumbnailURL now holds the extracted URL, or is empty if not found/unmarshal failed

						// --- Image Analysis (if thumbnail exists) ---
						contentBytes = marshaledDataForCheck // Start with the original marshaled data
						if thumbnailURL != "" {
							logger.Info("Twitch content has thumbnail, analyzing image...", "ContentID", signal.ContentID, "ThumbnailURL", thumbnailURL)
							// Format URL for 100x100
							imageUrl := thumbnailURL
							imageUrl = strings.ReplaceAll(imageUrl, "%{width}", "100")
							imageUrl = strings.ReplaceAll(imageUrl, "%{height}", "100")

							// --- Truncate Requirements for Image Prompt ---
							requirementsString := strings.Join(input.Requirements, "; ")
							if len(requirementsString) > MaxRequirementsCharsForLLMCheck { // Reuse constant
								requirementsString = requirementsString[:MaxRequirementsCharsForLLMCheck] + "..."
								logger.Warn("Truncated requirements for image analysis prompt due to length limit", "ContentID", signal.ContentID, "limit", MaxRequirementsCharsForLLMCheck)
							}
							// --- End Truncation ---

							imageAnalysisPrompt := fmt.Sprintf(
								"Analyze this Twitch thumbnail image based on the following bounty "+
									"requirements (you only need to consider the requirements pertaining "+
									"to the image): %s", requirementsString) // Use potentially truncated string

							imgActOpts := workflow.ActivityOptions{
								StartToCloseTimeout: 120 * time.Second,
								RetryPolicy: &temporal.RetryPolicy{
									// Keep default timing but limit attempts
									InitialInterval:    time.Second,
									BackoffCoefficient: 2.0,
									MaximumInterval:    time.Minute,
									MaximumAttempts:    3, // Limit AnalyzeImageUrlActivity attempts
								},
							}
							imgAnalysisErr := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, imgActOpts), (*Activities).AnalyzeImageURL, imageUrl, imageAnalysisPrompt).Get(loopCtx, &imgAnalysisResult)

							if imgAnalysisErr != nil {
								// The activity now returns a default result on error, so we use that
								logger.Error("Image analysis activity execution failed", "ContentID", signal.ContentID, "error", imgAnalysisErr)
								thumbnailAnalysisFailed = true // Still flag it, but we have the result struct now
								// imgAnalysisResult already contains Satisfies:false and a reason
							} else if !imgAnalysisResult.Satisfies {
								// Image analysis successful, but requirements not met
								logger.Info("Image analysis determined thumbnail does not meet requirements", "ContentID", signal.ContentID, "reason", imgAnalysisResult.Reason)
								thumbnailAnalysisFailed = true // Treat as failure for overall check
							}
							// If analysis succeeded and Satisfies is true, we don't need to do anything here
							// We no longer combine the analysis result text into contentBytes

						} // End if thumbnailURL != ""
					} // End else (marshalErr == nil)
				}
			default:
				pullErr = fmt.Errorf("unsupported platform type in loop: %s", signal.Platform)
			}

			if pullErr != nil {
				logger.Error("Failed to pull or marshal content", "ContentID", signal.ContentID, "Platform", signal.Platform, "error", pullErr)
				processedContentIDs[signal.ContentID] = true // Mark as processed even on pull/marshal error
				logger.Debug("Marked content ID as processed after pull/marshal error", "ContentID", signal.ContentID)
				return // Continue loop
			}

			// --- Check Requirements or skip if image analysis failed ---
			var checkResult CheckContentRequirementsResult
			requirementsMet := false // Default to false

			if thumbnailAnalysisFailed {
				logger.Info("Skipping content requirement check due to thumbnail analysis failure", "ContentID", signal.ContentID)
				// requirementsMet remains false
				// Explicitly set reason for failure using the result from image analysis
				checkResult = imgAnalysisResult // Use the result from image analysis
			} else {
				// Only run CheckContentRequirements if image analysis didn't fail and was satisfied
				checkOpts := workflow.ActivityOptions{
					StartToCloseTimeout: 120 * time.Second,
					RetryPolicy: &temporal.RetryPolicy{
						// Keep default timing but limit attempts
						InitialInterval:    time.Second,
						BackoffCoefficient: 2.0,
						MaximumInterval:    time.Minute,
						MaximumAttempts:    3, // Limit CheckContentRequirements attempts
					},
				}
				checkErr := workflow.ExecuteActivity(
					workflow.WithActivityOptions(ctx, checkOpts),
					(*Activities).CheckContentRequirements,
					contentBytes,
					input.Requirements,
				).Get(loopCtx, &checkResult)

				if checkErr != nil {
					logger.Error("Failed to check content requirements", "ContentID", signal.ContentID, "error", checkErr)
					// requirementsMet remains false
				} else {
					requirementsMet = checkResult.Satisfies
				}
			}

			// --- Payout Logic ---
			if requirementsMet {
				logger.Info("Content satisfies requirements, attempting payout.", "ContentID", signal.ContentID, "Reason", checkResult.Reason)
				// ... (rest of the existing payout logic remains the same)
				// ... calculates payoutAmount, checks remainingBounty, calls TransferUSDC, updates remainingBounty ...
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
						WorkflowID  string      `json:"workflow_id"`
						ContentID   string      `json:"content_id"`
						ContentKind ContentKind `json:"content_kind,omitempty"` // Placeholder for future use
					}
					memoData := PayoutMemo{
						WorkflowID:  workflowID,
						ContentID:   signal.ContentID,
						ContentKind: signal.ContentKind,
					}
					memoBytes, mErr := json.Marshal(memoData)
					if mErr != nil {
						logger.Error("Failed to marshal payout memo", "error", mErr)
						memoBytes = []byte("{}")
					}
					memoString := string(memoBytes)
					// --- End Memo Creation --- //

					// --- Pay Bounty ---
					// Update status before attempting payout
					err = workflow.UpsertTypedSearchAttributes(ctx, BountyStatusKey.ValueSet(string(BountyStatusPaying)))
					if err != nil {
						logger.Error("Failed to update search attribute BountyStatus to Paying", "error", err)
					}

					logger.Info("Attempting payout", "Amount", payoutAmount.ToUSDC(), "Recipient", signal.PayoutWallet, "ContentID", signal.ContentID)
					payErr := workflow.ExecuteActivity(
						workflow.WithActivityOptions(ctx, payOpts),
						(*Activities).TransferUSDC,
						signal.PayoutWallet,
						payoutAmount.ToUSDC(),
						memoString).Get(loopCtx, nil)
					if payErr != nil {
						logger.Error("Failed to pay bounty", "ContentID", signal.ContentID, "Wallet", signal.PayoutWallet, "Amount", payoutAmount.ToUSDC(), "error", payErr)
						// Update status back to Listening after failed payout attempt
						errUpdate := workflow.UpsertTypedSearchAttributes(ctx, BountyStatusKey.ValueSet(string(BountyStatusListening)))
						if errUpdate != nil {
							logger.Error("Failed to update search attribute BountyStatus back to Listening after failed payout", "error", errUpdate)
						}
					} else {
						logger.Info("Successfully paid bounty portion", "ContentID", signal.ContentID, "Wallet", signal.PayoutWallet, "Amount", payoutAmount.ToUSDC())
						if shouldCap {
							remainingBounty = solana.Zero()
						} else {
							remainingBounty = remainingBounty.Sub(input.BountyPerPost)
						}
						logger.Info("Remaining bounty after payout", "amount", remainingBounty.ToUSDC())
						// Update remaining value SA
						err = workflow.UpsertTypedSearchAttributes(ctx, BountyValueRemainingKey.ValueSet(remainingBounty.ToUSDC()))
						if err != nil {
							logger.Error("Failed to update search attribute BountyValueRemaining after payout", "error", err)
						}
						// Update status back to Listening after successful payout
						errUpdate := workflow.UpsertTypedSearchAttributes(ctx, BountyStatusKey.ValueSet(string(BountyStatusListening)))
						if errUpdate != nil {
							logger.Error("Failed to update search attribute BountyStatus back to Listening after successful payout", "error", errUpdate)
						}
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

// --- End Email Token Workflow ---
