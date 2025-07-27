package abb

import (
	"errors"
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
}

func (s *WorkflowTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.env.SetTestTimeout(30 * time.Second)

	// Register workflows
	s.env.RegisterWorkflow(BountyAssessmentWorkflow)
	s.env.RegisterWorkflow(ContactUsNotifyWorkflow)
	s.env.RegisterWorkflow(EmailTokenWorkflow)
	s.env.RegisterWorkflow(OrchestratorWorkflow)

	// Register activities
	a := &Activities{}
	s.env.RegisterActivity(a.GenerateResponse)
	s.env.RegisterActivity(a.PullContentActivity)
	s.env.RegisterActivity(a.CheckBountyFundedActivity)
	s.env.RegisterActivity(a.PayBountyActivity)
	s.env.RegisterActivity(a.RefundBountyActivity)
	s.env.RegisterActivity(a.SendContactUsEmail)
	s.env.RegisterActivity(a.SendTokenEmail)
	s.env.RegisterActivity(a.MarkGumroadSaleNotifiedActivity)
	s.env.RegisterActivity(a.GetOrchestratorPromptActivity)
	s.env.RegisterActivity(a.AnalyzeImageURL)
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
	s.env.OnActivity("GetOrchestratorPromptActivity", mock.Anything).Return(prompt, nil)
	s.env.OnActivity("GenerateResponse", mock.Anything, mock.Anything, mock.Anything).Return(&LLMResponse{
		ToolCalls: []ToolCall{{
			ID:        "decision1",
			Name:      "submit_decision",
			Arguments: `{"is_approved": true, "reason": "The answer is 4."}`,
		}},
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
	s.env.OnActivity("GetOrchestratorPromptActivity", mock.Anything).Return(prompt, nil)
	s.env.OnActivity("GenerateResponse", mock.Anything, mock.Anything, mock.Anything).Return(&LLMResponse{
		ToolCalls: []ToolCall{{
			ID:        "decision1",
			Name:      "submit_decision",
			Arguments: `{"is_approved": false, "reason": "Content is not valid."}`,
		}},
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
	input := OrchestratorWorkflowInput{Tools: []Tool{GetContentDetailsTool}}
	prompt := "Get content details for post 123"
	s.env.OnActivity("GetOrchestratorPromptActivity", mock.Anything).Return(prompt, nil)

	// First LLM call - requests tool
	s.env.OnActivity("GenerateResponse", mock.Anything, mock.Anything, mock.Anything).Return(&LLMResponse{
		ToolCalls: []ToolCall{{
			ID:        "call1",
			Name:      "get_content_details",
			Arguments: `{"platform":"reddit","content_kind":"post","content_id":"123"}`,
		}},
	}, nil).Once()

	s.env.OnActivity("PullContentActivity", mock.Anything, mock.Anything).Return([]byte(`{"title":"Test Post","content":"Hello world"}`), nil)

	// Second LLM call - final decision
	s.env.OnActivity("GenerateResponse", mock.Anything, mock.Anything, mock.Anything).Return(&LLMResponse{
		ToolCalls: []ToolCall{{
			ID:        "decision1",
			Name:      "submit_decision",
			Arguments: `{"is_approved": true, "reason": "Content retrieved successfully."}`,
		}},
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
	input := OrchestratorWorkflowInput{Tools: []Tool{GetContentDetailsTool}}
	prompt := "Get content details for invalid post"
	s.env.OnActivity("GetOrchestratorPromptActivity", mock.Anything).Return(prompt, nil)

	// First LLM call
	s.env.OnActivity("GenerateResponse", mock.Anything, mock.Anything, mock.Anything).Return(&LLMResponse{
		ToolCalls: []ToolCall{{
			ID:        "call1",
			Name:      "get_content_details",
			Arguments: `{"platform":"reddit","content_kind":"post","content_id":"invalid"}`,
		}},
	}, nil).Once()

	s.env.OnActivity("PullContentActivity", mock.Anything, mock.Anything).Return(nil, errors.New("content not found"))

	// Second LLM call with error
	s.env.OnActivity("GenerateResponse", mock.Anything, mock.Anything, mock.Anything).Return(&LLMResponse{
		ToolCalls: []ToolCall{{
			ID:        "decision1",
			Name:      "submit_decision",
			Arguments: `{"is_approved": false, "reason": "Content could not be retrieved."}`,
		}},
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
	input := OrchestratorWorkflowInput{Tools: []Tool{GetContentDetailsTool}}
	prompt := "Do something"
	s.env.OnActivity("GetOrchestratorPromptActivity", mock.Anything).Return(prompt, nil)

	// LLM requests unknown tool
	s.env.OnActivity("GenerateResponse", mock.Anything, mock.Anything, mock.Anything).Return(&LLMResponse{
		ToolCalls: []ToolCall{{
			ID:        "call1",
			Name:      "unknown_tool",
			Arguments: `{}`,
		}},
	}, nil).Once()

	// Second call with error response
	s.env.OnActivity("GenerateResponse", mock.Anything, mock.Anything, mock.Anything).Return(&LLMResponse{
		ToolCalls: []ToolCall{{
			ID:        "decision1",
			Name:      "submit_decision",
			Arguments: `{"is_approved": false, "reason": "I cannot use that tool."}`,
		}},
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
	input := OrchestratorWorkflowInput{Tools: []Tool{GetContentDetailsTool, AnalyzeImageURLTool}}
	prompt := "Analyze image"
	imageURL := "http://example.com/cat.jpg"
	analysisPrompt := "contains a cat"
	s.env.OnActivity("GetOrchestratorPromptActivity", mock.Anything).Return(prompt, nil)

	// Mock sequence of LLM and activity calls
	s.env.OnActivity("GenerateResponse", mock.Anything, mock.Anything, mock.Anything).Return(&LLMResponse{
		ToolCalls: []ToolCall{{ID: "call1", Name: "get_content_details", Arguments: `{"platform":"reddit","content_kind":"post","content_id":"123"}`}},
	}, nil).Once()
	s.env.OnActivity("PullContentActivity", mock.Anything, mock.Anything).Return([]byte(`{"thumbnail_url":"`+imageURL+`"}`), nil)
	s.env.OnActivity("GenerateResponse", mock.Anything, mock.Anything, mock.Anything).Return(&LLMResponse{
		ToolCalls: []ToolCall{{ID: "call2", Name: "analyze_image_url", Arguments: `{"image_url":"` + imageURL + `", "prompt":"` + analysisPrompt + `"}`}},
	}, nil).Once()
	s.env.OnActivity("AnalyzeImageURL", mock.Anything, imageURL, analysisPrompt).Return(CheckContentRequirementsResult{Satisfies: true, Reason: "Has a cat."}, nil)
	s.env.OnActivity("GenerateResponse", mock.Anything, mock.Anything, mock.Anything).Return(&LLMResponse{
		ToolCalls: []ToolCall{{ID: "decision1", Name: "submit_decision", Arguments: `{"is_approved":true,"reason":"It has a cat."}`}},
	}, nil).Once()

	s.env.ExecuteWorkflow(OrchestratorWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
	var result *OrchestratorWorkflowOutput
	s.NoError(s.env.GetWorkflowResult(&result))
	s.True(result.IsApproved)
}

func (s *WorkflowTestSuite) Test_OrchestratorWorkflow_MaxTurnsExceeded() {
	input := OrchestratorWorkflowInput{Tools: []Tool{GetContentDetailsTool}}
	prompt := "Keep calling tools forever"
	s.env.OnActivity("GetOrchestratorPromptActivity", mock.Anything).Return(prompt, nil)

	// Mock infinite tool calling
	s.env.OnActivity("GenerateResponse", mock.Anything, mock.Anything, mock.Anything).Return(&LLMResponse{
		ToolCalls: []ToolCall{{ID: "call1", Name: "get_content_details", Arguments: `{"platform":"reddit","content_kind":"post","content_id":"123"}`}},
	}, nil)
	s.env.OnActivity("PullContentActivity", mock.Anything, mock.Anything).Return([]byte(`{"data":"test"}`), nil)

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
		EscrowWallet: "escrow123",
	}

	s.env.OnActivity("CheckBountyFundedActivity", mock.Anything, mock.Anything).Return("", nil)
	s.env.ExecuteWorkflow(BountyAssessmentWorkflow, input)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *WorkflowTestSuite) Test_BountyAssessmentWorkflow_SuccessfulClaim() {
	bountyPerPost, _ := solana.NewUSDCAmount(10)
	totalBounty, _ := solana.NewUSDCAmount(10)
	totalCharged, _ := solana.NewUSDCAmount(10)
	input := BountyAssessmentWorkflowInput{
		Requirements: []string{"Must be helpful"},
		BountyPerPost: bountyPerPost, TotalBounty: totalBounty, TotalCharged: totalCharged,
		Platform: PlatformReddit, ContentKind: ContentKindPost,
		Timeout: 1 * time.Hour, PaymentTimeout: 5 * time.Minute,
		EscrowWallet: "escrow123",
	}

	s.env.OnActivity("CheckBountyFundedActivity", mock.Anything, mock.Anything).Return("funder_wallet_123", nil)
	s.env.OnActivity("GetOrchestratorPromptActivity", mock.Anything).Return("prompt", nil)
	s.env.OnActivity("GenerateResponse", mock.Anything, mock.Anything, mock.Anything).Return(&LLMResponse{
		ToolCalls: []ToolCall{{
			ID: "decision", Name: "submit_decision", Arguments: `{"is_approved":true,"reason":"meets requirements"}`,
		}},
	}, nil)
	s.env.OnActivity("PayBountyActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

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
		EscrowWallet: "escrow123",
	}

	s.env.OnActivity("CheckBountyFundedActivity", mock.Anything, mock.Anything).Return("funder_wallet_123", nil)
	s.env.OnActivity("GetOrchestratorPromptActivity", mock.Anything).Return("prompt", nil)
	s.env.OnActivity("GenerateResponse", mock.Anything, mock.Anything, mock.Anything).Return(&LLMResponse{
		ToolCalls: []ToolCall{{
			ID: "decision", Name: "submit_decision", Arguments: `{"is_approved":false,"reason":"does not meet requirements"}`,
		}},
	}, nil)
	s.env.OnActivity("RefundBountyActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

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
		EscrowWallet: "escrow123",
	}

	s.env.OnActivity("CheckBountyFundedActivity", mock.Anything, mock.Anything).Return(funderWallet, nil)
	s.env.OnActivity("RefundBountyActivity", mock.Anything, mock.Anything, funderWallet, totalBounty).Return(nil)

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
		EscrowWallet: "escrow123",
	}

	s.env.OnActivity("CheckBountyFundedActivity", mock.Anything, mock.Anything).Return("funder_wallet_123", nil)
	s.env.OnActivity("GetOrchestratorPromptActivity", mock.Anything).Return("prompt", nil)
	s.env.OnActivity("GenerateResponse", mock.Anything, mock.Anything, mock.Anything).Return(&LLMResponse{
		ToolCalls: []ToolCall{{
			ID: "decision", Name: "submit_decision", Arguments: `{"is_approved":false,"reason":"Content rejected"}`,
		}},
	}, nil).Once()
	s.env.OnActivity("RefundBountyActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

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
		EscrowWallet: "escrow123",
	}

	s.env.OnActivity("CheckBountyFundedActivity", mock.Anything, mock.Anything).Return(funderWallet, nil)
	s.env.OnActivity("RefundBountyActivity", mock.Anything, mock.Anything, funderWallet, totalBounty).Return(nil)

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
		EscrowWallet: "escrow-wallet", TreasuryWallet: "treasury-wallet",
	}

	s.env.OnActivity("CheckBountyFundedActivity", mock.Anything, mock.Anything).Return("funder-wallet-123", nil).Once()
	s.env.OnActivity("RefundBountyActivity", mock.Anything, mock.Anything, "funder-wallet-123", totalBounty).Return(nil).Once()

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
		Requirements: []string{"Must be good"},
		BountyPerPost: bountyPerPost, TotalBounty: totalBounty, TotalCharged: totalCharged,
		Platform: PlatformReddit, ContentKind: ContentKindPost,
		Timeout: 1 * time.Hour, PaymentTimeout: 5 * time.Minute,
		EscrowWallet: "escrow123",
	}

	s.env.OnActivity("CheckBountyFundedActivity", mock.Anything, mock.Anything).Return("funder_wallet_123", nil)
	s.env.OnActivity("GetOrchestratorPromptActivity", mock.Anything).Return("prompt", nil)
	s.env.OnActivity("GenerateResponse", mock.Anything, mock.Anything, mock.Anything).Return(&LLMResponse{
		ToolCalls: []ToolCall{{
			ID: "decision", Name: "submit_decision", Arguments: `{"is_approved":true,"reason":"meets requirements"}`,
		}},
	}, nil)
	s.env.OnActivity("PayBountyActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

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
	s.env.OnActivity("SendContactUsEmail", mock.Anything, input).Return(nil)
	s.env.ExecuteWorkflow(ContactUsNotifyWorkflow, input)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *WorkflowTestSuite) Test_ContactUsNotifyWorkflow_EmailFailure() {
	input := ContactUsNotifyWorkflowInput{
		Name: "John Doe", Email: "john@example.com", Message: "Hello there",
	}
	s.env.OnActivity("SendContactUsEmail", mock.Anything, input).Return(errors.New("email service unavailable"))
	s.env.ExecuteWorkflow(ContactUsNotifyWorkflow, input)
	s.True(s.env.IsWorkflowCompleted())
	err := s.env.GetWorkflowError()
	s.Error(err)
	s.Contains(err.Error(), "email service unavailable")
}

func (s *WorkflowTestSuite) Test_EmailTokenWorkflow_Success() {
	input := EmailTokenWorkflowInput{SaleID: "sale123", Email: "buyer@example.com", Token: "token456"}
	s.env.OnActivity("SendTokenEmail", mock.Anything, input.Email, input.Token).Return(nil)
	s.env.OnActivity("MarkGumroadSaleNotifiedActivity", mock.Anything, mock.Anything).Return(nil)
	s.env.ExecuteWorkflow(EmailTokenWorkflow, input)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *WorkflowTestSuite) Test_EmailTokenWorkflow_EmailFailure() {
	input := EmailTokenWorkflowInput{SaleID: "sale123", Email: "buyer@example.com", Token: "token456"}
	s.env.OnActivity("SendTokenEmail", mock.Anything, input.Email, input.Token).Return(errors.New("email failed"))
	s.env.ExecuteWorkflow(EmailTokenWorkflow, input)
	s.True(s.env.IsWorkflowCompleted())
	err := s.env.GetWorkflowError()
	s.Error(err)
	s.Contains(err.Error(), "email failed")
}

func (s *WorkflowTestSuite) Test_EmailTokenWorkflow_MarkNotifiedFailure() {
	input := EmailTokenWorkflowInput{SaleID: "sale123", Email: "buyer@example.com", Token: "token456"}
	s.env.OnActivity("SendTokenEmail", mock.Anything, input.Email, input.Token).Return(nil)
	s.env.OnActivity("MarkGumroadSaleNotifiedActivity", mock.Anything, mock.Anything).Return(errors.New("database error"))
	s.env.ExecuteWorkflow(EmailTokenWorkflow, input)
	s.True(s.env.IsWorkflowCompleted())
	err := s.env.GetWorkflowError()
	s.Error(err)
	s.Contains(err.Error(), "database error")
}
