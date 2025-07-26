package abb

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/brojonat/affiliate-bounty-board/solana"
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
	CancelSignalName            = "cancel"
	GetPaidBountiesQueryType    = "getPaidBounties"
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
	Platform       PlatformKind
	ContentKind    ContentKind
	Timeout        time.Duration
	FailedClaims   map[string]time.Time
	FunderWallet   string
}

// BountyAssessmentWorkflow is the main workflow for managing a bounty.
func BountyAssessmentWorkflow(ctx workflow.Context, input BountyAssessmentWorkflowInput) error {
	logger := workflow.GetLogger(ctx)
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
		Platform:       input.Platform,
		ContentKind:    input.ContentKind,
		Timeout:        input.Timeout,
		FailedClaims:   make(map[string]time.Time),
	}

	// Wait for funding
	var funderWallet string
	fundingInput := CheckBountyFundedActivityInput{
		BountyID:          bountyState.BountyID,
		ExpectedRecipient: input.EscrowWallet,
		ExpectedAmount:    input.TotalCharged,
		Timeout:           input.PaymentTimeout,
	}
	err := workflow.ExecuteActivity(ctx, a.CheckBountyFundedActivity, fundingInput).Get(ctx, &funderWallet)
	if err != nil {
		logger.Error("Funding check activity failed.", "error", err)
		return err // Returning error to let temporal handle retry/failure
	}
	if funderWallet == "" {
		logger.Error("Funding verification failed or timed out.")
		return nil // End workflow gracefully if not funded
	}
	bountyState.FunderWallet = funderWallet
	bountyState.Status = BountyStatusListening
	logger.Info("Bounty funded. Listening for submissions.", "funder_wallet", funderWallet)

	// Main loop for listening to signals, cancellation, or timing out
	assessmentChan := workflow.GetSignalChannel(ctx, AssessmentSignalName)
	cancelChan := workflow.GetSignalChannel(ctx, CancelSignalName)

	for {
		// Check if insufficient balance for next payout
		if bountyState.ValueRemaining.IsPositive() && bountyState.ValueRemaining.Cmp(bountyState.BountyPerPost) < 0 {
			logger.Info("Insufficient balance for next payout. Closing bounty.",
				"remaining", bountyState.ValueRemaining, "needed", bountyState.BountyPerPost)
			bountyState.Status = BountyStatusDrained
			break
		}

		selector := workflow.NewSelector(ctx)

		// Listen for assessment signals
		selector.AddReceive(assessmentChan, func(c workflow.ReceiveChannel, more bool) {
			var signal AssessContentSignal
			c.Receive(ctx, &signal)
			processClaim(ctx, a, bountyState, signal, logger, input)
		})

		// Listen for cancellation signals
		selector.AddReceive(cancelChan, func(c workflow.ReceiveChannel, more bool) {
			var signal struct{} // Empty signal payload
			c.Receive(ctx, &signal)
			logger.Info("Bounty cancellation requested")
			bountyState.Status = BountyStatusCancelled
		})

		// Listen for timeout
		selector.AddFuture(workflow.NewTimer(ctx, bountyState.Timeout), func(f workflow.Future) {
			logger.Info("Bounty timeout reached")
			bountyState.Status = BountyStatusClosed
		})

		selector.Select(ctx)

		if bountyState.Status != BountyStatusListening {
			break
		}
	}

	// Process final payouts
	if len(bountyState.Payouts) > 0 {
		for _, p := range bountyState.Payouts {
			workflow.ExecuteActivity(ctx, a.PayBountyActivity, bountyState.BountyID, p.PayoutWallet, p.Amount).Get(ctx, nil) // Wait for each payout to complete
		}
	}

	// Send any remaining balance back to the funder wallet
	if bountyState.ValueRemaining.IsPositive() {
		logger.Info("Refunding remaining balance", "amount", bountyState.ValueRemaining, "funder_wallet", bountyState.FunderWallet)

		err := workflow.ExecuteActivity(ctx, a.RefundBountyActivity, bountyState.BountyID, bountyState.FunderWallet, bountyState.ValueRemaining).Get(ctx, nil)
		if err != nil {
			logger.Error("Failed to refund remaining balance", "error", err)
			// Don't fail the workflow - log and continue
		} else {
			logger.Info("Successfully refunded remaining balance", "amount", bountyState.ValueRemaining)
		}
	}

	logger.Info("BountyAssessmentWorkflow finished.")
	return nil
}

func processClaim(ctx workflow.Context, a *Activities, bountyState *BountyState, signal AssessContentSignal, logger log.Logger, bountyInput BountyAssessmentWorkflowInput) {
	if lastFailureTime, ok := bountyState.FailedClaims[signal.ContentID]; ok {
		if workflow.Now(ctx).Sub(lastFailureTime) < ContentReassessmentCooldown {
			logger.Info("Content is on cooldown.", "content_id", signal.ContentID)
			return
		}
	}

	var orchestratorResult OrchestratorWorkflowOutput
	orchErr := workflow.ExecuteChildWorkflow(ctx, OrchestratorWorkflow, OrchestratorWorkflowInput{
		Tools:         []Tool{GetContentDetailsTool, AnalyzeImageURLTool, DetectMaliciousContentTool},
		Bounty:        bountyInput,
		InitialSignal: signal,
	}).Get(ctx, &orchestratorResult)

	if orchErr != nil || !orchestratorResult.IsApproved {
		logger.Error("Orchestrator rejected submission.", "error", orchErr, "reason", orchestratorResult.Reason)
		bountyState.FailedClaims[signal.ContentID] = workflow.Now(ctx)
		return
	}

	payoutAmount := bountyState.BountyPerPost

	// Check if we have sufficient balance for this payout
	if bountyState.ValueRemaining.Cmp(payoutAmount) < 0 {
		logger.Warn("Insufficient balance for payout",
			"requested", payoutAmount, "available", bountyState.ValueRemaining)
		bountyState.FailedClaims[signal.ContentID] = workflow.Now(ctx)
		return
	}

	bountyState.Payouts = append(bountyState.Payouts, PayoutDetail{
		ContentID:    signal.ContentID,
		PayoutWallet: signal.PayoutWallet,
		Amount:       payoutAmount,
		Timestamp:    workflow.Now(ctx),
		Platform:     bountyState.Platform,
		ContentKind:  bountyState.ContentKind,
	})
	bountyState.ValueRemaining = bountyState.ValueRemaining.Sub(payoutAmount)
	logger.Info("Payout recorded", "amount", payoutAmount)

	if bountyState.ValueRemaining.IsZero() {
		bountyState.Status = BountyStatusDrained
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

	messages := []Message{{Role: "user", Content: fullPrompt}}
	// Add the decision tool to the list of available tools.
	tools := append(input.Tools, SubmitDecisionTool)

	// FIXME: this should be a constant/configured from env
	for i := 0; i < 10; i++ {
		var llmResponse LLMResponse
		err := workflow.ExecuteActivity(ctx, a.GenerateResponse, messages, tools).Get(ctx, &llmResponse)
		if err != nil {
			logger.Error("LLM activity failed", "error", err)
			return nil, err
		}
		messages = append(messages, Message{Role: "assistant", Content: llmResponse.Content, ToolCalls: llmResponse.ToolCalls})

		if len(llmResponse.ToolCalls) > 0 {
			for _, toolCall := range llmResponse.ToolCalls {
				var toolResult string
				switch toolCall.Name {
				case "get_content_details":
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
				case "analyze_image_url":
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
				case "detect_malicious_content":
					var args struct {
						Content string `json:"content"`
					}
					if err := json.Unmarshal([]byte(toolCall.Arguments), &args); err != nil {
						toolResult = fmt.Sprintf(`{"error": "failed to parse arguments: %v"}`, err)
					} else {
						var result DetectMaliciousContentResult
						activityErr := workflow.ExecuteActivity(ctx, a.DetectMaliciousContent, []byte(args.Content)).Get(ctx, &result)
						if activityErr != nil {
							toolResult = fmt.Sprintf(`{"error": "failed to execute tool: %v"}`, activityErr)
						} else {
							resultBytes, _ := json.Marshal(result)
							toolResult = string(resultBytes)
						}
					}
				case "submit_decision":
					// This is the final decision from the LLM.
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

					return &OrchestratorWorkflowOutput{
						IsApproved: decisionArgs.IsApproved,
						Reason:     decisionArgs.Reason,
					}, nil
				default:
					toolResult = `{"error": "unknown tool requested"}`
				}
				messages = append(messages, Message{Role: "tool", ToolCallID: toolCall.ID, Content: toolResult})
			}
			continue
		}

		// If the LLM responds without a tool call, we treat it as a continuation of the conversation,
		// but not a final decision. In a real-world scenario, you might want to handle this differently,
		// but for now, we'll just loop again. If it happens too many times, the max turns limit will be hit.
		logger.Warn("LLM response without tool call, continuing conversation.", "response", llmResponse.Content)
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
