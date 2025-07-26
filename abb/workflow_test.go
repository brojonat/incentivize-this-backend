package abb

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/brojonat/affiliate-bounty-board/solana"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

type WorkflowTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func (s *WorkflowTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	// Set test environment to avoid configuration issues
	s.env.SetTestTimeout(30 * time.Second)

	// Register workflows
	s.env.RegisterWorkflow(BountyAssessmentWorkflow)
	s.env.RegisterWorkflow(ContactUsNotifyWorkflow)
	s.env.RegisterWorkflow(EmailTokenWorkflow)

	// Register activities (required for mocking to work)
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
	// Register the orchestrator workflow
	s.env.RegisterWorkflow(OrchestratorWorkflow)

	// Test the simple case where LLM responds without needing tools
	input := OrchestratorWorkflowInput{
		Tools: []Tool{},
	}
	prompt := "What is 2+2?"
	s.env.OnActivity("GetOrchestratorPromptActivity", mock.Anything).Return(prompt, nil)

	// Mock LLM response with a tool call to submit_decision
	s.env.OnActivity("GenerateResponse", mock.Anything,
		[]Message{{Role: "user", Content: prompt}},
		[]Tool{SubmitDecisionTool}).
		Return(&LLMResponse{
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
	// Register the orchestrator workflow
	s.env.RegisterWorkflow(OrchestratorWorkflow)

	// Test the simple case where LLM responds without needing tools
	input := OrchestratorWorkflowInput{
		Tools: []Tool{},
	}
	prompt := "Is this valid?"
	s.env.OnActivity("GetOrchestratorPromptActivity", mock.Anything).Return(prompt, nil)

	// Mock LLM response with a rejection
	s.env.OnActivity("GenerateResponse", mock.Anything,
		[]Message{{Role: "user", Content: prompt}},
		[]Tool{SubmitDecisionTool}).
		Return(&LLMResponse{
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
	// Register the orchestrator workflow
	s.env.RegisterWorkflow(OrchestratorWorkflow)

	// Test the case where LLM makes a tool call, then provides final response
	input := OrchestratorWorkflowInput{
		Tools: []Tool{GetContentDetailsTool},
	}
	prompt := "Get content details for post 123"
	s.env.OnActivity("GetOrchestratorPromptActivity", mock.Anything).Return(prompt, nil)

	// First LLM call - requests tool
	s.env.OnActivity("GenerateResponse", mock.Anything,
		[]Message{{Role: "user", Content: prompt}},
		[]Tool{GetContentDetailsTool, SubmitDecisionTool}).
		Return(&LLMResponse{
			ToolCalls: []ToolCall{{
				ID:        "call1",
				Name:      "get_content_details",
				Arguments: `{"platform":"reddit","content_kind":"post","content_id":"123"}`,
			}},
		}, nil)

	// Mock the tool execution
	s.env.OnActivity("PullContentActivity", mock.Anything,
		PullContentInput{
			PlatformType: "reddit",
			ContentKind:  "post",
			ContentID:    "123",
		}).
		Return([]byte(`{"title":"Test Post","content":"Hello world"}`), nil)

	// Second LLM call - final response with tool result
	expectedMessages := []Message{
		{Role: "user", Content: prompt},
		{Role: "assistant", ToolCalls: []ToolCall{{
			ID:        "call1",
			Name:      "get_content_details",
			Arguments: `{"platform":"reddit","content_kind":"post","content_id":"123"}`,
		}}},
		{Role: "tool", ToolCallID: "call1", Content: `{"title":"Test Post","content":"Hello world"}`},
	}
	s.env.OnActivity("GenerateResponse", mock.Anything, expectedMessages, []Tool{GetContentDetailsTool, SubmitDecisionTool}).
		Return(&LLMResponse{
			ToolCalls: []ToolCall{{
				ID:        "decision1",
				Name:      "submit_decision",
				Arguments: `{"is_approved": true, "reason": "Content retrieved successfully."}`,
			}},
		}, nil)

	s.env.ExecuteWorkflow(OrchestratorWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result *OrchestratorWorkflowOutput
	s.NoError(s.env.GetWorkflowResult(&result))
	s.True(result.IsApproved)
	s.Equal("Content retrieved successfully.", result.Reason)
}

func (s *WorkflowTestSuite) Test_OrchestratorWorkflow_ToolCall_Failure() {
	// Register the orchestrator workflow
	s.env.RegisterWorkflow(OrchestratorWorkflow)

	// Test tool execution failure
	input := OrchestratorWorkflowInput{
		Tools: []Tool{GetContentDetailsTool},
	}
	prompt := "Get content details for invalid post"
	s.env.OnActivity("GetOrchestratorPromptActivity", mock.Anything).Return(prompt, nil)

	// First LLM call
	s.env.OnActivity("GenerateResponse", mock.Anything,
		[]Message{{Role: "user", Content: prompt}},
		[]Tool{GetContentDetailsTool, SubmitDecisionTool}).
		Return(&LLMResponse{
			ToolCalls: []ToolCall{{
				ID:        "call1",
				Name:      "get_content_details",
				Arguments: `{"platform":"reddit","content_kind":"post","content_id":"invalid"}`,
			}},
		}, nil).Once()

	// Tool execution fails
	s.env.OnActivity("PullContentActivity", mock.Anything, mock.Anything).
		Return(nil, errors.New("content not found"))

	// Second LLM call with error - should provide final response without tool calls
	s.env.OnActivity("GenerateResponse",
		mock.Anything, // context
		mock.MatchedBy(func(messages []Message) bool {
			if len(messages) != 3 {
				return false
			}
			lastMessage := messages[2]
			// Check that the tool result contains the error, without being too specific about the format.
			return lastMessage.Role == "tool" &&
				lastMessage.ToolCallID == "call1" &&
				strings.Contains(lastMessage.Content, "failed to execute tool") &&
				strings.Contains(lastMessage.Content, "content not found")
		}),
		[]Tool{GetContentDetailsTool, SubmitDecisionTool},
	).Return(&LLMResponse{
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
	// Register the orchestrator workflow
	s.env.RegisterWorkflow(OrchestratorWorkflow)

	// Test unknown tool handling
	input := OrchestratorWorkflowInput{
		Tools: []Tool{GetContentDetailsTool},
	}
	prompt := "Do something"
	s.env.OnActivity("GetOrchestratorPromptActivity", mock.Anything).Return(prompt, nil)

	// LLM requests unknown tool
	s.env.OnActivity("GenerateResponse", mock.Anything,
		[]Message{{Role: "user", Content: prompt}},
		[]Tool{GetContentDetailsTool, SubmitDecisionTool}).
		Return(&LLMResponse{
			ToolCalls: []ToolCall{{
				ID:        "call1",
				Name:      "unknown_tool",
				Arguments: `{}`,
			}},
		}, nil).Once()

	// Second call with error response - should provide final response without tool calls
	expectedMessages := []Message{
		{Role: "user", Content: prompt},
		{Role: "assistant", ToolCalls: []ToolCall{{
			ID:        "call1",
			Name:      "unknown_tool",
			Arguments: `{}`,
		}}},
		{Role: "tool", ToolCallID: "call1", Content: `{"error": "unknown tool requested"}`},
	}
	s.env.OnActivity("GenerateResponse", mock.Anything, expectedMessages, []Tool{GetContentDetailsTool, SubmitDecisionTool}).
		Return(&LLMResponse{
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
	s.env.RegisterWorkflow(OrchestratorWorkflow)

	input := OrchestratorWorkflowInput{
		Tools: []Tool{GetContentDetailsTool, AnalyzeImageURLTool},
	}
	prompt := "Analyze the thumbnail of the reddit post with id '123' to see if it contains a cat."
	imageURL := "http://example.com/cat_thumbnail.jpg"
	analysisPrompt := "the image must contain a cat"

	s.env.OnActivity("GetOrchestratorPromptActivity", mock.Anything).Return(prompt, nil)

	// First LLM call - requests content details
	s.env.OnActivity("GenerateResponse", mock.Anything,
		[]Message{{Role: "user", Content: prompt}},
		[]Tool{GetContentDetailsTool, AnalyzeImageURLTool, SubmitDecisionTool}).
		Return(&LLMResponse{
			ToolCalls: []ToolCall{{
				ID:        "call1",
				Name:      "get_content_details",
				Arguments: `{"platform":"reddit","content_kind":"post","content_id":"123"}`,
			}},
		}, nil).Once()

	// Mock the PullContentActivity
	s.env.OnActivity("PullContentActivity", mock.Anything,
		PullContentInput{PlatformType: "reddit", ContentKind: "post", ContentID: "123"},
	).Return([]byte(`{"title":"Post about cats","thumbnail_url":"`+imageURL+`"}`), nil)

	// Second LLM call - requests image analysis with specific prompt
	expectedMessagesAfterContent := []Message{
		{Role: "user", Content: prompt},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "call1", Name: "get_content_details", Arguments: `{"platform":"reddit","content_kind":"post","content_id":"123"}`}}},
		{Role: "tool", ToolCallID: "call1", Content: `{"title":"Post about cats","thumbnail_url":"` + imageURL + `"}`},
	}
	s.env.OnActivity("GenerateResponse", mock.Anything, expectedMessagesAfterContent, []Tool{GetContentDetailsTool, AnalyzeImageURLTool, SubmitDecisionTool}).
		Return(&LLMResponse{
			ToolCalls: []ToolCall{{
				ID:        "call2",
				Name:      "analyze_image_url",
				Arguments: `{"image_url":"` + imageURL + `", "prompt":"` + analysisPrompt + `"}`,
			}},
		}, nil).Once()

	// Mock the AnalyzeImageURL activity
	analysisResult := CheckContentRequirementsResult{
		Satisfies: true,
		Reason:    "The image contains a cat.",
	}
	s.env.OnActivity("AnalyzeImageURL", mock.Anything, imageURL, analysisPrompt).
		Return(analysisResult, nil)

	// Third LLM call - final decision
	expectedMessagesAfterAnalysis := []Message{
		{Role: "user", Content: prompt},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "call1", Name: "get_content_details", Arguments: `{"platform":"reddit","content_kind":"post","content_id":"123"}`}}},
		{Role: "tool", ToolCallID: "call1", Content: `{"title":"Post about cats","thumbnail_url":"` + imageURL + `"}`},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "call2", Name: "analyze_image_url", Arguments: `{"image_url":"` + imageURL + `", "prompt":"` + analysisPrompt + `"}`}}},
		{Role: "tool", ToolCallID: "call2", Content: `{"satisfies":true,"reason":"The image contains a cat."}`},
	}
	s.env.OnActivity("GenerateResponse", mock.Anything, expectedMessagesAfterAnalysis, []Tool{GetContentDetailsTool, AnalyzeImageURLTool, SubmitDecisionTool}).
		Return(&LLMResponse{
			ToolCalls: []ToolCall{{
				ID:        "decision1",
				Name:      "submit_decision",
				Arguments: `{"is_approved":true,"reason":"The post is about cats and the thumbnail contains a cat."}`,
			}},
		}, nil).Once()

	s.env.ExecuteWorkflow(OrchestratorWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result *OrchestratorWorkflowOutput
	s.NoError(s.env.GetWorkflowResult(&result))
	s.True(result.IsApproved)
	s.Equal("The post is about cats and the thumbnail contains a cat.", result.Reason)
}

func (s *WorkflowTestSuite) Test_OrchestratorWorkflow_MaxTurnsExceeded() {
	// Register the orchestrator workflow
	s.env.RegisterWorkflow(OrchestratorWorkflow)

	// Test infinite loop protection
	input := OrchestratorWorkflowInput{
		Tools: []Tool{GetContentDetailsTool},
	}
	prompt := "Keep calling tools forever"
	s.env.OnActivity("GetOrchestratorPromptActivity", mock.Anything).Return(prompt, nil)

	// Mock infinite tool calling - but not the decision tool
	s.env.OnActivity("GenerateResponse", mock.Anything, mock.Anything, []Tool{GetContentDetailsTool, SubmitDecisionTool}).
		Return(&LLMResponse{
			ToolCalls: []ToolCall{{
				ID: "call1", Name: "get_content_details", Arguments: `{"platform":"reddit","content_kind":"post","content_id":"123"}`,
			}},
		}, nil)

	s.env.OnActivity("PullContentActivity", mock.Anything, mock.Anything).
		Return([]byte(`{"data":"test"}`), nil)

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
	// Test workflow when funding check fails
	bountyPerPost, _ := solana.NewUSDCAmount(10)
	totalBounty, _ := solana.NewUSDCAmount(100)
	totalCharged, _ := solana.NewUSDCAmount(105) // Example with fee

	input := BountyAssessmentWorkflowInput{
		BountyPerPost:  bountyPerPost,
		TotalBounty:    totalBounty,
		TotalCharged:   totalCharged,
		Platform:       PlatformReddit,
		ContentKind:    ContentKindPost,
		Timeout:        1 * time.Hour,
		PaymentTimeout: 5 * time.Minute,
		EscrowWallet:   "escrow123",
	}

	// Mock funding check failure by returning an empty funder wallet
	s.env.OnActivity("CheckBountyFundedActivity", mock.Anything, mock.AnythingOfType("abb.CheckBountyFundedActivityInput")).
		Return("", nil)

	s.env.ExecuteWorkflow(BountyAssessmentWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *WorkflowTestSuite) Test_BountyAssessmentWorkflow_SuccessfulClaim() {
	// Test successful claim processing
	bountyPerPost, _ := solana.NewUSDCAmount(10)
	totalBounty, _ := solana.NewUSDCAmount(10) // Will be drained after one payout
	totalCharged, _ := solana.NewUSDCAmount(10)

	input := BountyAssessmentWorkflowInput{
		Requirements:   []string{"Must be helpful"},
		BountyPerPost:  bountyPerPost,
		TotalBounty:    totalBounty,
		TotalCharged:   totalCharged,
		Platform:       PlatformReddit,
		ContentKind:    ContentKindPost,
		Timeout:        1 * time.Hour, // Long timeout for testing - signal will drain the bounty
		PaymentTimeout: 5 * time.Minute,
		EscrowWallet:   "escrow123",
	}

	// Mock successful funding
	s.env.OnActivity("CheckBountyFundedActivity", mock.Anything, mock.AnythingOfType("abb.CheckBountyFundedActivityInput")).
		Return("funder_wallet_123", nil)

	// Register the child workflow to return approved result
	s.env.RegisterWorkflowWithOptions(func(ctx workflow.Context, input OrchestratorWorkflowInput) (*OrchestratorWorkflowOutput, error) {
		return &OrchestratorWorkflowOutput{
			IsApproved: true,
			Reason:     "Content meets requirements",
		}, nil
	}, workflow.RegisterOptions{Name: "OrchestratorWorkflow"})

	// Mock the payout activity
	s.env.OnActivity("PayBountyActivity", mock.Anything,
		mock.AnythingOfType("string"), // bountyID
		"wallet123",                   // payoutWallet
		bountyPerPost,
	).Return(nil)

	// Send signal before executing workflow to ensure it's processed
	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(AssessmentSignalName, AssessContentSignal{
			ContentID:    "post123",
			PayoutWallet: "wallet123",
			Platform:     PlatformReddit,
			ContentKind:  ContentKindPost,
		})
	}, 0)

	s.env.ExecuteWorkflow(BountyAssessmentWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *WorkflowTestSuite) Test_BountyAssessmentWorkflow_RejectedClaim() {
	// Test claim rejection
	bountyPerPost, _ := solana.NewUSDCAmount(10)
	totalBounty, _ := solana.NewUSDCAmount(100)
	totalCharged, _ := solana.NewUSDCAmount(105)

	input := BountyAssessmentWorkflowInput{
		BountyPerPost:  bountyPerPost,
		TotalBounty:    totalBounty,
		TotalCharged:   totalCharged,
		Platform:       PlatformReddit,
		ContentKind:    ContentKindPost,
		Timeout:        1 * time.Second, // Short timeout - workflow will timeout after processing signal
		PaymentTimeout: 5 * time.Minute,
		EscrowWallet:   "escrow123",
	}

	// Mock successful funding
	s.env.OnActivity("CheckBountyFundedActivity", mock.Anything, mock.AnythingOfType("abb.CheckBountyFundedActivityInput")).
		Return("funder_wallet_123", nil)

	// Register child workflow to reject claim
	s.env.RegisterWorkflowWithOptions(func(ctx workflow.Context, input OrchestratorWorkflowInput) (*OrchestratorWorkflowOutput, error) {
		return &OrchestratorWorkflowOutput{
			IsApproved: false,
			Reason:     "Content does not meet requirements",
		}, nil
	}, workflow.RegisterOptions{Name: "OrchestratorWorkflow"})

	// Send signal before executing workflow
	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(AssessmentSignalName, AssessContentSignal{
			ContentID:    "post123",
			PayoutWallet: "wallet123",
			Platform:     PlatformReddit,
			ContentKind:  ContentKindPost,
		})
	}, 0)

	s.env.ExecuteWorkflow(BountyAssessmentWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *WorkflowTestSuite) Test_BountyAssessmentWorkflow_Timeout() {
	// Test workflow timeout and refund
	bountyPerPost, _ := solana.NewUSDCAmount(10)
	totalBounty, _ := solana.NewUSDCAmount(100)
	totalCharged, _ := solana.NewUSDCAmount(105)
	funderWallet := "funder_wallet_for_refund"

	input := BountyAssessmentWorkflowInput{
		BountyPerPost:  bountyPerPost,
		TotalBounty:    totalBounty,
		TotalCharged:   totalCharged,
		Timeout:        1 * time.Millisecond, // Very short timeout
		PaymentTimeout: 1 * time.Minute,
		EscrowWallet:   "escrow123",
	}

	// Mock successful funding
	s.env.OnActivity("CheckBountyFundedActivity", mock.Anything, mock.AnythingOfType("abb.CheckBountyFundedActivityInput")).
		Return(funderWallet, nil)

	// Expect a refund because the bounty times out with remaining funds
	s.env.OnActivity("RefundBountyActivity", mock.Anything,
		mock.AnythingOfType("string"), // bountyID
		funderWallet,                  // refundRecipient
		totalBounty,                   // amount
	).Return(nil)

	s.env.ExecuteWorkflow(BountyAssessmentWorkflow, input)

	// Don't send any signals, let it timeout
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *WorkflowTestSuite) Test_BountyAssessmentWorkflow_ClaimCooldown() {
	// Test that same content can't be claimed twice within cooldown period
	bountyPerPost, _ := solana.NewUSDCAmount(10)
	totalBounty, _ := solana.NewUSDCAmount(100)
	totalCharged, _ := solana.NewUSDCAmount(105)

	input := BountyAssessmentWorkflowInput{
		BountyPerPost:  bountyPerPost,
		TotalBounty:    totalBounty,
		TotalCharged:   totalCharged,
		Platform:       PlatformReddit,
		ContentKind:    ContentKindPost,
		Timeout:        1 * time.Second, // Short timeout - workflow will timeout after processing signals
		PaymentTimeout: 5 * time.Minute,
		EscrowWallet:   "escrow123",
	}

	s.env.OnActivity("CheckBountyFundedActivity", mock.Anything, mock.AnythingOfType("abb.CheckBountyFundedActivityInput")).
		Return("funder_wallet_123", nil)

	// Register child workflow to reject first claim
	callCount := 0
	s.env.RegisterWorkflowWithOptions(func(ctx workflow.Context, input OrchestratorWorkflowInput) (*OrchestratorWorkflowOutput, error) {
		callCount++
		return &OrchestratorWorkflowOutput{
			IsApproved: false,
			Reason:     "Content rejected",
		}, nil
	}, workflow.RegisterOptions{Name: "OrchestratorWorkflow"})

	// Send signals before executing workflow
	signal := AssessContentSignal{
		ContentID:    "same_content",
		PayoutWallet: "wallet123",
		Platform:     PlatformReddit,
		ContentKind:  ContentKindPost,
	}

	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(AssessmentSignalName, signal)
		s.env.SignalWorkflow(AssessmentSignalName, signal) // Should be ignored due to cooldown
	}, 0)

	s.env.ExecuteWorkflow(BountyAssessmentWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	// Child workflow should only be called once
	s.Equal(1, callCount)
}

func (s *WorkflowTestSuite) Test_BountyAssessmentWorkflow_SuccessfulRefund() {
	// Test that remaining funds are refunded to the original funder
	bountyPerPost, _ := solana.NewUSDCAmount(10)
	totalBounty, _ := solana.NewUSDCAmount(100)
	totalCharged, _ := solana.NewUSDCAmount(105)
	funderWallet := "original_funder_wallet"

	input := BountyAssessmentWorkflowInput{
		BountyPerPost:  bountyPerPost,
		TotalBounty:    totalBounty,
		TotalCharged:   totalCharged,
		Timeout:        100 * time.Millisecond, // Short timeout to trigger refund
		PaymentTimeout: 1 * time.Minute,
		EscrowWallet:   "escrow123",
	}

	// Mock successful funding
	s.env.OnActivity("CheckBountyFundedActivity", mock.Anything, mock.AnythingOfType("abb.CheckBountyFundedActivityInput")).
		Return(funderWallet, nil)

	// Expect a refund to the specific funder wallet
	s.env.OnActivity("RefundBountyActivity", mock.Anything,
		mock.AnythingOfType("string"),
		funderWallet,
		totalBounty,
	).Return(nil)

	s.env.ExecuteWorkflow(BountyAssessmentWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

// SIMPLE WORKFLOW TESTS

func (s *WorkflowTestSuite) Test_ContactUsNotifyWorkflow_Success() {
	input := ContactUsNotifyWorkflowInput{
		Name:    "John Doe",
		Email:   "john@example.com",
		Message: "Hello there",
	}

	s.env.OnActivity("SendContactUsEmail", mock.Anything, input).
		Return(nil)

	s.env.ExecuteWorkflow(ContactUsNotifyWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *WorkflowTestSuite) Test_ContactUsNotifyWorkflow_EmailFailure() {
	input := ContactUsNotifyWorkflowInput{
		Name:    "John Doe",
		Email:   "john@example.com",
		Message: "Hello there",
	}

	s.env.OnActivity("SendContactUsEmail", mock.Anything, input).
		Return(errors.New("email service unavailable"))

	s.env.ExecuteWorkflow(ContactUsNotifyWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	err := s.env.GetWorkflowError()
	s.Error(err)
	s.Contains(err.Error(), "email service unavailable")
}

func (s *WorkflowTestSuite) Test_EmailTokenWorkflow_Success() {
	input := EmailTokenWorkflowInput{
		SaleID: "sale123",
		Email:  "buyer@example.com",
		Token:  "token456",
	}

	s.env.OnActivity("SendTokenEmail", mock.Anything, input.Email, input.Token).
		Return(nil)

	s.env.OnActivity("MarkGumroadSaleNotifiedActivity", mock.Anything,
		MarkGumroadSaleNotifiedActivityInput{
			SaleID: input.SaleID,
			APIKey: input.Token,
		}).
		Return(nil)

	s.env.ExecuteWorkflow(EmailTokenWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

func (s *WorkflowTestSuite) Test_EmailTokenWorkflow_EmailFailure() {
	input := EmailTokenWorkflowInput{
		SaleID: "sale123",
		Email:  "buyer@example.com",
		Token:  "token456",
	}

	s.env.OnActivity("SendTokenEmail", mock.Anything, input.Email, input.Token).
		Return(errors.New("email failed"))

	s.env.ExecuteWorkflow(EmailTokenWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	err := s.env.GetWorkflowError()
	s.Error(err)
	s.Contains(err.Error(), "email failed")
}

func (s *WorkflowTestSuite) Test_EmailTokenWorkflow_MarkNotifiedFailure() {
	input := EmailTokenWorkflowInput{
		SaleID: "sale123",
		Email:  "buyer@example.com",
		Token:  "token456",
	}

	s.env.OnActivity("SendTokenEmail", mock.Anything, input.Email, input.Token).
		Return(nil)

	s.env.OnActivity("MarkGumroadSaleNotifiedActivity", mock.Anything, mock.Anything).
		Return(errors.New("database error"))

	s.env.ExecuteWorkflow(EmailTokenWorkflow, input)

	s.True(s.env.IsWorkflowCompleted())
	err := s.env.GetWorkflowError()
	s.Error(err)
	s.Contains(err.Error(), "database error")
}
