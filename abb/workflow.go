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

// PayoutDetail struct moved to package level for accessibility
type PayoutDetail struct {
	ContentID    string             `json:"content_id"`
	PayoutWallet string             `json:"payout_wallet"`
	Amount       *solana.USDCAmount `json:"amount"`
	Timestamp    time.Time          `json:"timestamp"`
	Platform     PlatformKind       `json:"platform"`
	ContentKind  ContentKind        `json:"content_kind"`
}

// Search Attribute Keys
var (
	EnvironmentKey          = temporal.NewSearchAttributeKeyString("Environment")
	BountyFunderWalletKey   = temporal.NewSearchAttributeKeyString("BountyFunderWallet")
	BountyPlatformKey       = temporal.NewSearchAttributeKeyString("BountyPlatform")
	BountyContentKindKey    = temporal.NewSearchAttributeKeyString("BountyContentKind")
	BountyTotalAmountKey    = temporal.NewSearchAttributeKeyFloat64("BountyTotalAmount")
	BountyPerPostAmountKey  = temporal.NewSearchAttributeKeyFloat64("BountyPerPostAmount")
	BountyCreationTimeKey   = temporal.NewSearchAttributeKeyTime("BountyCreationTime")
	BountyTimeoutTimeKey    = temporal.NewSearchAttributeKeyTime("BountyTimeoutTime")
	BountyStatusKey         = temporal.NewSearchAttributeKeyString("BountyStatus")
	BountyValueRemainingKey = temporal.NewSearchAttributeKeyFloat64("BountyValueRemaining")
	BountyTierKey           = temporal.NewSearchAttributeKeyInt64("BountyTier")
)

// BountyCompletionStatus defines the various ways a bounty workflow can conclude.
// These are more granular than BountyStatus and used for the final summary.
type BountyCompletionStatus string

const (
	BountyCompletedEmpty         BountyCompletionStatus = "COMPLETED_EMPTY"
	BountyCompletedCancelled     BountyCompletionStatus = "COMPLETED_CANCELLED"
	BountyTimedOutRefunded       BountyCompletionStatus = "TIMED_OUT_REFUNDED"
	BountyTimedOutNoRefundNeeded BountyCompletionStatus = "TIMED_OUT_NO_REFUND_NEEDED"
	BountyFailedAwaitingFunding  BountyCompletionStatus = "AWAITING_FUNDING_FAILED"
	BountyFailedFeeTransfer      BountyCompletionStatus = "FEE_TRANSFER_FAILED"
	BountyFailedEmbedding        BountyCompletionStatus = "EMBEDDING_FAILED"
	BountyFailedInternalError    BountyCompletionStatus = "INTERNAL_ERROR"
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
	Title          string             `json:"title"`
	Requirements   []string           `json:"requirements"`
	BountyPerPost  *solana.USDCAmount `json:"bounty_per_post"`
	TotalBounty    *solana.USDCAmount `json:"total_bounty"`
	TotalCharged   *solana.USDCAmount `json:"total_charged"`
	Platform       PlatformKind       `json:"platform"`
	ContentKind    ContentKind        `json:"content_kind"`
	Tier           BountyTier         `json:"tier"`
	PaymentTimeout time.Duration      `json:"payment_timeout"`
	Timeout        time.Duration      `json:"timeout"`
	TreasuryWallet string             `json:"treasury_wallet"`
	EscrowWallet   string             `json:"escrow_wallet"`
}

// BountyAssessmentWorkflow represents the workflow that manages bounty assessment
func BountyAssessmentWorkflow(ctx workflow.Context, input BountyAssessmentWorkflowInput) error {
	logger := workflow.GetLogger(ctx)
	actualWorkflowStartTime := workflow.Now(ctx) // Capture start time
	logger.Info("BountyAssessmentWorkflow started", "workflowID", workflow.GetInfo(ctx).WorkflowExecution.ID, "input", input, "actualStartTime", actualWorkflowStartTime)

	// --- Initialize State for Payout Details and Register Query Handler Early ---
	successfullyPaidIDs := make(map[string]PayoutDetail)
	// --- Initialize data for final summary activity ---
	var workflowErr error // To store any error that terminates the workflow
	var finalStatus BountyCompletionStatus
	amountRefunded := solana.Zero()
	totalAmountPaid := solana.Zero()
	var funderWallet string // To store the inferred funder wallet
	// --- End Summary Data Initialization ---

	defer func() {
		// This function will execute when the workflow is about to exit,
		// either normally or due to an error (panic or returned error).
		wfInfo := workflow.GetInfo(ctx)
		summaryActivityOpts := workflow.ActivityOptions{
			StartToCloseTimeout: 2 * time.Minute, // Generous timeout for summary storage
			RetryPolicy: &temporal.RetryPolicy{
				MaximumAttempts: 3,
			},
		}
		disconnectedCtx, _ := workflow.NewDisconnectedContext(ctx)                       // Get disconnected context
		summaryCtx := workflow.WithActivityOptions(disconnectedCtx, summaryActivityOpts) // Use it here

		// Collect all paid bounties for the summary
		payoutsForSummary := make([]PayoutDetail, 0, len(successfullyPaidIDs))
		for _, pd := range successfullyPaidIDs {
			payoutsForSummary = append(payoutsForSummary, pd)
		}
		sort.Slice(payoutsForSummary, func(i, j int) bool {
			return payoutsForSummary[i].Timestamp.Before(payoutsForSummary[j].Timestamp)
		})

		feeAmount := solana.Zero()
		if input.TotalCharged != nil && input.TotalBounty != nil {
			feeAmount = input.TotalCharged.Sub(input.TotalBounty)
		}

		summaryData := BountySummaryData{
			BountyID:             wfInfo.WorkflowExecution.ID,
			Title:                input.Title,
			Requirements:         input.Requirements,
			Platform:             input.Platform,
			ContentKind:          input.ContentKind,
			Tier:                 input.Tier,
			BountyFunderWallet:   funderWallet, // Use inferred value
			OriginalTotalBounty:  input.TotalCharged,
			EffectiveTotalBounty: input.TotalBounty,
			BountyPerPost:        input.BountyPerPost,
			TotalAmountPaid:      totalAmountPaid,
			AmountRefunded:       amountRefunded,
			Payouts:              payoutsForSummary,
			FinalStatus:          string(finalStatus),
			WorkflowStartTime:    actualWorkflowStartTime,
			WorkflowEndTime:      workflow.Now(ctx),
			TimeoutDuration:      input.Timeout.String(),
			FeeAmount:            feeAmount,
		}

		if workflowErr != nil {
			summaryData.ErrorDetails = workflowErr.Error()
			// If finalStatus wasn't set by a specific failure point, set a generic one.
			if summaryData.FinalStatus == "" {
				summaryData.FinalStatus = string(BountyFailedInternalError)
			}
		}

		// If workflowErr is nil but finalStatus is also empty, it means normal completion.
		// We need to decide if it was empty or partial based on remainingBounty at the very end of awaitLoop.
		// This logic will be more accurately set within the awaitLoop or its return path.

		logger.Info("Preparing to execute SummarizeAndStoreBountyActivity", "bounty_id", summaryData.BountyID, "final_status_for_summary", summaryData.FinalStatus)
		if err := workflow.ExecuteActivity(summaryCtx, (*Activities).SummarizeAndStoreBountyActivity, SummarizeAndStoreBountyActivityInput{SummaryData: summaryData}).Get(summaryCtx, nil); err != nil {
			// Log critical failure to store summary, but don't let this error overshadow the original workflow error.
			logger.Error("Critical: Failed to execute SummarizeAndStoreBountyActivity", "bounty_id", summaryData.BountyID, "error", err)
		}

		// After successfully storing the summary, send a notification email.
		if emailErr := workflow.ExecuteActivity(summaryCtx, (*Activities).SendBountySummaryEmail, summaryData).Get(summaryCtx, nil); emailErr != nil {
			logger.Error("Failed to send bounty summary email", "bounty_id", summaryData.BountyID, "error", emailErr)
		}

		// --- Attempt to delete the bounty embedding via HTTP --- //
		deleteEmbeddingActivityOpts := workflow.ActivityOptions{
			StartToCloseTimeout: 1 * time.Minute, // Adjusted timeout for HTTP call
			RetryPolicy: &temporal.RetryPolicy{
				MaximumAttempts: 3, // Standard retries
			},
		}
		deleteEmbeddingCtx := workflow.WithActivityOptions(disconnectedCtx, deleteEmbeddingActivityOpts)
		deleteInput := DeleteBountyEmbeddingViaHTTPActivityInput{BountyID: wfInfo.WorkflowExecution.ID}
		if err := workflow.ExecuteActivity(deleteEmbeddingCtx, (*Activities).DeleteBountyEmbeddingViaHTTPActivity, deleteInput).Get(deleteEmbeddingCtx, nil); err != nil {
			logger.Error("Failed to execute DeleteBountyEmbeddingViaHTTPActivity", "bounty_id", deleteInput.BountyID, "error", err)
		}

	}()

	// --- End Defer for Summary and Embedding Deletion ---

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
		logger.Error("Failed to register query handler", "error", err)
		finalStatus = BountyFailedInternalError // Set specific final status
		workflowErr = err                       // Store the error
		return workflowErr                      // Return the error to trigger deferred summary
	}

	if err := workflow.UpsertTypedSearchAttributes(ctx, BountyStatusKey.ValueSet(string(BountyStatusAwaitingFunding))); err != nil {
		logger.Error("Failed to update search attribute BountyStatus to AwaitingFunding", "error", err)
		finalStatus = BountyFailedInternalError // Set specific final status
		workflowErr = err                       // Store the error
		return workflowErr                      // Return the error to trigger deferred summary
	}

	// Generate and Store Embedding for the bounty, return if there's an error
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 1 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 3,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)
	embeddingActivityInput := GenerateAndStoreBountyEmbeddingActivityInput{
		BountyID:      workflow.GetInfo(ctx).WorkflowExecution.ID,
		WorkflowInput: input,
	}
	if err := workflow.ExecuteActivity(
		ctx,
		(*Activities).GenerateAndStoreBountyEmbeddingActivity,
		embeddingActivityInput,
	).Get(ctx, nil); err != nil {
		logger.Error("GenerateAndStoreBountyEmbeddingActivity failed", "error", err)
		finalStatus = BountyFailedEmbedding // Set specific final status
		workflowErr = err                   // Store the error
		return workflowErr                  // Return the error to trigger deferred summary
	}

	// --- Input Validation ---
	if input.BountyPerPost == nil || !input.BountyPerPost.IsPositive() {
		errMsg := fmt.Sprintf("BountyPerPost must be a positive value, got: %v", input.BountyPerPost)
		logger.Error(errMsg)
		finalStatus = BountyFailedInternalError // Set specific final status
		workflowErr = temporal.NewApplicationError(errMsg, "INVALID_INPUT_BOUNTY_PER_POST")
		return workflowErr
	}
	if input.TotalBounty == nil || !input.TotalBounty.IsPositive() {
		errMsg := fmt.Sprintf("TotalBounty must be a positive value, got: %v", input.TotalBounty)
		logger.Error(errMsg)
		finalStatus = BountyFailedInternalError // Set specific final status
		workflowErr = temporal.NewApplicationError(errMsg, "INVALID_INPUT_TOTAL_BOUNTY")
		return workflowErr
	}
	// Ensure TotalCharged is not nil before comparing
	if input.TotalCharged == nil {
		errMsg := "TotalCharged cannot be nil"
		logger.Error(errMsg)
		finalStatus = BountyFailedInternalError // Set specific final status
		workflowErr = temporal.NewApplicationError(errMsg, "INVALID_INPUT_NIL_TOTAL_CHARGED")
		return workflowErr
	}
	// Use Cmp for USDCAmount comparison: input.TotalCharged < input.TotalBounty
	if input.TotalCharged.Cmp(input.TotalBounty) < 0 {
		errMsg := fmt.Sprintf("TotalCharged (%v) cannot be less than TotalBounty (%v)", input.TotalCharged.ToUSDC(), input.TotalBounty.ToUSDC())
		logger.Error(errMsg)
		finalStatus = BountyFailedInternalError // Set specific final status
		workflowErr = temporal.NewApplicationError(errMsg, "INVALID_INPUT_TOTAL_CHARGED_LESS_THAN_TOTAL_BOUNTY")
		return workflowErr
	}
	// Add other critical input validations as needed (e.g., wallet address formats, platform supported)
	// For wallet addresses, parsing them early can also act as validation:
	if _, err := solanago.PublicKeyFromBase58(input.EscrowWallet); err != nil {
		errMsg := fmt.Sprintf("Invalid EscrowWallet address: %s", input.EscrowWallet)
		logger.Error(errMsg, "error", err)
		finalStatus = BountyFailedInternalError // Set specific final status
		workflowErr = temporal.NewApplicationError(errMsg, "INVALID_ESCROW_WALLET", err)
		return workflowErr
	}
	if input.TreasuryWallet != "" { // Treasury wallet is optional
		if _, err := solanago.PublicKeyFromBase58(input.TreasuryWallet); err != nil {
			errMsg := fmt.Sprintf("Invalid TreasuryWallet address: %s", input.TreasuryWallet)
			logger.Error(errMsg, "error", err)
			finalStatus = BountyFailedInternalError // Set specific final status
			workflowErr = temporal.NewApplicationError(errMsg, "INVALID_TREASURY_WALLET", err)
			return workflowErr
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
	paymentResult, err := awaitBountyFund(ctx, input)
	if err != nil {
		finalStatus = BountyFailedAwaitingFunding // Set specific final status
		workflowErr = err                         // Store the error
		return workflowErr                        // Return the error to trigger deferred summary
	}
	funderWallet = paymentResult.FunderWallet // Store the inferred funder wallet

	// --- Execute Fee Transfer (Escrow -> Treasury) ---
	logger.Info("Executing fee transfer")

	// Update status before attempting fee transfer
	if err := workflow.UpsertTypedSearchAttributes(ctx, BountyStatusKey.ValueSet(string(BountyStatusTransferringFee))); err != nil {
		logger.Error("Failed to update search attribute BountyStatus to TransferringFee", "error", err)
		finalStatus = BountyFailedFeeTransfer // Set specific final status
		workflowErr = err                     // Store the error
		return workflowErr                    // Return the error to trigger deferred summary
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
			if err := workflow.ExecuteActivity(
				workflow.WithActivityOptions(ctx, transferOpts),
				(*Activities).TransferUSDC,
				input.TreasuryWallet,
				feeAmount.ToUSDC(),
				feeTransferMemo,
			).Get(ctx, nil); err != nil {
				logger.Error("Failed to execute fee transfer activity", "error", err)
				finalStatus = BountyFailedFeeTransfer // Set specific final status
				workflowErr = fmt.Errorf("failed to execute fee transfer: %w", err)
				return workflowErr // Return the error to trigger deferred summary
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
	if err := workflow.UpsertTypedSearchAttributes(ctx,
		BountyTotalAmountKey.ValueSet(input.TotalBounty.ToUSDC()),
		BountyPerPostAmountKey.ValueSet(input.BountyPerPost.ToUSDC()),
		BountyValueRemainingKey.ValueSet(input.TotalBounty.ToUSDC()),
		BountyTierKey.ValueSet(int64(input.Tier)),
		BountyFunderWalletKey.ValueSet(funderWallet), // Set the inferred funder wallet
	); err != nil {
		logger.Error("Failed to upsert initial bounty value search attributes", "error", err)
		// Decide if this should be fatal or just a warning
		finalStatus = BountyFailedInternalError // Set specific final status
		workflowErr = err                       // Store the error
		return workflowErr                      // Return the error to trigger deferred summary
	}

	// Update status after fee transfer (or skip) before listening loop
	if err := workflow.UpsertTypedSearchAttributes(ctx, BountyStatusKey.ValueSet(string(BountyStatusListening))); err != nil {
		logger.Error("Failed to update search attribute BountyStatus to Listening", "error", err)
		finalStatus = BountyFailedInternalError // Set specific final status
		workflowErr = err                       // Store the error
		return workflowErr                      // Return the error to trigger deferred summary
	}

	// loop to process content submissions
	workflowErr = awaitLoopUntilEmptyOrTimeout(ctx, input, successfullyPaidIDs, &finalStatus, amountRefunded, totalAmountPaid, funderWallet)
	// workflowErr from awaitLoopUntilEmptyOrTimeout will be handled by the defer block if not nil.
	// If it's nil, the finalStatus should have been set correctly within the loop.
	return workflowErr
}

// PlatformDependencies is an interface for platform-specific dependencies
type PlatformDependencies interface {
	// Type returns the platform type
	Type() PlatformKind
}

// ContentProvider is an interface for retrieving content from a platform
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
		BountyID string `json:"bounty_id"`
	}
	memoData := DirectPaymentMemo{BountyID: workflow.GetInfo(ctx).WorkflowExecution.ID}
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
			NonRetryableErrorTypes: []string{"TimeoutError", "PaymentVerificationTimeout"},
		},
	}
	verifyPaymentCtx := workflow.WithActivityOptions(ctx, verifyPaymentActivityOptions)

	var verifyResult *VerifyPaymentResult
	err = workflow.ExecuteActivity(
		verifyPaymentCtx,
		(*Activities).VerifyPayment,
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
	successfullyPaidIDs map[string]PayoutDetail,
	finalStatus *BountyCompletionStatus, // Pointer to update final status
	amountRefunded *solana.USDCAmount, // Pointer to update amount refunded
	totalAmountPaid *solana.USDCAmount, // Pointer to update total amount paid
	funderWallet string, // The inferred funder wallet
) error {
	logger := workflow.GetLogger(ctx)
	workflowID := workflow.GetInfo(ctx).WorkflowExecution.ID

	remainingBounty, err := solana.NewUSDCAmount(input.TotalBounty.ToUSDC())
	if err != nil {
		logger.Error("Failed to create mutable copy of remaining bounty", "error", err)
		*finalStatus = BountyFailedInternalError // Set specific final status
		return fmt.Errorf("failed to initialize remaining bounty: %w", err)
	}
	failedAttemptsCooldown := make(map[string]time.Time) // Stores timestamp of last failure for a contentID
	walletsThatReceivedPayout := make(map[string]bool)   // Stores wallets that have already received a payout

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
			*finalStatus = BountyCompletedEmpty
			return nil
		}

		// If remaining bounty is not enough for another full payout, refund and end.
		if remainingBounty.Cmp(input.BountyPerPost) < 0 {
			logger.Info("Remaining bounty is less than one payout, ending workflow and refunding remainder.", "remaining", remainingBounty.ToUSDC(), "per_post", input.BountyPerPost.ToUSDC())
			cancelTimer() // Stop the main timer.

			// Set final status
			*finalStatus = BountyCompletedEmpty

			// --- Refund Logic ---
			amountToRefund := remainingBounty
			refundCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: DefaultPayoutTimeout})

			type RefundMemo struct {
				BountyID string `json:"bounty_id"`
				Reason   string `json:"reason"`
			}
			memoData := RefundMemo{BountyID: workflowID, Reason: "Insufficient funds for full payout"}
			memoBytes, mErr := json.Marshal(memoData)
			if mErr != nil {
				logger.Error("Failed to marshal refund memo", "error", mErr)
				memoBytes = []byte(`{"reason":"insufficient_funds"}`) // Fallback
			}
			memoString := string(memoBytes)

			refundErr := workflow.ExecuteActivity(refundCtx, (*Activities).TransferUSDC, funderWallet, amountToRefund.ToUSDC(), memoString).Get(refundCtx, nil)

			if refundErr != nil {
				logger.Error("Failed to return remaining bounty to owner", "owner_wallet", funderWallet, "amount", amountToRefund.ToUSDC(), "error", refundErr)
			} else {
				logger.Info("Successfully returned remaining bounty to owner")
				*amountRefunded = *remainingBounty
				remainingBounty = solana.Zero()
				err = workflow.UpsertTypedSearchAttributes(ctx, BountyValueRemainingKey.ValueSet(0.0))
				if err != nil {
					logger.Error("Failed to update search attribute BountyValueRemaining to 0 on refund", "error", err)
				}
			}
			// --- End of Refund Logic ---
			return nil // End the workflow.
		}

		selector := workflow.NewSelector(ctx)
		selector.AddFuture(timerFuture, func(f workflow.Future) {
			logger.Info("Bounty assessment period timed out.")
			fErr := f.Get(ctx, nil)
			if fErr != nil {
				logger.Error("Timer future failed but proceeding with potential refund", "error", fErr)
				*finalStatus = BountyTimedOutRefunded
			}

			// Update status before attempting timeout refund
			err := workflow.UpsertTypedSearchAttributes(ctx, BountyStatusKey.ValueSet(string(BountyStatusRefunded)))
			if err != nil {
				logger.Error("Failed to update search attribute BountyStatus to Refunded", "error", err)
				*finalStatus = BountyTimedOutRefunded
			}

			// Implement refund logic similar to cancel signal
			if !remainingBounty.IsZero() {
				amountToRefund := remainingBounty
				logger.Info("Attempting to return remaining bounty to owner due to timeout", "owner_wallet", funderWallet, "amount", amountToRefund.ToUSDC())
				// Use a context that won't be immediately cancelled by the timer completing
				refundCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: DefaultPayoutTimeout})

				// --- Create Memo for Timeout Refund --- //
				type RefundMemo struct {
					BountyID string `json:"bounty_id"`
				}
				memoData := RefundMemo{BountyID: workflowID}
				memoBytes, mErr := json.Marshal(memoData)
				if mErr != nil {
					logger.Error("Failed to marshal refund memo", "error", mErr)
					memoBytes = []byte("{}")
				}
				memoString := string(memoBytes)
				// --- End Memo Creation --- //

				// Call TransferUSDC activity with memo
				refundErr := workflow.ExecuteActivity(refundCtx, (*Activities).TransferUSDC, funderWallet, amountToRefund.ToUSDC(), memoString).Get(refundCtx, nil)
				if refundErr != nil {
					// Log error but don't fail the workflow, timeout already happened
					logger.Error("Failed to return remaining bounty to owner on timeout", "owner_wallet", funderWallet, "amount", amountToRefund.ToUSDC(), "error", refundErr)
					// finalStatus remains BountyTimedOutRefunded but refund failed. The summary will reflect actual amountRefunded.
				} else {
					logger.Info("Successfully returned remaining bounty to owner on timeout")
					*amountRefunded = *remainingBounty // remainingBounty is *solana.USDCAmount
					remainingBounty = solana.Zero()    // Set remaining bounty to zero after successful refund
					// Update remaining value SA
					err = workflow.UpsertTypedSearchAttributes(ctx, BountyValueRemainingKey.ValueSet(0.0))
					if err != nil {
						logger.Error("Failed to update search attribute BountyValueRemaining to 0 on timeout refund", "error", err)
					}
					*finalStatus = BountyTimedOutRefunded
				}
			} else {
				logger.Info("Timeout occurred, but remaining bounty was already zero.")
				*finalStatus = BountyTimedOutNoRefundNeeded
				// Ensure SA is 0 if it somehow wasn't already
				err = workflow.UpsertTypedSearchAttributes(ctx, BountyValueRemainingKey.ValueSet(0.0))
				if err != nil {
					logger.Error("Failed to update search attribute BountyValueRemaining to 0 on timeout (already zero bounty)", "error", err)
				}
				*finalStatus = BountyTimedOutNoRefundNeeded
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

			// --- Check if wallet has already been paid for this bounty (moved earlier) ---
			if _, walletAlreadyPaid := walletsThatReceivedPayout[signal.PayoutWallet]; walletAlreadyPaid {
				logger.Info("Payout wallet has already received a payout for this bounty, skipping further processing.", "ContentID", signal.ContentID, "PayoutWallet", signal.PayoutWallet)
				return // Continue to next Select iteration
			}
			// --- End wallet already paid check ---

			// 1. Pull Content using PullContentActivity
			var contentBytes []byte
			var pullErr error
			pullActivityInput := PullContentInput{
				PlatformType: signal.Platform,
				ContentKind:  signal.ContentKind,
				ContentID:    signal.ContentID,
			}
			pullOpts := workflow.ActivityOptions{
				StartToCloseTimeout: 60 * time.Second,
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

			// Determine if image analysis is needed based on requirements
			var shouldAnalyzeImgResult ShouldPerformImageAnalysisResult
			shouldAnalyzeOpts := workflow.ActivityOptions{
				StartToCloseTimeout: 60 * time.Second, // Shorter timeout for this simpler LLM call
				RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3},
			}
			shouldAnalyzeCtx := workflow.WithActivityOptions(ctx, shouldAnalyzeOpts)
			shouldAnalyzeErr := workflow.ExecuteActivity(shouldAnalyzeCtx, (*Activities).ShouldPerformImageAnalysisActivity, input.Requirements).Get(shouldAnalyzeCtx, &shouldAnalyzeImgResult)

			if shouldAnalyzeErr != nil {
				logger.Warn("ShouldPerformImageAnalysisActivity failed. Proceeding without image analysis.", "ContentID", signal.ContentID, "error", shouldAnalyzeErr)
				// If we can't determine, err on the side of not doing image analysis to avoid unnecessary failures/costs
				shouldAnalyzeImgResult.ShouldAnalyze = false
				shouldAnalyzeImgResult.Reason = "Failed to determine if image analysis was required: " + shouldAnalyzeErr.Error()
			}

			logger.Info("Image analysis requirement check result", "ContentID", signal.ContentID, "ShouldAnalyze", shouldAnalyzeImgResult.ShouldAnalyze, "Reason", shouldAnalyzeImgResult.Reason)

			// 2. Image Analysis (if applicable, based on unmarshalled content)
			thumbnailAnalysisFailed := false
			var imgAnalysisResult CheckContentRequirementsResult // Stores result from AnalyzeImageURL

			if shouldAnalyzeImgResult.ShouldAnalyze {
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
								"Analyze this Reddit thumbnail/image. Task: Does it *visually violate* requirements pertaining to the image? Requirements: '%s'.\n"+
									"Response Rules:\n"+
									"1. If CLEAR violation: {\"satisfies\": false, \"reason\": \"Explain violation.\"}\n"+
									"2. ELSE (no clear violation, or requirement not image-assessable like view counts): {\"satisfies\": true, \"reason\": \"No visual violation, or requirement not visually assessable from image.\"}\n"+
									"Format: JSON {\"satisfies\": boolean, \"reason\": \"string\"}", requirementsStringForImagePrompt)
							imgErr := workflow.ExecuteActivity(imgAnalysisCtx, (*Activities).AnalyzeImageURL, thumbnailURL, imageAnalysisPrompt).Get(imgAnalysisCtx, &imgAnalysisResult)
							if imgErr != nil {
								logger.Error("Image analysis activity execution failed for Reddit content", "ContentID", signal.ContentID, "error", imgErr)
								thumbnailAnalysisFailed = true // Activity execution error implies failure to analyze
							} else if !imgAnalysisResult.Satisfies {
								logger.Info("Image analysis determined Reddit thumbnail/image does not meet requirements (or could not be assessed)", "ContentID", signal.ContentID, "reason", imgAnalysisResult.Reason)
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
								"Analyze this YouTube thumbnail. Task: Does it *visually violate* requirements pertaining to the image? Requirements: '%s'.\n"+
									"Response Rules:\n"+
									"1. If CLEAR violation: {\"satisfies\": false, \"reason\": \"Explain violation.\"}\n"+
									"2. ELSE (no clear violation, or requirement not image-assessable like view counts): {\"satisfies\": true, \"reason\": \"No visual violation, or requirement not visually assessable from image.\"}\n"+
									"Format: JSON {\"satisfies\": boolean, \"reason\": \"string\"}", requirementsStringForImagePrompt)
							imgErr := workflow.ExecuteActivity(imgAnalysisCtx, (*Activities).AnalyzeImageURL, thumbnailURL, imageAnalysisPrompt).Get(imgAnalysisCtx, &imgAnalysisResult)
							if imgErr != nil {
								logger.Error("Image analysis activity execution failed for YouTube content", "ContentID", signal.ContentID, "error", imgErr)
								thumbnailAnalysisFailed = true // Activity execution error implies failure to analyze
							} else if !imgAnalysisResult.Satisfies {
								logger.Info("Image analysis determined YouTube thumbnail does not meet requirements (or could not be assessed)", "ContentID", signal.ContentID, "reason", imgAnalysisResult.Reason)
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
						imageUrl := strings.ReplaceAll(strings.ReplaceAll(thumbnailURL, "%_width%", "100"), "%_height%", "100")
						imageAnalysisPrompt := fmt.Sprintf(
							"Analyze this Twitch thumbnail. Task: Does it *visually violate* requirements pertaining to the image? Requirements: '%s'.\n"+
								"Response Rules:\n"+
								"1. If CLEAR violation: {\"satisfies\": false, \"reason\": \"Explain violation.\"}\n"+
								"2. ELSE (no clear violation, or requirement not image-assessable like view counts): {\"satisfies\": true, \"reason\": \"No visual violation, or requirement not visually assessable from image.\"}\n"+
								"Format: JSON {\"satisfies\": boolean, \"reason\": \"string\"}", requirementsStringForImagePrompt)
						imgErr := workflow.ExecuteActivity(imgAnalysisCtx, (*Activities).AnalyzeImageURL, imageUrl, imageAnalysisPrompt).Get(imgAnalysisCtx, &imgAnalysisResult)
						if imgErr != nil {
							logger.Error("Image analysis activity execution failed for Twitch content", "ContentID", signal.ContentID, "error", imgErr)
							thumbnailAnalysisFailed = true // Activity execution error implies failure to analyze
						} else if !imgAnalysisResult.Satisfies {
							logger.Info("Image analysis determined Twitch thumbnail does not meet requirements (or could not be assessed)", "ContentID", signal.ContentID, "reason", imgAnalysisResult.Reason)
							thumbnailAnalysisFailed = true
						}
					}
				case PlatformHackerNews:
					logger.Debug("No thumbnail analysis implemented for HackerNews", "ContentID", signal.ContentID)
				case PlatformBluesky:
					// Placeholder for potential future Bluesky image analysis if embeds are parsed
					logger.Debug("No explicit thumbnail analysis implemented for Bluesky yet", "ContentID", signal.ContentID)
				case PlatformInstagram:
					// Placeholder for Instagram image analysis if needed
					logger.Debug("No explicit thumbnail analysis implemented for Instagram yet", "ContentID", signal.ContentID)
				case PlatformIncentivizeThis:
					logger.Debug("No thumbnail analysis needed for IncentivizeThis platform", "ContentID", signal.ContentID)
				case PlatformTripAdvisor:
					logger.Debug("No thumbnail analysis implemented for Tripadvisor", "ContentID", signal.ContentID)
				default:
					logger.Warn("Thumbnail analysis not implemented for platform or platform unhandled in switch", "platform", signal.Platform, "ContentID", signal.ContentID)
				}
			} else {
				logger.Info("Skipping image analysis as requirements do not necessitate it.", "ContentID", signal.ContentID, "Reason", shouldAnalyzeImgResult.Reason)
				// Ensure imgAnalysisResult has a default state indicating no failure if skipped
				imgAnalysisResult.Satisfies = true
				imgAnalysisResult.Reason = "Image analysis not required by bounty terms."
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

				if !payoutAmount.IsZero() {
					payOpts := workflow.ActivityOptions{StartToCloseTimeout: DefaultPayoutTimeout}
					type PayoutMemo struct {
						BountyID    string       `json:"bounty_id"`
						ContentID   string       `json:"content_id"`
						Platform    PlatformKind `json:"platform,omitempty"`
						ContentKind ContentKind  `json:"content_kind,omitempty"`
					}
					memoData := PayoutMemo{BountyID: workflowID, ContentID: signal.ContentID, Platform: signal.Platform, ContentKind: signal.ContentKind}
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
						totalAmountPaid = (*totalAmountPaid).Add(payoutAmount)
						remainingBounty = remainingBounty.Sub(payoutAmount)
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

						// --- Record wallet as having received a payout ---
						walletsThatReceivedPayout[signal.PayoutWallet] = true
						logger.Debug("Recorded wallet in walletsThatReceivedPayout map", "PayoutWallet", signal.PayoutWallet)
						// --- End Record Payout Detail ---

						// Set status back to listening after successful payout
						err = workflow.UpsertTypedSearchAttributes(ctx, BountyStatusKey.ValueSet(string(BountyStatusListening)))
						if err != nil {
							logger.Error("Failed to update search attribute BountyStatus back to Listening after successful payout", "error", err)
						}
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
				logger.Info("Custom cancel signal channel closed.")
				return
			}
			var cancelSignal CancelBountySignal
			c.Receive(ctx, &cancelSignal)
			logger.Info("Received custom cancel signal", "BountyOwnerWallet", cancelSignal.BountyOwnerWallet)
			cancelTimer()

			if cancelSignal.BountyOwnerWallet != funderWallet {
				logger.Warn("Received custom cancel signal from incorrect owner", "received_owner", cancelSignal.BountyOwnerWallet, "expected_owner", funderWallet)
				return
			}

			// Update status before attempting cancel refund
			err := workflow.UpsertTypedSearchAttributes(ctx, BountyStatusKey.ValueSet(string(BountyStatusCancelled)))
			if err != nil {
				logger.Error("Failed to update search attribute BountyStatus to Cancelled on custom signal", "error", err)
			}

			if !remainingBounty.IsZero() {
				amountToRefund := remainingBounty
				logger.Info("Attempting to return remaining bounty to owner due to custom cancel signal", "owner_wallet", funderWallet, "amount", amountToRefund.ToUSDC())
				cancelOpts := workflow.ActivityOptions{StartToCloseTimeout: DefaultPayoutTimeout}
				// --- Create Memo for Cancellation Refund --- //
				type CancelRefundMemo struct {
					BountyID string `json:"bounty_id"`
				}
				memoData := CancelRefundMemo{BountyID: workflowID}
				memoBytes, mErr := json.Marshal(memoData)
				if mErr != nil {
					logger.Error("Failed to marshal cancel refund memo (custom signal)", "error", mErr)
					memoBytes = []byte("{}")
				}
				memoString := string(memoBytes)
				// --- End Memo Creation --- //
				refundErr := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, cancelOpts), (*Activities).TransferUSDC, funderWallet, amountToRefund.ToUSDC(), memoString).Get(ctx, nil)
				if refundErr != nil {
					logger.Error("Failed to return remaining bounty to owner (custom signal)", "owner_wallet", funderWallet, "amount", amountToRefund.ToUSDC(), "error", refundErr)
					// finalStatus will be set based on whether refund happened or not for summary
					*finalStatus = BountyCompletedCancelled // Even if refund fails, it was intended.
				} else {
					logger.Info("Successfully returned remaining bounty to owner (custom signal)")
					*amountRefunded = *remainingBounty // remainingBounty is *solana.USDCAmount
					remainingBounty = solana.Zero()
					// Update remaining value SA
					err = workflow.UpsertTypedSearchAttributes(ctx, BountyValueRemainingKey.ValueSet(0.0))
					if err != nil {
						logger.Error("Failed to update search attribute BountyValueRemaining to 0 on custom cancellation refund", "error", err)
					}
					*finalStatus = BountyCompletedCancelled
				}
			} else {
				logger.Info("Workflow cancelled by custom signal, no refund needed as bounty was zero.")
				*finalStatus = BountyCompletedCancelled
			}
			// Workflow will exit loop after this due to timer cancellation and subsequent select returning ctx.Err()
		})

		// Listen for generic workflow cancellation from context
		selector.AddReceive(ctx.Done(), func(c workflow.ReceiveChannel, more bool) {
			logger.Info("Workflow cancellation requested via context.Done()")
			cancelTimer() // Stop the main bounty timeout timer

			// Update status to Cancelled
			err := workflow.UpsertTypedSearchAttributes(ctx, BountyStatusKey.ValueSet(string(BountyStatusCancelled)))
			if err != nil {
				logger.Error("Failed to update search attribute BountyStatus to Cancelled on context cancellation", "error", err)
			}

			if !remainingBounty.IsZero() {
				amountToRefund := remainingBounty
				logger.Info("Attempting to return remaining bounty to owner due to context cancellation", "owner_wallet", funderWallet, "amount", amountToRefund.ToUSDC())
				// Use a disconnected context for refund activity if main context is already cancelled
				disconnectedRefundCtx, _ := workflow.NewDisconnectedContext(ctx)
				refundActivityOpts := workflow.ActivityOptions{StartToCloseTimeout: DefaultPayoutTimeout}
				activityCtx := workflow.WithActivityOptions(disconnectedRefundCtx, refundActivityOpts)

				// --- Create Memo for Cancellation Refund --- //
				type CancelRefundMemo struct {
					BountyID string `json:"bounty_id"`
				}
				memoData := CancelRefundMemo{BountyID: workflowID}
				memoBytes, mErr := json.Marshal(memoData)
				if mErr != nil {
					logger.Error("Failed to marshal cancel refund memo (context cancellation)", "error", mErr)
					memoBytes = []byte("{}")
				}
				memoString := string(memoBytes)
				// --- End Memo Creation --- //

				refundErr := workflow.ExecuteActivity(activityCtx, (*Activities).TransferUSDC, funderWallet, amountToRefund.ToUSDC(), memoString).Get(activityCtx, nil)
				if refundErr != nil {
					logger.Error("Failed to return remaining bounty to owner (context cancellation)", "owner_wallet", funderWallet, "amount", amountToRefund.ToUSDC(), "error", refundErr)
					*finalStatus = BountyCompletedCancelled // Still mark as intended for refund
				} else {
					logger.Info("Successfully returned remaining bounty to owner (context cancellation)")
					*amountRefunded = *remainingBounty
					remainingBounty = solana.Zero()
					err = workflow.UpsertTypedSearchAttributes(ctx, BountyValueRemainingKey.ValueSet(0.0))
					if err != nil {
						logger.Error("Failed to update search attribute BountyValueRemaining to 0 on context cancellation refund", "error", err)
					}
					*finalStatus = BountyCompletedCancelled
				}
			} else {
				logger.Info("Workflow cancelled via context, no refund needed as bounty was zero.")
				*finalStatus = BountyCompletedCancelled
			}
			// Workflow will exit loop because cancelTimer() was called and ctx.Err() will be non-nil.
		})

		selector.Select(ctx)

		if ctx.Err() != nil {
			logger.Warn("Workflow context done, exiting loop", "error", ctx.Err())
			// If context error occurs and status not set, it's an internal error or unhandled cancellation
			if *finalStatus == "" {
				*finalStatus = BountyFailedInternalError
			}
			return ctx.Err()
		}
	}
}

// PublishNewBountyWorkflowInput defines the input for publishing a newly created bounty
type PublishNewBountyWorkflowInput struct {
	BountyID string `json:"bounty_id"`
}

// PublishNewBountyWorkflow publishes a newly created bounty to notification channels
func PublishNewBountyWorkflow(ctx workflow.Context, input PublishNewBountyWorkflowInput) error {
	logger := workflow.GetLogger(ctx)
	logger.Info("PublishNewBountyWorkflow started", "bounty_id", input.BountyID)

	// Set activity options with a reasonable timeout
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 3 * time.Minute, // Shorter timeout for single bounty
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    2 * time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    30 * time.Second,
			MaximumAttempts:    3,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	// Execute activities to publish the new bounty
	redditErr := workflow.ExecuteActivity(ctx, (*Activities).PublishNewBountyReddit, input.BountyID).Get(ctx, nil)
	discordErr := workflow.ExecuteActivity(ctx, (*Activities).PublishNewBountyDiscord, input.BountyID).Get(ctx, nil)

	if redditErr != nil || discordErr != nil {
		// Log individual errors but don't fail the workflow - notification is not critical
		if redditErr != nil {
			logger.Error("Failed to publish new bounty to Reddit", "bounty_id", input.BountyID, "error", redditErr)
		}
		if discordErr != nil {
			logger.Error("Failed to publish new bounty to Discord", "bounty_id", input.BountyID, "error", discordErr)
		}
		// Return error to trigger retry, but this is non-critical so we could also just log and continue
		return fmt.Errorf("failed to publish new bounty to notification channels: Reddit error: %v, Discord error: %v", redditErr, discordErr)
	}

	logger.Info("PublishNewBountyWorkflow completed successfully", "bounty_id", input.BountyID)
	return nil
}

// --- Email Token Workflow ---

// EmailTokenWorkflowInput defines the input for the email sending workflow.
type EmailTokenWorkflowInput struct {
	SaleID         string              `json:"sale_id"`
	Email          string              `json:"email"`
	Token          string              `json:"token"`
	SourcePlatform PaymentPlatformKind `json:"source_platform,omitempty"`
}

// EmailTokenWorkflow sends an email containing a token to the specified address.
// It also now calls an activity to mark the corresponding Gumroad sale as notified,
// but this isn't necessary for BMC because that's webhook-based.
func EmailTokenWorkflow(ctx workflow.Context, input EmailTokenWorkflowInput) error {
	logger := workflow.GetLogger(ctx)
	logger.Info("EmailTokenWorkflow started", "email", input.Email, "saleID", input.SaleID)

	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 1 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    5 * time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    time.Minute,
			MaximumAttempts:    3,
		},
	}
	activityCtx := workflow.WithActivityOptions(ctx, ao)

	err := workflow.ExecuteActivity(activityCtx, (*Activities).SendTokenEmail, input.Email, input.Token).Get(activityCtx, nil)
	if err != nil {
		logger.Error("SendEmailActivity failed", "error", err, "email", input.Email, "saleID", input.SaleID)
		return fmt.Errorf("failed to send token email for sale %s: %w", input.SaleID, err)
	}
	logger.Info("Successfully sent token email", "email", input.Email, "saleID", input.SaleID)

	// After successfully sending the email, mark the sale as notified if it's from Gumroad.
	if input.SourcePlatform == PlatformGumroad { // Check the source platform
		markNotifiedInput := MarkGumroadSaleNotifiedActivityInput{
			SaleID: input.SaleID,
			APIKey: input.Token,
		}

		markNotifiedActivityOpts := workflow.ActivityOptions{
			StartToCloseTimeout: 1 * time.Minute,
			RetryPolicy: &temporal.RetryPolicy{
				InitialInterval:    5 * time.Second,
				BackoffCoefficient: 2.0,
				MaximumInterval:    time.Minute,
				MaximumAttempts:    3,
			},
		}
		markNotifiedCtx := workflow.WithActivityOptions(ctx, markNotifiedActivityOpts)

		err = workflow.ExecuteActivity(markNotifiedCtx, (*Activities).MarkGumroadSaleNotifiedActivity, markNotifiedInput).Get(markNotifiedCtx, nil)
		if err != nil {
			logger.Error("MarkGumroadSaleNotifiedActivity failed after sending email for Gumroad sale", "error", err, "saleID", input.SaleID)
		} else {
			logger.Info("Successfully marked Gumroad sale as notified", "saleID", input.SaleID)
		}
	} else {
		logger.Info("Skipping MarkGumroadSaleNotifiedActivity for non-Gumroad source", "sourcePlatform", input.SourcePlatform, "saleID", input.SaleID)
	}

	logger.Info("EmailTokenWorkflow completed successfully", "email", input.Email, "saleID", input.SaleID)
	return nil
}

func PruneStaleEmbeddingsWorkflow(ctx workflow.Context) (string, error) {
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    10 * time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    2 * time.Minute,
			MaximumAttempts:    3,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	var result string
	err := workflow.ExecuteActivity(ctx, (*Activities).PruneStaleEmbeddingsActivity, PruneStaleEmbeddingsActivityInput{}).Get(ctx, &result)
	if err != nil {
		return "", fmt.Errorf("PruneStaleEmbeddingsActivity failed: %w", err)
	}

	return result, nil
}

// GumroadNotifyWorkflowInput defines the input for the ScheduledGumroadNotifyWorkflow.
// It specifies how far back the notify job should look.
type GumroadNotifyWorkflowInput struct {
	LookbackDuration time.Duration `json:"lookback_duration"`
}

// GumroadNotifyWorkflow is a simple workflow that calls an activity
// to trigger Gumroad notifications based on a lookback duration.
// This workflow is intended to be run on a schedule (e.g., every 5 minutes).
func GumroadNotifyWorkflow(ctx workflow.Context, input GumroadNotifyWorkflowInput) error {
	logger := workflow.GetLogger(ctx)
	logger.Info("GumroadNotifyWorkflow started", "lookbackDuration", input.LookbackDuration)

	activityOptions := workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute, // Timeout for the activity call
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    10 * time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    time.Minute,
			MaximumAttempts:    3,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, activityOptions)

	activityInput := CallGumroadNotifyActivityInput{
		LookbackDuration: input.LookbackDuration,
	}

	err := workflow.ExecuteActivity(ctx, (*Activities).CallGumroadNotifyActivity, activityInput).Get(ctx, nil)
	if err != nil {
		logger.Error("GumroadNotifyWorkflow failed to execute CallGumroadNotifyActivity", "error", err)
		return fmt.Errorf("GumroadNotifyActivity failed: %w", err)
	}

	logger.Info("GumroadNotifyWorkflow completed successfully")
	return nil
}

// --- End Scheduled Gumroad Notify Workflow ---

type ContactUsNotifyWorkflowInput struct {
	Name    string
	Email   string
	Message string
}

func ContactUsNotifyWorkflow(ctx workflow.Context, input ContactUsNotifyWorkflowInput) error {
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 1 * time.Minute,
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	var activities *Activities
	err := workflow.ExecuteActivity(ctx, activities.SendContactUsEmail, input).Get(ctx, nil)
	if err != nil {
		workflow.GetLogger(ctx).Error("failed to send contact us email", "error", err)
		return err
	}
	return nil
}
