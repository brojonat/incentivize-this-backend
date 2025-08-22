package abb

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/brojonat/affiliate-bounty-board/solana"
	solanago "github.com/gagliardetto/solana-go"
	"go.temporal.io/sdk/log"
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
	OrchestratorMaxTurns                                = 20
)

// BountyStatus defines the possible states of a bounty workflow.
type BountyStatus string

// Bounty Status Constants
const (
	BountyStatusAwaitingFunding BountyStatus = "AwaitingFunding"
	BountyStatusListening       BountyStatus = "Listening"
	BountyStatusPaying          BountyStatus = "Paying"
	BountyStatusCancelled       BountyStatus = "Cancelled"
	BountyStatusDrained         BountyStatus = "Drained"
	BountyStatusClosed          BountyStatus = "Closed"
)

// Signal Name Constants
const (
	AssessmentSignalName        = "assessment"
	GetPaidBountiesQueryType    = "getPaidBounties"
	GetBountyDetailsQueryType   = "getBountyDetails"
	DefaultPayoutTimeout        = 10 * time.Minute
	ContentReassessmentCooldown = 24 * time.Hour
)

// AssessContentSignal represents a signal to assess content against bounty requirements
type AssessContentSignal struct {
	ContentID    string       `json:"content_id"`
	PayoutWallet string       `json:"payout_wallet"`
	Platform     PlatformKind `json:"platform"`
	ContentKind  ContentKind  `json:"content_kind"`
}

// BountyDetails represents the detailed state of a bounty, suitable for querying.
type BountyDetails struct {
	BountyID                string       `json:"bounty_id"`
	Title                   string       `json:"title"`
	Status                  BountyStatus `json:"status"`
	Requirements            []string     `json:"requirements"`
	BountyPerPost           float64      `json:"bounty_per_post"`
	TotalBounty             float64      `json:"total_bounty"`
	TotalCharged            float64      `json:"total_charged"`
	RemainingBountyValue    float64      `json:"remaining_bounty_value"`
	BountyFunderWallet      string       `json:"bounty_funder_wallet,omitempty"`
	PlatformKind            PlatformKind `json:"platform_kind"`
	ContentKind             ContentKind  `json:"content_kind"`
	Tier                    BountyTier   `json:"tier"`
	CreatedAt               time.Time    `json:"created_at"`
	EndAt                   time.Time    `json:"end_at"`
	PaymentTimeoutExpiresAt *time.Time   `json:"payment_timeout_expires_at,omitempty"`
}

// BountyAssessmentWorkflowInput represents the input parameters for the workflow
type BountyAssessmentWorkflowInput struct {
	Title             string             `json:"title"`
	Requirements      []string           `json:"requirements"`
	BountyPerPost     *solana.USDCAmount `json:"bounty_per_post"`
	TotalBounty       *solana.USDCAmount `json:"total_bounty"`
	TotalCharged      *solana.USDCAmount `json:"total_charged"`
	Platform          PlatformKind       `json:"platform"`
	ContentKind       ContentKind        `json:"content_kind"`
	Tier              BountyTier         `json:"tier"`
	PaymentTimeout    time.Duration      `json:"payment_timeout"`
	Timeout           time.Duration      `json:"timeout"`
	TreasuryWallet    string             `json:"treasury_wallet"`
	EscrowWallet      string             `json:"escrow_wallet"`
	MaxPayoutsPerUser int                `json:"max_payouts_per_user"`
}

// BountyState manages the state of the bounty within the workflow.
type BountyState struct {
	BountyID       string
	Status         BountyStatus
	Requirements   []string
	BountyPerPost  *solana.USDCAmount
	TotalBounty    *solana.USDCAmount
	TotalCharged   *solana.USDCAmount
	ValueRemaining *solana.USDCAmount
	Payouts        []PayoutDetail
	PayoutCounts   map[string]int
	Platform       PlatformKind
	ContentKind    ContentKind
	Timeout        time.Duration
	FailedClaims   map[string]time.Time
	FunderWallet   string
}

// BountyAssessmentWorkflow is the main workflow for managing a bounty.
func BountyAssessmentWorkflow(ctx workflow.Context, input BountyAssessmentWorkflowInput) error {
	logger := workflow.GetLogger(ctx)
	startTime := workflow.Now(ctx)
	ao := workflow.ActivityOptions{StartToCloseTimeout: 1 * time.Minute}
	ctx = workflow.WithActivityOptions(ctx, ao)
	a := &Activities{}

	bountyState := &BountyState{
		BountyID:       workflow.GetInfo(ctx).WorkflowExecution.ID,
		Status:         BountyStatusAwaitingFunding,
		Requirements:   input.Requirements,
		BountyPerPost:  input.BountyPerPost,
		ValueRemaining: input.TotalBounty,
		Payouts:        make([]PayoutDetail, 0),
		PayoutCounts:   make(map[string]int),
		Platform:       input.Platform,
		ContentKind:    input.ContentKind,
		Timeout:        input.Timeout,
		FailedClaims:   make(map[string]time.Time),
	}

	workflow.SetQueryHandler(ctx, GetPaidBountiesQueryType, func() ([]PayoutDetail, error) {
		return bountyState.Payouts, nil
	})

	workflow.SetQueryHandler(ctx, GetBountyDetailsQueryType, func() (*BountyDetails, error) {
		info := workflow.GetInfo(ctx)
		details := &BountyDetails{
			BountyID:             info.WorkflowExecution.ID,
			Title:                input.Title,
			Status:               bountyState.Status,
			Requirements:         input.Requirements,
			BountyPerPost:        input.BountyPerPost.ToUSDC(),
			TotalBounty:          input.TotalBounty.ToUSDC(),
			TotalCharged:         input.TotalCharged.ToUSDC(),
			RemainingBountyValue: bountyState.ValueRemaining.ToUSDC(),
			BountyFunderWallet:   bountyState.FunderWallet,
			PlatformKind:         input.Platform,
			ContentKind:          input.ContentKind,
			Tier:                 input.Tier,
			CreatedAt:            startTime,
			EndAt:                startTime.Add(input.Timeout),
		}
		if details.Status == BountyStatusAwaitingFunding {
			expiresAt := startTime.Add(input.PaymentTimeout)
			details.PaymentTimeoutExpiresAt = &expiresAt
		}
		return details, nil
	})

	// Wait for funding
	var verifyResult *VerifyPaymentResult
	expectedRecipient, err := solanago.PublicKeyFromBase58(input.EscrowWallet)
	if err != nil {
		logger.Error("Invalid escrow wallet address", "address", input.EscrowWallet, "error", err)
		return fmt.Errorf("invalid escrow wallet address: %w", err)
	}

	err = workflow.ExecuteActivity(ctx, a.VerifyPayment,
		expectedRecipient,
		input.TotalCharged,
		bountyState.BountyID,
		input.PaymentTimeout,
	).Get(ctx, &verifyResult)
	if err != nil {
		logger.Error("Funding check activity failed.", "error", err)
		return err // Returning error to let temporal handle retry/failure
	}
	if !verifyResult.Verified {
		logger.Error("Funding verification failed or timed out.", "error", verifyResult.Error)
		return nil // End workflow gracefully if not funded
	}
	bountyState.FunderWallet = verifyResult.FunderWallet
	bountyState.Status = BountyStatusListening
	logger.Info("Bounty funded. Listening for submissions.", "funder_wallet", verifyResult.FunderWallet)

	// Update search attributes to reflect the funded status
	if err := workflow.UpsertTypedSearchAttributes(ctx,
		BountyFunderWalletKey.ValueSet(verifyResult.FunderWallet),
		BountyStatusKey.ValueSet(string(BountyStatusListening)),
	); err != nil {
		logger.Error("Failed to upsert search attributes after funding", "error", err)
		// This is not a fatal error for the workflow itself, so just log it.
	}

	// Main loop for listening to signals, cancellation, or timing out
	assessmentChan := workflow.GetSignalChannel(ctx, AssessmentSignalName)

	for {
		// Check if insufficient balance for next payout
		if bountyState.ValueRemaining.IsPositive() && bountyState.ValueRemaining.Cmp(bountyState.BountyPerPost) < 0 {
			logger.Info("Insufficient balance for next payout. Closing bounty.",
				"remaining", bountyState.ValueRemaining, "needed", bountyState.BountyPerPost)
			bountyState.Status = BountyStatusDrained
			if err := workflow.UpsertTypedSearchAttributes(ctx, BountyStatusKey.ValueSet(string(BountyStatusDrained))); err != nil {
				logger.Error("Failed to upsert search attributes for drained status", "error", err)
			}
			break
		}

		selector := workflow.NewSelector(ctx)

		// Listen for assessment signals
		selector.AddReceive(assessmentChan, func(c workflow.ReceiveChannel, more bool) {
			var signal AssessContentSignal
			c.Receive(ctx, &signal)
			processClaim(ctx, a, bountyState, signal, logger, input)
		})

		// Listen for cancellation via the workflow's context
		selector.AddReceive(ctx.Done(), func(c workflow.ReceiveChannel, more bool) {
			logger.Info("Bounty cancellation requested")
			bountyState.Status = BountyStatusCancelled
			if err := workflow.UpsertTypedSearchAttributes(ctx, BountyStatusKey.ValueSet(string(BountyStatusCancelled))); err != nil {
				logger.Error("Failed to upsert search attributes for cancelled status", "error", err)
			}
		})

		// Listen for timeout
		selector.AddFuture(workflow.NewTimer(ctx, bountyState.Timeout), func(f workflow.Future) {
			logger.Info("Bounty timeout reached")
			bountyState.Status = BountyStatusClosed
			if err := workflow.UpsertTypedSearchAttributes(ctx, BountyStatusKey.ValueSet(string(BountyStatusClosed))); err != nil {
				logger.Error("Failed to upsert search attributes for closed status", "error", err)
			}
		})

		selector.Select(ctx)

		if bountyState.Status != BountyStatusListening {
			break
		}
	}

	// Send any remaining balance back to the funder wallet
	if bountyState.ValueRemaining.IsPositive() {
		logger.Info("Refunding remaining balance", "amount", bountyState.ValueRemaining, "funder_wallet", bountyState.FunderWallet)

		// Use a disconnected context to ensure refund completes even if the parent workflow is cancelled.
		refundCtx, cancel := workflow.NewDisconnectedContext(ctx)
		defer cancel()
		err := workflow.ExecuteActivity(refundCtx, a.RefundBountyActivity, bountyState.BountyID, bountyState.FunderWallet, bountyState.ValueRemaining).Get(refundCtx, nil)
		if err != nil {
			logger.Error("Failed to refund remaining balance", "error", err)
			return err
		}
		logger.Info("Successfully refunded remaining balance", "amount", bountyState.ValueRemaining)
	}

	logger.Info("BountyAssessmentWorkflow finished.")
	return nil
}

func processClaim(ctx workflow.Context, a *Activities, bountyState *BountyState, signal AssessContentSignal, logger log.Logger, bountyInput BountyAssessmentWorkflowInput) {
	maxPayouts := bountyInput.MaxPayoutsPerUser
	if maxPayouts <= 0 {
		maxPayouts = 1
	}

	if lastFailureTime, ok := bountyState.FailedClaims[signal.ContentID]; ok {
		if workflow.Now(ctx).Sub(lastFailureTime) < ContentReassessmentCooldown {
			logger.Info("Content is on cooldown.", "content_id", signal.ContentID)
			return
		}
	}

	if bountyState.PayoutCounts[signal.PayoutWallet] >= maxPayouts {
		logger.Info("User has reached the maximum payout limit for this bounty.", "wallet", signal.PayoutWallet)
		return
	}

	tools := []Tool{
		PullContentTool,
		AnalyzeImageURLTool,
		DetectMaliciousContentTool,
	}
	if signal.Platform == PlatformReddit {
		tools = append(tools, GetRedditChildrenCommentsTool)
	}
	if signal.Platform == PlatformGitHub {
		tools = append(tools, GetClosingPRTool)
	}

	var orchestratorResult OrchestratorWorkflowOutput
	orchErr := workflow.ExecuteChildWorkflow(ctx, OrchestratorWorkflow, OrchestratorWorkflowInput{
		Tools:         tools,
		Bounty:        bountyInput,
		InitialSignal: signal,
	}).Get(ctx, &orchestratorResult)

	if orchErr != nil || !orchestratorResult.IsApproved {
		logger.Error("Orchestrator rejected submission.", "error", orchErr, "reason", orchestratorResult.Reason)
		bountyState.FailedClaims[signal.ContentID] = workflow.Now(ctx)
		return
	}

	payoutAmount := bountyState.BountyPerPost

	if bountyState.ValueRemaining.Cmp(payoutAmount) < 0 {
		logger.Warn("Insufficient balance for payout", "requested", payoutAmount, "available", bountyState.ValueRemaining)
		bountyState.FailedClaims[signal.ContentID] = workflow.Now(ctx)
		return
	}

	payoutDetail := PayoutDetail{
		ContentID:    signal.ContentID,
		PayoutWallet: signal.PayoutWallet,
		Amount:       payoutAmount,
		Timestamp:    workflow.Now(ctx),
		Platform:     signal.Platform,
		ContentKind:  signal.ContentKind,
	}

	// Execute immediate payout
	payoutErr := workflow.ExecuteActivity(ctx, a.PayBountyActivity, bountyState.BountyID, payoutDetail.PayoutWallet, payoutDetail.Amount).Get(ctx, nil)
	if payoutErr != nil {
		logger.Error("Payout failed", "error", payoutErr, "wallet", payoutDetail.PayoutWallet)
		bountyState.FailedClaims[signal.ContentID] = workflow.Now(ctx)
		return
	}

	bountyState.Payouts = append(bountyState.Payouts, payoutDetail)
	bountyState.PayoutCounts[signal.PayoutWallet]++
	bountyState.ValueRemaining = bountyState.ValueRemaining.Sub(payoutAmount)
	logger.Info("Payout successful", "amount", payoutAmount, "wallet", signal.PayoutWallet, "content_id", signal.ContentID)

	searchAttributes := []temporal.SearchAttributeUpdate{
		BountyValueRemainingKey.ValueSet(bountyState.ValueRemaining.ToUSDC()),
	}

	if bountyState.ValueRemaining.IsZero() {
		bountyState.Status = BountyStatusDrained
		searchAttributes = append(searchAttributes, BountyStatusKey.ValueSet(string(BountyStatusDrained)))
	}

	if err := workflow.UpsertTypedSearchAttributes(ctx, searchAttributes...); err != nil {
		logger.Error("Failed to upsert search attributes after processing claim", "error", err)
	}
}

type OrchestratorWorkflowInput struct {
	Tools         []Tool
	Bounty        BountyAssessmentWorkflowInput
	InitialSignal AssessContentSignal
}

type OrchestratorWorkflowOutput struct {
	IsApproved bool
	Reason     string
}

func OrchestratorWorkflow(ctx workflow.Context, input OrchestratorWorkflowInput) (*OrchestratorWorkflowOutput, error) {
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)
	logger := workflow.GetLogger(ctx)
	a := &Activities{}

	var orchestratorPrompt string
	err := workflow.ExecuteActivity(ctx, a.GetOrchestratorPromptActivity).Get(ctx, &orchestratorPrompt)
	if err != nil {
		logger.Error("Failed to get orchestrator prompt", "error", err)
		return nil, err
	}

	bountyInfo := fmt.Sprintf(
		"You are assessing a piece of content for a bounty.\n\nBounty Title: %s\nBounty Requirements:\n- %s\n\nInitial content to assess:\nPlatform: %s\nContent Kind: %s\nContent ID: %s\n\nWhen you have fetched all the information you need, you MUST call the `submit_decision` tool.",
		input.Bounty.Title,
		strings.Join(input.Bounty.Requirements, "\n- "),
		input.InitialSignal.Platform,
		input.InitialSignal.ContentKind,
		input.InitialSignal.ContentID,
	)
	fullPrompt := bountyInfo + "\n\n" + orchestratorPrompt

	// Add the decision tool to the list of available tools.
	tools := append(input.Tools, SubmitDecisionTool)

	previousResponseID := ""
	pendingOutputs := map[string]string{}

	for i := 0; i < OrchestratorMaxTurns; i++ {
		var turnResult ResponsesTurnResult
		var actErr error
		if previousResponseID == "" {
			actErr = workflow.ExecuteActivity(ctx, a.GenerateResponsesTurn, previousResponseID, fullPrompt, tools, nil).Get(ctx, &turnResult)
		} else {
			actErr = workflow.ExecuteActivity(ctx, a.GenerateResponsesTurn, previousResponseID, "", nil, pendingOutputs).Get(ctx, &turnResult)
		}
		if actErr != nil {
			logger.Error("LLM activity failed", "error", actErr)
			return nil, actErr
		}
		previousResponseID = turnResult.ID
		pendingOutputs = map[string]string{}

		if len(turnResult.Calls) > 0 {
			for _, toolCall := range turnResult.Calls {
				var toolResult string
				switch toolCall.Name {
				case ToolNamePullContent:
					var args PullContentInput
					if err := json.Unmarshal([]byte(toolCall.Arguments), &args); err != nil {
						toolResult = fmt.Sprintf(`{"error": "failed to parse arguments: %v"}`, err)
					} else {
						var contentBytes []byte
						activityErr := workflow.ExecuteActivity(ctx, a.PullContentActivity, args).Get(ctx, &contentBytes)
						if activityErr != nil {
							toolResult = fmt.Sprintf(`{"error": "failed to execute tool: %v"}`, activityErr)
						} else {
							toolResult = string(contentBytes)
						}
					}
				case ToolNameGetClosingPR:
					var args struct {
						Owner       string `json:"owner"`
						Repo        string `json:"repo"`
						IssueNumber int    `json:"issue_number"`
					}
					if err := json.Unmarshal([]byte(toolCall.Arguments), &args); err != nil {
						toolResult = fmt.Sprintf(`{"error": "failed to parse arguments: %v"}`, err)
					} else {
						var result GitHubPullRequestContent
						activityErr := workflow.ExecuteActivity(ctx, a.GetClosingPullRequest, args.Owner, args.Repo, args.IssueNumber).Get(ctx, &result)
						if activityErr != nil {
							toolResult = fmt.Sprintf(`{"error": "failed to execute tool: %v"}`, activityErr)
						} else {
							resultBytes, _ := json.Marshal(result)
							toolResult = string(resultBytes)
						}
					}
				case ToolNameAnalyzeImageURL:
					var args struct {
						ImageURL string `json:"image_url"`
						Prompt   string `json:"prompt"`
					}
					if err := json.Unmarshal([]byte(toolCall.Arguments), &args); err != nil {
						toolResult = fmt.Sprintf(`{"error": "failed to parse arguments: %v"}`, err)
					} else {
						var result CheckContentRequirementsResult
						activityErr := workflow.ExecuteActivity(ctx, a.AnalyzeImageURL, args.ImageURL, args.Prompt).Get(ctx, &result)
						if activityErr != nil {
							toolResult = fmt.Sprintf(`{"error": "failed to execute tool: %v"}`, activityErr)
						} else {
							resultBytes, _ := json.Marshal(result)
							toolResult = string(resultBytes)
						}
					}
				case ToolNameDetectMaliciousContent:
					var args struct {
						Content string `json:"content"`
					}
					if err := json.Unmarshal([]byte(toolCall.Arguments), &args); err != nil {
						toolResult = fmt.Sprintf(`{"error": "failed to parse arguments: %v"}`, err)
					} else {
						var result DetectMaliciousContentResult
						activityErr := workflow.ExecuteActivity(ctx, a.DetectMaliciousContent, args.Content).Get(ctx, &result)
						if activityErr != nil {
							toolResult = fmt.Sprintf(`{"error": "failed to execute tool: %v"}`, activityErr)
						} else {
							resultBytes, _ := json.Marshal(result)
							toolResult = string(resultBytes)
						}
					}
				case ToolNameSubmitDecision:
					var decisionArgs struct {
						IsApproved bool   `json:"is_approved"`
						Reason     string `json:"reason"`
					}
					if err := json.Unmarshal([]byte(toolCall.Arguments), &decisionArgs); err != nil {
						logger.Error("Failed to unmarshal decision tool arguments", "error", err, "arguments", toolCall.Arguments)
						return &OrchestratorWorkflowOutput{
							IsApproved: false,
							Reason:     "Failed to parse LLM decision.",
						}, nil
					}
					return &OrchestratorWorkflowOutput{IsApproved: decisionArgs.IsApproved, Reason: decisionArgs.Reason}, nil
				default:
					toolResult = `{"error": "unknown tool requested"}`
				}
				pendingOutputs[toolCall.ID] = toolResult
			}
			continue
		}
		if strings.TrimSpace(turnResult.Assistant) == "" {
			logger.Warn("No tool calls and no assistant content; ending conversation")
			break
		}
	}
	return nil, temporal.NewApplicationError("Orchestrator reached max turns", "MaxTurnsExceeded")
}

// ContactUsNotifyWorkflowInput is the input for the ContactUsNotifyWorkflow.
type ContactUsNotifyWorkflowInput struct {
	Name    string
	Email   string
	Message string
}

// ContactUsNotifyWorkflow sends a notification email for a contact form submission.
func ContactUsNotifyWorkflow(ctx workflow.Context, input ContactUsNotifyWorkflowInput) error {
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 1 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    1 * time.Minute,
			BackoffCoefficient: 1.0,
			MaximumInterval:    5 * time.Minute,
			MaximumAttempts:    5,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)
	a := &Activities{}

	return workflow.ExecuteActivity(ctx, a.SendContactUsEmail, input).Get(ctx, nil)
}

// EmailTokenWorkflowInput is the input for the EmailTokenWorkflow.
type EmailTokenWorkflowInput struct {
	SaleID         string
	Email          string
	Token          string
	SourcePlatform PaymentPlatformKind
}

// EmailTokenWorkflow sends an email with a token to a user.
func EmailTokenWorkflow(ctx workflow.Context, input EmailTokenWorkflowInput) error {
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 1 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    1 * time.Minute,
			BackoffCoefficient: 1.0,
			MaximumInterval:    5 * time.Minute,
			MaximumAttempts:    5,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)
	a := &Activities{}

	err := workflow.ExecuteActivity(ctx, a.SendTokenEmail, input.Email, input.Token).Get(ctx, nil)
	if err != nil {
		return err
	}

	// After sending the email, mark the sale as notified.
	markInput := MarkGumroadSaleNotifiedActivityInput{
		SaleID: input.SaleID,
		APIKey: input.Token,
	}
	return workflow.ExecuteActivity(ctx, a.MarkGumroadSaleNotifiedActivity, markInput).Get(ctx, nil)
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
