package abb

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/brojonat/affiliate-bounty-board/solana"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

type WorkflowTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
	a   *Activities
}

func (s *WorkflowTestSuite) SetupTest() {
	s.SetLogger(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})))
	s.a = &Activities{
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
	s.env = s.NewTestWorkflowEnvironment()
	s.env.RegisterActivity(s.a) // Register all activities from the struct
	s.env.SetTestTimeout(30 * time.Second)

	// Set required environment variables for tests
	os.Setenv("SOLANA_ESCROW_PRIVATE_KEY", "dummy_private_key_for_testing")
	os.Setenv("ENV", "test")

	// Register workflows
	s.env.RegisterWorkflow(OrchestratorWorkflow)
	s.env.RegisterWorkflow(BountyAssessmentWorkflow)
	s.env.RegisterWorkflow(ContactUsNotifyWorkflow)
	s.env.RegisterWorkflow(EmailTokenWorkflow)
}

func (s *WorkflowTestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

func TestWorkflowTestSuite(t *testing.T) {
	suite.Run(t, new(WorkflowTestSuite))
}

// ORCHESTRATOR WORKFLOW TESTS

func (s *WorkflowTestSuite) Test_OrchestratorWorkflow_NoToolCalls_Success() {
	input := OrchestratorWorkflowInput{
		Tools: []Tool{},
	}
	prompt := "What is 2+2?"
	s.env.OnActivity(s.a.GetOrchestratorPromptActivity, mock.Anything).Return(prompt, nil)
	s.env.OnActivity(s.a.GenerateResponsesTurn, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(ResponsesTurnResult{
		Assistant: "",
		Calls: []ToolCall{{
			ID:        "decision1",
			Name:      "submit_decision",
			Arguments: `{"is_approved": true, "reason": "The answer is 4."}`,
		}},
		ID: "resp_1",
	}, nil)

	s.env.ExecuteWorkflow(OrchestratorWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result *OrchestratorWorkflowOutput
	s.NoError(s.env.GetWorkflowResult(&result))
	s.True(result.IsApproved)
	s.Equal("The answer is 4.", result.Reason)
}

func (s *WorkflowTestSuite) Test_OrchestratorWorkflow_Rejection() {
	input := OrchestratorWorkflowInput{Tools: []Tool{}}
	prompt := "Is this valid?"
	s.env.OnActivity(s.a.GetOrchestratorPromptActivity, mock.Anything).Return(prompt, nil)
	s.env.OnActivity(s.a.GenerateResponsesTurn, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(ResponsesTurnResult{
		Calls: []ToolCall{{
			ID:        "decision1",
			Name:      "submit_decision",
			Arguments: `{"is_approved": false, "reason": "Content is not valid."}`,
		}},
		ID: "resp_1",
	}, nil)

	s.env.ExecuteWorkflow(OrchestratorWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result *OrchestratorWorkflowOutput
	s.NoError(s.env.GetWorkflowResult(&result))
	s.False(result.IsApproved)
	s.Equal("Content is not valid.", result.Reason)
}

func (s *WorkflowTestSuite) Test_OrchestratorWorkflow_ToolCall_Success() {
	input := OrchestratorWorkflowInput{Tools: []Tool{PullContentTool}}
	prompt := "Get content details for post 123"
	s.env.OnActivity(s.a.GetOrchestratorPromptActivity, mock.Anything).Return(prompt, nil)

	// First LLM call - requests tool
	s.env.OnActivity(s.a.GenerateResponsesTurn, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(ResponsesTurnResult{
		Calls: []ToolCall{{
			ID:        "call1",
			Name:      "pull_content",
			Arguments: `{"platform":"reddit","content_kind":"post","content_id":"123"}`,
		}},
		ID: "resp_1",
	}, nil).Once()

	s.env.OnActivity(s.a.PullContentActivity, mock.Anything, mock.Anything).Return([]byte(`{"title":"Test Post","content":"Hello world"}`), nil)

	// Second LLM call - final decision
	s.env.OnActivity(s.a.GenerateResponsesTurn, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(ResponsesTurnResult{
		Calls: []ToolCall{{
			ID:        "decision1",
			Name:      "submit_decision",
			Arguments: `{"is_approved": true, "reason": "Content retrieved successfully."}`,
		}},
		ID: "resp_2",
	}, nil).Once()

	s.env.ExecuteWorkflow(OrchestratorWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result *OrchestratorWorkflowOutput
	s.NoError(s.env.GetWorkflowResult(&result))
	s.True(result.IsApproved)
	s.Equal("Content retrieved successfully.", result.Reason)
}

func (s *WorkflowTestSuite) Test_OrchestratorWorkflow_ToolCall_Failure() {
	input := OrchestratorWorkflowInput{Tools: []Tool{PullContentTool}}
	prompt := "Get content details for invalid post"
	s.env.OnActivity(s.a.GetOrchestratorPromptActivity, mock.Anything).Return(prompt, nil)

	// First LLM call
	s.env.OnActivity(s.a.GenerateResponsesTurn, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(ResponsesTurnResult{
		Calls: []ToolCall{{
			ID:        "call1",
			Name:      "pull_content",
			Arguments: `{"platform":"reddit","content_kind":"post","content_id":"invalid"}`,
		}},
		ID: "resp_1",
	}, nil).Once()

	s.env.OnActivity(s.a.PullContentActivity, mock.Anything, mock.Anything).Return(nil, errors.New("content not found"))

	// Second LLM call with error
	s.env.OnActivity(s.a.GenerateResponsesTurn, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(ResponsesTurnResult{
		Calls: []ToolCall{{
			ID:        "decision1",
			Name:      "submit_decision",
			Arguments: `{"is_approved": false, "reason": "Content could not be retrieved."}`,
		}},
		ID: "resp_2",
	}, nil).Once()

	s.env.ExecuteWorkflow(OrchestratorWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result *OrchestratorWorkflowOutput
	s.NoError(s.env.GetWorkflowResult(&result))
	s.False(result.IsApproved)
	s.Equal("Content could not be retrieved.", result.Reason)
}

func (s *WorkflowTestSuite) Test_OrchestratorWorkflow_UnknownTool() {
	input := OrchestratorWorkflowInput{Tools: []Tool{PullContentTool}}
	prompt := "Do something"
	s.env.OnActivity(s.a.GetOrchestratorPromptActivity, mock.Anything).Return(prompt, nil)

	// LLM requests unknown tool
	s.env.OnActivity(s.a.GenerateResponsesTurn, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(ResponsesTurnResult{
		Calls: []ToolCall{{
			ID:        "call1",
			Name:      "unknown_tool",
			Arguments: `{}`,
		}},
		ID: "resp_1",
	}, nil).Once()

	// Second call with error response
	s.env.OnActivity(s.a.GenerateResponsesTurn, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(ResponsesTurnResult{
		Calls: []ToolCall{{
			ID:        "decision1",
			Name:      "submit_decision",
			Arguments: `{"is_approved": false, "reason": "I cannot use that tool."}`,
		}},
		ID: "resp_2",
	}, nil).Once()

	s.env.ExecuteWorkflow(OrchestratorWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result *OrchestratorWorkflowOutput
	s.NoError(s.env.GetWorkflowResult(&result))
	s.False(result.IsApproved)
	s.Equal("I cannot use that tool.", result.Reason)
}

func (s *WorkflowTestSuite) Test_OrchestratorWorkflow_AnalyzeImageURL_Success() {
	// Mock activities
	s.env.OnActivity(s.a.GetOrchestratorPromptActivity, mock.Anything).Return("test orchestrator prompt", nil).Once()
	s.env.OnActivity(s.a.GenerateResponsesTurn, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(func(ctx context.Context, previousResponseID string, userInput string, tools []Tool, functionOutputs map[string]string) (ResponsesTurnResult, error) {
		// First turn: decide to call analyze_image_url
		if previousResponseID == "" {
			return ResponsesTurnResult{
				Calls: []ToolCall{{
					ID:        "call1",
					Name:      ToolNameAnalyzeImageURL,
					Arguments: `{"image_url": "http://example.com/cat.jpg", "prompt": "contains a cat"}`,
				}},
				ID: "resp_1",
			}, nil
		}
		// Second turn: submit final decision
		return ResponsesTurnResult{
			Calls: []ToolCall{{
				ID:        "decision1",
				Name:      ToolNameSubmitDecision,
				Arguments: `{"is_approved": true, "reason": "Image contains a cat as required."}`,
			}},
			ID: "resp_2",
		}, nil
	}).Times(2)
	s.env.OnActivity(s.a.AnalyzeImageURL, mock.Anything, "http://example.com/cat.jpg", "contains a cat").Return(CheckContentRequirementsResult{Satisfies: true, Reason: "Image contains a cat."}, nil).Once()

	// Execute workflow
	s.env.ExecuteWorkflow(OrchestratorWorkflow, OrchestratorWorkflowInput{
		Tools: []Tool{AnalyzeImageURLTool},
		Bounty: BountyAssessmentWorkflowInput{
			Title:        "Cat Bounty",
			Requirements: []string{"Image must contain a cat."},
		},
		InitialSignal: AssessContentSignal{
			ContentID: "image.jpg",
		},
	})

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result OrchestratorWorkflowOutput
	err := s.env.GetWorkflowResult(&result)
	s.NoError(err)
	s.True(result.IsApproved)
	s.Equal("Image contains a cat as required.", result.Reason)
}

func (s *WorkflowTestSuite) Test_OrchestratorWorkflow_MaxTurnsExceeded() {
	input := OrchestratorWorkflowInput{Tools: []Tool{PullContentTool}}
	prompt := "Keep calling tools forever"
	s.env.OnActivity(s.a.GetOrchestratorPromptActivity, mock.Anything).Return(prompt, nil)

	// Mock infinite tool calling
	s.env.OnActivity(s.a.GenerateResponsesTurn, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(ResponsesTurnResult{
		Calls: []ToolCall{{ID: "call1", Name: "pull_content", Arguments: `{"platform":"reddit","content_kind":"post","content_id":"123"}`}},
		ID:    "resp_1",
	}, nil)
	s.env.OnActivity(s.a.PullContentActivity, mock.Anything, mock.Anything).Return([]byte(`{"data":"test"}`), nil)

	s.env.ExecuteWorkflow(OrchestratorWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	err := s.env.GetWorkflowError()
	s.Error(err)
	var applicationError *temporal.ApplicationError
	s.True(errors.As(err, &applicationError))
	s.Equal("MaxTurnsExceeded", applicationError.Type())
}

// BOUNTY ASSESSMENT WORKFLOW TESTS

func (s *WorkflowTestSuite) Test_BountyAssessmentWorkflow_FundingFailed() {
	bountyPerPost, _ := solana.NewUSDCAmount(10)
	totalBounty, _ := solana.NewUSDCAmount(100)
	totalCharged, _ := solana.NewUSDCAmount(105)
	input := BountyAssessmentWorkflowInput{
		BountyPerPost: bountyPerPost, TotalBounty: totalBounty, TotalCharged: totalCharged,
		Platform: PlatformReddit, ContentKind: ContentKindPost,
		Timeout: 1 * time.Hour, PaymentTimeout: 5 * time.Minute,
		EscrowWallet: "11111111111111111111111111111112",
	}

	s.env.OnActivity(s.a.VerifyPayment, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&VerifyPaymentResult{Verified: false, Error: "funding failed"}, nil)
	s.env.ExecuteWorkflow(BountyAssessmentWorkflow, input)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *WorkflowTestSuite) Test_BountyAssessmentWorkflow_SuccessfulClaim() {
	bountyPerPost, _ := solana.NewUSDCAmount(10)
	totalBounty, _ := solana.NewUSDCAmount(10)
	totalCharged, _ := solana.NewUSDCAmount(10)
	input := BountyAssessmentWorkflowInput{
		Requirements:  []string{"Must be helpful"},
		BountyPerPost: bountyPerPost, TotalBounty: totalBounty, TotalCharged: totalCharged,
		Platform: PlatformReddit, ContentKind: ContentKindPost,
		Timeout: 1 * time.Hour, PaymentTimeout: 5 * time.Minute,
		EscrowWallet: "11111111111111111111111111111112",
	}

	s.env.OnActivity(s.a.VerifyPayment, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&VerifyPaymentResult{Verified: true, FunderWallet: "funder_wallet_123"}, nil)
	s.env.OnActivity(s.a.GenerateAndStoreBountyEmbeddingActivity, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.a.GetOrchestratorPromptActivity, mock.Anything).Return("prompt", nil)
	s.env.OnActivity(s.a.GenerateResponsesTurn, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(ResponsesTurnResult{
		Calls: []ToolCall{{
			ID: "decision", Name: "submit_decision", Arguments: `{"is_approved":true,"reason":"meets requirements"}`,
		}},
		ID: "resp_1",
	}, nil)
	s.env.OnActivity(s.a.PayBountyActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(AssessmentSignalName, AssessContentSignal{
			ContentID: "post123", PayoutWallet: "wallet123", Platform: PlatformReddit, ContentKind: ContentKindPost,
		})
	}, 0)

	s.env.ExecuteWorkflow(BountyAssessmentWorkflow, input)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *WorkflowTestSuite) Test_BountyAssessmentWorkflow_RejectedClaim() {
	bountyPerPost, _ := solana.NewUSDCAmount(10)
	totalBounty, _ := solana.NewUSDCAmount(100)
	totalCharged, _ := solana.NewUSDCAmount(105)
	input := BountyAssessmentWorkflowInput{
		BountyPerPost: bountyPerPost, TotalBounty: totalBounty, TotalCharged: totalCharged,
		Platform: PlatformReddit, ContentKind: ContentKindPost,
		Timeout: 1 * time.Second, PaymentTimeout: 5 * time.Minute,
		EscrowWallet: "11111111111111111111111111111112",
	}

	s.env.OnActivity(s.a.VerifyPayment, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&VerifyPaymentResult{Verified: true, FunderWallet: "funder_wallet_123"}, nil)
	s.env.OnActivity(s.a.GenerateAndStoreBountyEmbeddingActivity, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.a.GetOrchestratorPromptActivity, mock.Anything).Return("prompt", nil)
	s.env.OnActivity(s.a.GenerateResponsesTurn, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(ResponsesTurnResult{
		Calls: []ToolCall{{
			ID: "decision", Name: "submit_decision", Arguments: `{"is_approved":false,"reason":"does not meet requirements"}`,
		}},
		ID: "resp_1",
	}, nil)
	s.env.OnActivity(s.a.RefundBountyActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(AssessmentSignalName, AssessContentSignal{
			ContentID: "post123", PayoutWallet: "wallet123", Platform: PlatformReddit, ContentKind: ContentKindPost,
		})
	}, 0)

	s.env.ExecuteWorkflow(BountyAssessmentWorkflow, input)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *WorkflowTestSuite) Test_BountyAssessmentWorkflow_Timeout() {
	bountyPerPost, _ := solana.NewUSDCAmount(10)
	totalBounty, _ := solana.NewUSDCAmount(100)
	totalCharged, _ := solana.NewUSDCAmount(105)
	funderWallet := "funder_wallet_for_refund"
	input := BountyAssessmentWorkflowInput{
		BountyPerPost: bountyPerPost, TotalBounty: totalBounty, TotalCharged: totalCharged,
		Timeout: 1 * time.Millisecond, PaymentTimeout: 1 * time.Minute,
		EscrowWallet: "11111111111111111111111111111112",
	}

	s.env.OnActivity(s.a.VerifyPayment, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&VerifyPaymentResult{Verified: true, FunderWallet: funderWallet}, nil)
	s.env.OnActivity(s.a.GenerateAndStoreBountyEmbeddingActivity, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.a.RefundBountyActivity, mock.Anything, mock.Anything, funderWallet, totalBounty).Return(nil)

	s.env.ExecuteWorkflow(BountyAssessmentWorkflow, input)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *WorkflowTestSuite) Test_BountyAssessmentWorkflow_ClaimCooldown() {
	bountyPerPost, _ := solana.NewUSDCAmount(10)
	totalBounty, _ := solana.NewUSDCAmount(100)
	totalCharged, _ := solana.NewUSDCAmount(105)
	input := BountyAssessmentWorkflowInput{
		BountyPerPost: bountyPerPost, TotalBounty: totalBounty, TotalCharged: totalCharged,
		Platform: PlatformReddit, ContentKind: ContentKindPost,
		Timeout: 1 * time.Second, PaymentTimeout: 5 * time.Minute,
		EscrowWallet: "11111111111111111111111111111112",
	}

	s.env.OnActivity(s.a.VerifyPayment, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&VerifyPaymentResult{Verified: true, FunderWallet: "funder_wallet_123"}, nil)
	s.env.OnActivity(s.a.GenerateAndStoreBountyEmbeddingActivity, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.a.GetOrchestratorPromptActivity, mock.Anything).Return("prompt", nil)
	s.env.OnActivity(s.a.GenerateResponsesTurn, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(ResponsesTurnResult{
		Calls: []ToolCall{{
			ID: "decision", Name: "submit_decision", Arguments: `{"is_approved":false,"reason":"Content rejected"}`,
		}},
		ID: "resp_1",
	}, nil).Once()
	s.env.OnActivity(s.a.RefundBountyActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	signal := AssessContentSignal{
		ContentID: "same_content", PayoutWallet: "wallet123", Platform: PlatformReddit, ContentKind: ContentKindPost,
	}
	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(AssessmentSignalName, signal)
		s.env.SignalWorkflow(AssessmentSignalName, signal) // Should be ignored
	}, 0)

	s.env.ExecuteWorkflow(BountyAssessmentWorkflow, input)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *WorkflowTestSuite) Test_BountyAssessmentWorkflow_SuccessfulRefund() {
	bountyPerPost, _ := solana.NewUSDCAmount(10)
	totalBounty, _ := solana.NewUSDCAmount(100)
	totalCharged, _ := solana.NewUSDCAmount(105)
	funderWallet := "original_funder_wallet"
	input := BountyAssessmentWorkflowInput{
		BountyPerPost: bountyPerPost, TotalBounty: totalBounty, TotalCharged: totalCharged,
		Timeout: 100 * time.Millisecond, PaymentTimeout: 1 * time.Minute,
		EscrowWallet: "11111111111111111111111111111112",
	}

	s.env.OnActivity(s.a.VerifyPayment, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&VerifyPaymentResult{Verified: true, FunderWallet: funderWallet}, nil)
	s.env.OnActivity(s.a.GenerateAndStoreBountyEmbeddingActivity, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.a.RefundBountyActivity, mock.Anything, mock.Anything, funderWallet, totalBounty).Return(nil)

	s.env.ExecuteWorkflow(BountyAssessmentWorkflow, input)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *WorkflowTestSuite) Test_BountyAssessmentWorkflow_QueryBountyDetails() {
	bountyPerPost, _ := solana.NewUSDCAmount(10)
	totalBounty, _ := solana.NewUSDCAmount(100)
	totalCharged, _ := solana.NewUSDCAmount(105)
	timeoutDuration := 100 * time.Millisecond
	input := BountyAssessmentWorkflowInput{
		Title: "Test Bounty", Requirements: []string{"req1", "req2"},
		BountyPerPost: bountyPerPost, TotalBounty: totalBounty, TotalCharged: totalCharged,
		Platform: PlatformReddit, ContentKind: ContentKindComment, Tier: 1, // BountyTierCreator
		Timeout: timeoutDuration, PaymentTimeout: 10 * time.Minute,
		EscrowWallet: "11111111111111111111111111111112", TreasuryWallet: "11111111111111111111111111111113",
	}

	s.env.OnActivity(s.a.VerifyPayment, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&VerifyPaymentResult{Verified: true, FunderWallet: "funder-wallet-123"}, nil).Once()
	s.env.OnActivity(s.a.GenerateAndStoreBountyEmbeddingActivity, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.a.RefundBountyActivity, mock.Anything, mock.Anything, "funder-wallet-123", totalBounty).Return(nil).Once()

	s.env.RegisterDelayedCallback(func() {
		resp, err := s.env.QueryWorkflow(GetBountyDetailsQueryType)
		s.NoError(err)
		var details BountyDetails
		s.NoError(resp.Get(&details))
		s.Equal("Test Bounty", details.Title)
		s.Equal(BountyStatusListening, details.Status)
		s.Equal(BountyTier(1), details.Tier)
	}, 1*time.Millisecond)

	s.env.ExecuteWorkflow(BountyAssessmentWorkflow, input)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *WorkflowTestSuite) Test_BountyAssessmentWorkflow_QueryPaidBounties() {
	bountyPerPost, _ := solana.NewUSDCAmount(10)
	totalBounty, _ := solana.NewUSDCAmount(20)
	totalCharged, _ := solana.NewUSDCAmount(21)
	input := BountyAssessmentWorkflowInput{
		Requirements:  []string{"Must be good"},
		BountyPerPost: bountyPerPost, TotalBounty: totalBounty, TotalCharged: totalCharged,
		Platform: PlatformReddit, ContentKind: ContentKindPost,
		Timeout: 1 * time.Hour, PaymentTimeout: 5 * time.Minute,
		EscrowWallet:   "11111111111111111111111111111112",
		TreasuryWallet: "11111111111111111111111111111113",
	}

	s.env.OnActivity(s.a.VerifyPayment, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&VerifyPaymentResult{Verified: true, FunderWallet: "funder_wallet_123"}, nil)
	s.env.OnActivity(s.a.GenerateAndStoreBountyEmbeddingActivity, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.a.GetOrchestratorPromptActivity, mock.Anything).Return("prompt", nil)
	s.env.OnActivity(s.a.GenerateResponsesTurn, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(ResponsesTurnResult{
		Calls: []ToolCall{{
			ID: "decision", Name: "submit_decision", Arguments: `{"is_approved":true,"reason":"meets requirements"}`,
		}},
		ID: "resp_1",
	}, nil)
	s.env.OnActivity(s.a.PayBountyActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.a.TransferUSDC, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	signal1 := AssessContentSignal{
		ContentID: "post123", PayoutWallet: "wallet123", Platform: PlatformReddit, ContentKind: ContentKindPost,
	}
	s.env.RegisterDelayedCallback(func() { s.env.SignalWorkflow(AssessmentSignalName, signal1) }, 0)

	s.env.RegisterDelayedCallback(func() {
		resp, err := s.env.QueryWorkflow(GetPaidBountiesQueryType)
		s.NoError(err)
		var paidBounties []PayoutDetail
		s.NoError(resp.Get(&paidBounties))
		s.Len(paidBounties, 1)

		// Send another signal to drain the bounty
		signal2 := AssessContentSignal{
			ContentID: "post456", PayoutWallet: "wallet456", Platform: PlatformReddit, ContentKind: ContentKindPost,
		}
		s.env.SignalWorkflow(AssessmentSignalName, signal2)
	}, 20*time.Millisecond)

	s.env.ExecuteWorkflow(BountyAssessmentWorkflow, input)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

// SIMPLE WORKFLOW TESTS

func (s *WorkflowTestSuite) Test_ContactUsNotifyWorkflow_Success() {
	input := ContactUsNotifyWorkflowInput{
		Name: "John Doe", Email: "john@example.com", Message: "Hello there",
	}
	s.env.OnActivity(s.a.SendContactUsEmail, mock.Anything, input).Return(nil)
	s.env.ExecuteWorkflow(ContactUsNotifyWorkflow, input)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *WorkflowTestSuite) Test_ContactUsNotifyWorkflow_EmailFailure() {
	input := ContactUsNotifyWorkflowInput{
		Name: "John Doe", Email: "john@example.com", Message: "Hello there",
	}
	s.env.OnActivity(s.a.SendContactUsEmail, mock.Anything, input).Return(errors.New("email service unavailable"))
	s.env.ExecuteWorkflow(ContactUsNotifyWorkflow, input)
	s.True(s.env.IsWorkflowCompleted())
	err := s.env.GetWorkflowError()
	s.Error(err)
	s.Contains(err.Error(), "email service unavailable")
}

func (s *WorkflowTestSuite) Test_EmailTokenWorkflow_Success() {
	input := EmailTokenWorkflowInput{SaleID: "sale123", Email: "buyer@example.com", Token: "token456"}
	s.env.OnActivity(s.a.SendTokenEmail, mock.Anything, input.Email, input.Token).Return(nil)
	s.env.OnActivity(s.a.MarkGumroadSaleNotifiedActivity, mock.Anything, mock.Anything).Return(nil)
	s.env.ExecuteWorkflow(EmailTokenWorkflow, input)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *WorkflowTestSuite) Test_EmailTokenWorkflow_EmailFailure() {
	input := EmailTokenWorkflowInput{SaleID: "sale123", Email: "buyer@example.com", Token: "token456"}
	s.env.OnActivity(s.a.SendTokenEmail, mock.Anything, input.Email, input.Token).Return(errors.New("email failed"))
	s.env.ExecuteWorkflow(EmailTokenWorkflow, input)
	s.True(s.env.IsWorkflowCompleted())
	err := s.env.GetWorkflowError()
	s.Error(err)
	s.Contains(err.Error(), "email failed")
}

func (s *WorkflowTestSuite) Test_EmailTokenWorkflow_MarkNotifiedFailure() {
	input := EmailTokenWorkflowInput{SaleID: "sale123", Email: "buyer@example.com", Token: "token456"}
	s.env.OnActivity(s.a.SendTokenEmail, mock.Anything, input.Email, input.Token).Return(nil)
	s.env.OnActivity(s.a.MarkGumroadSaleNotifiedActivity, mock.Anything, mock.Anything).Return(errors.New("database error"))
	s.env.ExecuteWorkflow(EmailTokenWorkflow, input)
	s.True(s.env.IsWorkflowCompleted())
	err := s.env.GetWorkflowError()
	s.Error(err)
	s.Contains(err.Error(), "database error")
}
