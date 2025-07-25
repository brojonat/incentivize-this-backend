package abb

import (
	"encoding/json"
	"fmt"
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
	var funded bool
	err := workflow.ExecuteActivity(ctx, a.CheckBountyFundedActivity, bountyState.BountyID).Get(ctx, &funded)
	if err != nil || !funded {
		logger.Error("Funding failed or timed out.", "error", err)
		return nil
	}
	bountyState.Status = BountyStatusListening
	logger.Info("Bounty funded. Listening for submissions.")

	// Main loop for listening to signals or timing out
	assessmentChan := workflow.GetSignalChannel(ctx, AssessmentSignalName)
	for {
		selector := workflow.NewSelector(ctx)
		selector.AddReceive(assessmentChan, func(c workflow.ReceiveChannel, more bool) {
			var signal AssessContentSignal
			c.Receive(ctx, &signal)
			processClaim(ctx, a, bountyState, signal, logger)
		})
		selector.AddFuture(workflow.NewTimer(ctx, bountyState.Timeout), func(f workflow.Future) {
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
			workflow.ExecuteActivity(ctx, a.PayBountyActivity, PayBountyInput{
				Recipient: p.PayoutWallet,
				Amount:    p.Amount,
			}).Get(ctx, nil) // Wait for each payout to complete
		}
	}

	logger.Info("BountyAssessmentWorkflow finished.")
	return nil
}

func processClaim(ctx workflow.Context, a *Activities, bountyState *BountyState, signal AssessContentSignal, logger log.Logger) {
	if lastFailureTime, ok := bountyState.FailedClaims[signal.ContentID]; ok {
		if workflow.Now(ctx).Sub(lastFailureTime) < ContentReassessmentCooldown {
			logger.Info("Content is on cooldown.", "content_id", signal.ContentID)
			return
		}
	}

	var orchestratorResult OrchestratorWorkflowOutput
	orchErr := workflow.ExecuteChildWorkflow(ctx, OrchestratorWorkflow, OrchestratorWorkflowInput{
		Goal:  "Determine if the submitted content is valid for the bounty payout.",
		Tools: []Tool{GetContentDetailsTool},
	}).Get(ctx, &orchestratorResult)

	if orchErr != nil || !orchestratorResult.IsApproved {
		logger.Error("Orchestrator rejected submission.", "error", orchErr, "reason", orchestratorResult.Reason)
		bountyState.FailedClaims[signal.ContentID] = workflow.Now(ctx)
		return
	}

	payoutAmount := orchestratorResult.Payout
	if payoutAmount == nil {
		payoutAmount = bountyState.BountyPerPost
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

	if bountyState.ValueRemaining.IsZero() || bountyState.ValueRemaining.IsNegative() {
		bountyState.Status = BountyStatusDrained
	}
}

type OrchestratorWorkflowInput struct {
	Goal   string
	Tools  []Tool
	Bounty BountyAssessmentWorkflowInput
}

type OrchestratorWorkflowOutput struct {
	IsApproved bool
	Reason     string
	Payout     *solana.USDCAmount
}

func OrchestratorWorkflow(ctx workflow.Context, input OrchestratorWorkflowInput) (*OrchestratorWorkflowOutput, error) {
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)
	logger := workflow.GetLogger(ctx)
	a := &Activities{}

	messages := []Message{{Role: "user", Content: input.Goal}}
	for i := 0; i < 10; i++ {
		var llmResponse LLMResponse
		err := workflow.ExecuteActivity(ctx, a.GenerateResponse, messages, input.Tools).Get(ctx, &llmResponse)
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
				default:
					toolResult = `{"error": "unknown tool requested"}`
				}
				messages = append(messages, Message{Role: "tool", ToolCallID: toolCall.ID, Content: toolResult})
			}
			continue
		}
		return &OrchestratorWorkflowOutput{IsApproved: true, Reason: llmResponse.Content}, nil
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
