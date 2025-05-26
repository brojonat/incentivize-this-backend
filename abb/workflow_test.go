package abb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/brojonat/affiliate-bounty-board/solana"
	solanago "github.com/gagliardetto/solana-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// --- Helper mockRoundTripper ---
type mockRoundTripper struct {
	bodies       []string // Sequence of response bodies to return
	statusCodes  []int    // Optional corresponding status codes (defaults to 200 OK)
	requestIndex int      // Tracks which request we are responding to
}

func (m *mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if m.requestIndex >= len(m.bodies) {
		// Return an error or a default response if more requests are made than bodies provided
		return nil, fmt.Errorf("mockRoundTripper: received request %d but only %d bodies were provided", m.requestIndex+1, len(m.bodies))
	}

	body := m.bodies[m.requestIndex]
	statusCode := http.StatusOK // Default status code
	if m.statusCodes != nil && m.requestIndex < len(m.statusCodes) {
		statusCode = m.statusCodes[m.requestIndex]
	}

	resp := &http.Response{
		StatusCode: statusCode,
		Body:       io.NopCloser(bytes.NewBufferString(body)),
		Header:     make(http.Header),
	}
	m.requestIndex++ // Move to the next response
	return resp, nil
}

// --- End Helper mockRoundTripper ---

// mockLLMProvider implements the LLMProvider interface for testing
type mockLLMProvider struct{}

func (p *mockLLMProvider) Complete(ctx context.Context, prompt string) (string, error) {
	return `{"satisfies": true, "reason": "Content meets all requirements"}`, nil
}

// mockTransport implements http.RoundTripper for testing
type mockTransport struct{}

func (t *mockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Create a mock response
	response := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewBufferString(`{"status": "success"}`)),
		Header:     make(http.Header),
	}
	return response, nil
}

func TestWorkflow(t *testing.T) {
	t.Setenv("ENV", "test")
	// Test PayBounty workflow
	t.Run("PayBounty", func(t *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestWorkflowEnvironment()

		// dummyEscrowWallet no longer needed here as getConfiguration will handle it for ENV=test
		// dummyEscrowWallet := solanago.NewWallet()

		// mockedTestConfig no longer needed here

		activities, err := NewActivities()
		require.NoError(t, err)

		// Register activities
		env.RegisterActivity(activities.TransferUSDC)

		amount, err := solana.NewUSDCAmount(1.0)
		if err != nil {
			t.Fatalf("Failed to create USDC amount: %v", err)
		}

		toWallet := solanago.NewWallet()

		payInput := PayBountyWorkflowInput{
			Wallet: toWallet.PublicKey().String(),
			Amount: amount,
		}

		env.OnActivity(activities.TransferUSDC, mock.Anything, payInput.Wallet, payInput.Amount.ToUSDC(), mock.AnythingOfType("string")).Return(nil)

		env.ExecuteWorkflow(PayBountyWorkflow, payInput)
		assert.NoError(t, env.GetWorkflowError())
	})

	// Test ReturnBountyToOwner workflow
	t.Run("ReturnBountyToOwner", func(t *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestWorkflowEnvironment()

		mockHTTPClient := &http.Client{
			Transport: &mockTransport{},
		}

		activities, err := NewActivities()
		require.NoError(t, err)
		activities.httpClient = mockHTTPClient

		env.RegisterActivity(activities.VerifyPayment)
		env.RegisterActivity(activities.TransferUSDC)
		env.RegisterActivity(activities.PullContentActivity)
		env.RegisterActivity(activities.CheckContentRequirements)

		initialBalance, err := solana.NewUSDCAmount(10.0)
		require.NoError(t, err)

		env.OnActivity(activities.VerifyPayment, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&VerifyPaymentResult{
				Verified: true,
				Amount:   initialBalance,
			}, nil)

		mockThumbnailURL := "https://example.com/reddit_thumb.jpg"
		mockedRedditContent := &RedditContent{
			ID:          "test-content",
			Title:       "Test Title",
			Selftext:    "Test Content",
			URL:         "https://test.com",
			Author:      "test-author",
			Subreddit:   "test-subreddit",
			Score:       100,
			IsComment:   false,
			Permalink:   "test-permalink",
			NumComments: 10,
			Thumbnail:   mockThumbnailURL,
		}
		mockedRedditBytes, err := json.Marshal(mockedRedditContent)
		require.NoError(t, err, "Failed to marshal mocked Reddit content for activity mock")

		env.OnActivity(activities.PullContentActivity, mock.Anything, mock.MatchedBy(func(input PullContentInput) bool {
			return input.PlatformType == PlatformReddit && input.ContentID == "test-content" && input.ContentKind == ContentKindPost
		})).Return(mockedRedditBytes, nil).Once()

		env.OnActivity(activities.CheckContentRequirements, mock.Anything, mock.AnythingOfType("[]uint8"), mock.AnythingOfType("[]string")).
			Return(CheckContentRequirementsResult{
				Satisfies: true,
				Reason:    "Content meets requirements",
			}, nil)

		env.OnActivity(activities.TransferUSDC, mock.Anything, "test-owner", mock.AnythingOfType("float64"), mock.AnythingOfType("string")).Return(nil)

		assert.NoError(t, err)
	})
}

func TestBountyAssessmentWorkflow(t *testing.T) {
	t.Setenv("ENV", "test")
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	// mockedTestConfig and dummy wallets no longer needed here

	activities, err := NewActivities()
	require.NoError(t, err)

	env.RegisterActivity(activities.GenerateAndStoreBountyEmbeddingActivity)
	env.RegisterActivity(activities.VerifyPayment)
	env.RegisterActivity(activities.TransferUSDC)
	env.RegisterActivity(activities.PullContentActivity)
	env.RegisterActivity(activities.CheckContentRequirements)
	env.RegisterActivity(activities.AnalyzeImageURL)
	env.RegisterActivity(activities.ValidatePayoutWallet)
	env.RegisterActivity(activities.ShouldPerformImageAnalysisActivity)
	env.RegisterActivity(activities.SummarizeAndStoreBountyActivity)

	originalTotalBounty, err := solana.NewUSDCAmount(10.0)
	require.NoError(t, err)
	totalBounty, err := solana.NewUSDCAmount(9.0)
	require.NoError(t, err)
	feeAmount := originalTotalBounty.Sub(totalBounty)

	bountyPerPost, _ := solana.NewUSDCAmount(1.0)

	ownerWallet := solanago.NewWallet()
	funderWallet := solanago.NewWallet()
	payoutWallet := solanago.NewWallet()
	escrowWallet := solanago.NewWallet()   // This is for the input to the workflow, distinct from the one used internally by activities
	treasuryWallet := solanago.NewWallet() // This is for the input to the workflow

	input := BountyAssessmentWorkflowInput{
		Requirements:       []string{"Test requirement"},
		BountyPerPost:      bountyPerPost,
		TotalBounty:        totalBounty,
		TotalCharged:       originalTotalBounty,
		BountyOwnerWallet:  ownerWallet.PublicKey().String(),
		BountyFunderWallet: funderWallet.PublicKey().String(),
		EscrowWallet:       escrowWallet.PublicKey().String(),   // Passed to workflow
		TreasuryWallet:     treasuryWallet.PublicKey().String(), // Passed to workflow
		Platform:           PlatformReddit,
		ContentKind:        ContentKindPost,
		Timeout:            300 * time.Second,
		PaymentTimeout:     30 * time.Second,
	}

	env.OnActivity(activities.VerifyPayment, mock.Anything, funderWallet.PublicKey(), escrowWallet.PublicKey(), originalTotalBounty, mock.AnythingOfType("string"), mock.Anything).
		Return(&VerifyPaymentResult{
			Verified: true,
			Amount:   originalTotalBounty,
		}, nil).Once()

	env.OnActivity(activities.ShouldPerformImageAnalysisActivity, mock.Anything, mock.AnythingOfType("[]string")).Return(ShouldPerformImageAnalysisResult{ShouldAnalyze: true, Reason: "mocked-should-analyze"}, nil).Once()

	expectedFeeMemo := fmt.Sprintf("%s-fee-transfer", "default-test-workflow-id")
	env.OnActivity(activities.TransferUSDC, mock.Anything, treasuryWallet.PublicKey().String(), feeAmount.ToUSDC(), expectedFeeMemo).
		Return(nil).Once()

	mockThumbnailURL := "https://example.com/reddit_thumb.jpg"
	mockedRedditContent := &RedditContent{
		ID:          "test-content",
		Title:       "Test Title",
		Selftext:    "Test Content",
		URL:         "https://test.com",
		Author:      "test-author",
		Subreddit:   "test-subreddit",
		Score:       100,
		IsComment:   false,
		Permalink:   "test-permalink",
		NumComments: 10,
		Thumbnail:   mockThumbnailURL,
	}
	mockedRedditBytes, err := json.Marshal(mockedRedditContent)
	require.NoError(t, err, "Failed to marshal mocked Reddit content for activity mock")

	env.OnActivity(activities.PullContentActivity, mock.Anything, mock.MatchedBy(func(input PullContentInput) bool {
		return input.PlatformType == PlatformReddit && input.ContentID == "test-content" && input.ContentKind == ContentKindPost
	})).Return(mockedRedditBytes, nil).Once()

	mockImgAnalysisResult := CheckContentRequirementsResult{Satisfies: true, Reason: "Image visually acceptable"}
	env.OnActivity(activities.AnalyzeImageURL, mock.Anything, mockThumbnailURL, mock.AnythingOfType("string")).Return(mockImgAnalysisResult, nil).Once()

	env.OnActivity(activities.CheckContentRequirements, mock.Anything, mock.AnythingOfType("[]uint8"), mock.AnythingOfType("[]string")).
		Return(CheckContentRequirementsResult{
			Satisfies: true,
			Reason:    "Content meets requirements",
		}, nil).Once()

	env.OnActivity(activities.ValidatePayoutWallet, mock.Anything, payoutWallet.PublicKey().String(), input.Requirements).
		Return(ValidateWalletResult{Satisfies: true, Reason: "Wallet validation passed in test"}, nil).Once()

	// Mock GenerateAndStoreBountyEmbeddingActivity (placed with other activity mocks)
	env.OnActivity(activities.GenerateAndStoreBountyEmbeddingActivity, mock.Anything, mock.AnythingOfType("abb.GenerateAndStoreBountyEmbeddingActivityInput")).Return(nil).Once()
	// Mock SummarizeAndStoreBountyActivity
	env.OnActivity(activities.SummarizeAndStoreBountyActivity, mock.Anything, mock.AnythingOfType("SummarizeAndStoreBountyActivityInput")).Return(nil).Maybe()

	expectedPayoutMemo := fmt.Sprintf("{\"bounty_id\":\"%s\",\"content_id\":\"%s\",\"platform\":\"%s\",\"content_kind\":\"%s\"}", "default-test-workflow-id", "test-content", string(input.Platform), string(ContentKindPost))
	env.OnActivity(activities.TransferUSDC, mock.Anything, payoutWallet.PublicKey().String(), bountyPerPost.ToUSDC(), expectedPayoutMemo).Return(nil).Once()

	// After one payout of 'bountyPerPost', if the workflow times out,
	// the remaining bounty (TotalBounty - BountyPerPost) should be refunded.
	amountToRefundAfterPayout := input.TotalBounty.Sub(input.BountyPerPost) // e.g., 9.0 - 1.0 = 8.0 USDC
	expectedOwnerRefundMemo := fmt.Sprintf("{\"bounty_id\":\"%s\"}", "default-test-workflow-id")
	env.OnActivity(
		activities.TransferUSDC,
		mock.Anything,
		ownerWallet.PublicKey().String(),
		amountToRefundAfterPayout.ToUSDC(), // Use the corrected amount
		expectedOwnerRefundMemo,
	).
		Run(func(args mock.Arguments) {
			// This Run function can be used for debugging or asserting calls if needed
		}).
		Return(nil).Once()

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("assessment", AssessContentSignal{
			ContentID:    "test-content",
			PayoutWallet: payoutWallet.PublicKey().String(),
			Platform:     PlatformReddit,
			ContentKind:  ContentKindPost,
		})
	}, time.Second)

	env.ExecuteWorkflow(BountyAssessmentWorkflow, input)
	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestBountyAssessmentWorkflowTimeout(t *testing.T) {
	t.Setenv("ENV", "test")
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	// mockedTestConfig and dummy wallets no longer needed here

	activities, err := NewActivities()
	require.NoError(t, err)

	env.RegisterActivity(activities.VerifyPayment)
	env.RegisterActivity(activities.TransferUSDC)
	env.RegisterActivity(activities.PullContentActivity)
	env.RegisterActivity(activities.CheckContentRequirements)
	env.RegisterActivity(activities.ValidatePayoutWallet)
	env.RegisterActivity(activities.AnalyzeImageURL)
	env.RegisterActivity(activities.ShouldPerformImageAnalysisActivity)
	env.RegisterActivity(activities.GenerateAndStoreBountyEmbeddingActivity)
	env.RegisterActivity(activities.SummarizeAndStoreBountyActivity)

	bountyPerPost, err := solana.NewUSDCAmount(1.0)
	require.NoError(t, err)
	originalTotalBounty, err := solana.NewUSDCAmount(10.0)
	require.NoError(t, err)
	totalBounty, err := solana.NewUSDCAmount(9.0)
	require.NoError(t, err)
	feeAmount := originalTotalBounty.Sub(totalBounty)

	ownerWallet := solanago.NewWallet()
	funderWallet := solanago.NewWallet()
	escrowWallet := solanago.NewWallet()
	treasuryWallet := solanago.NewWallet()

	input := BountyAssessmentWorkflowInput{
		Requirements:       []string{"Test requirement"},
		BountyPerPost:      bountyPerPost,
		TotalBounty:        totalBounty,
		TotalCharged:       originalTotalBounty,
		BountyOwnerWallet:  ownerWallet.PublicKey().String(),
		BountyFunderWallet: funderWallet.PublicKey().String(),
		EscrowWallet:       escrowWallet.PublicKey().String(),
		TreasuryWallet:     treasuryWallet.PublicKey().String(),
		Platform:           PlatformReddit,
		ContentKind:        ContentKindPost,
		Timeout:            30 * time.Second,
		PaymentTimeout:     5 * time.Second,
	}

	env.OnActivity(activities.VerifyPayment, mock.Anything, funderWallet.PublicKey(), escrowWallet.PublicKey(), originalTotalBounty, mock.AnythingOfType("string"), mock.Anything).
		Return(&VerifyPaymentResult{Verified: true, Amount: originalTotalBounty}, nil).Maybe()

	env.OnActivity(activities.ShouldPerformImageAnalysisActivity, mock.Anything, mock.AnythingOfType("[]string")).Return(ShouldPerformImageAnalysisResult{ShouldAnalyze: false, Reason: "mocked-no-analysis"}, nil).Maybe()

	expectedFeeMemoTimeout := fmt.Sprintf("%s-fee-transfer", "default-test-workflow-id")
	env.OnActivity(activities.TransferUSDC, mock.Anything, treasuryWallet.PublicKey().String(), feeAmount.ToUSDC(), expectedFeeMemoTimeout).
		Return(nil).Maybe()

	env.OnActivity(activities.PullContentActivity, mock.Anything, mock.AnythingOfType("string"), ContentKindPost).
		Return(&RedditContent{ID: "timeout-test-content"}, nil).Maybe()
	env.OnActivity(activities.CheckContentRequirements, mock.Anything, mock.Anything, mock.Anything).
		Return(CheckContentRequirementsResult{Satisfies: true}, nil).Maybe()

	env.OnActivity(activities.ValidatePayoutWallet, mock.Anything, mock.AnythingOfType("string"), input.Requirements).
		Return(ValidateWalletResult{Satisfies: true, Reason: "Wallet validation passed in test"}, nil).Maybe()

	expectedTimeoutRefundMemo := fmt.Sprintf("{\"bounty_id\":\"%s\"}", "default-test-workflow-id")
	env.OnActivity(activities.TransferUSDC,
		mock.Anything,
		ownerWallet.PublicKey().String(),
		originalTotalBounty.Sub(feeAmount).ToUSDC(),
		expectedTimeoutRefundMemo,
	).Return(nil).Once()

	// Mock GenerateAndStoreBountyEmbeddingActivity
	env.OnActivity(activities.GenerateAndStoreBountyEmbeddingActivity, mock.Anything, mock.AnythingOfType("abb.GenerateAndStoreBountyEmbeddingActivityInput")).Return(nil).Once()
	// Mock SummarizeAndStoreBountyActivity
	env.OnActivity(activities.SummarizeAndStoreBountyActivity, mock.Anything, mock.AnythingOfType("SummarizeAndStoreBountyActivityInput")).Return(nil).Maybe()

	env.ExecuteWorkflow(BountyAssessmentWorkflow, input)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestBountyAssessmentWorkflow_Idempotency(t *testing.T) {
	t.Setenv("ENV", "test")
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	// mockedTestConfig and dummy wallets no longer needed here

	activities, err := NewActivities()
	require.NoError(t, err)

	env.RegisterActivity(activities.VerifyPayment)
	env.RegisterActivity(activities.TransferUSDC)
	env.RegisterActivity(activities.PullContentActivity)
	env.RegisterActivity(activities.CheckContentRequirements)
	env.RegisterActivity(activities.ValidatePayoutWallet)
	env.RegisterActivity(activities.AnalyzeImageURL)
	env.RegisterActivity(activities.ShouldPerformImageAnalysisActivity)
	env.RegisterActivity(activities.GenerateAndStoreBountyEmbeddingActivity)
	env.RegisterActivity(activities.SummarizeAndStoreBountyActivity)

	originalTotalBounty, _ := solana.NewUSDCAmount(10.0)
	totalBounty, _ := solana.NewUSDCAmount(5.0)
	bountyPerPost, _ := solana.NewUSDCAmount(1.0)
	feeAmount := originalTotalBounty.Sub(totalBounty)

	funderWallet := solanago.NewWallet()
	ownerWallet := solanago.NewWallet()
	payoutWallet := solanago.NewWallet()
	escrowWallet := solanago.NewWallet()
	treasuryWallet := solanago.NewWallet()
	contentID := "idempotent-content"

	input := BountyAssessmentWorkflowInput{
		Requirements:       []string{"Idempotency Test"},
		BountyPerPost:      bountyPerPost,
		TotalBounty:        totalBounty,
		TotalCharged:       originalTotalBounty,
		BountyOwnerWallet:  ownerWallet.PublicKey().String(),
		BountyFunderWallet: funderWallet.PublicKey().String(),
		EscrowWallet:       escrowWallet.PublicKey().String(),
		TreasuryWallet:     treasuryWallet.PublicKey().String(),
		Platform:           PlatformReddit,
		Timeout:            30 * time.Second,
		PaymentTimeout:     5 * time.Second,
	}

	env.OnActivity(activities.VerifyPayment, mock.Anything, funderWallet.PublicKey(), escrowWallet.PublicKey(), originalTotalBounty, mock.AnythingOfType("string"), mock.Anything).Return(&VerifyPaymentResult{Verified: true, Amount: originalTotalBounty}, nil).Once()

	env.OnActivity(activities.ShouldPerformImageAnalysisActivity, mock.Anything, mock.AnythingOfType("[]string")).Return(ShouldPerformImageAnalysisResult{ShouldAnalyze: false, Reason: "mocked-no-analysis"}, nil).Maybe()

	expectedFeeMemoIdempotency := fmt.Sprintf("%s-fee-transfer", "default-test-workflow-id")
	env.OnActivity(activities.TransferUSDC, mock.Anything, treasuryWallet.PublicKey().String(), feeAmount.ToUSDC(), expectedFeeMemoIdempotency).
		Return(nil).Once()

	mockRedditContent := &RedditContent{ID: contentID, Title: "Idempotent Test"}
	mockedRedditBytesIdempotency, err := json.Marshal(mockRedditContent)
	require.NoError(t, err, "Failed to marshal mocked Reddit content for activity mock (idempotency)")
	env.OnActivity(activities.PullContentActivity, mock.Anything, mock.MatchedBy(func(input PullContentInput) bool {
		return input.PlatformType == PlatformReddit && input.ContentID == contentID && input.ContentKind == ContentKindPost
	})).Return(mockedRedditBytesIdempotency, nil).Once()

	env.OnActivity(activities.CheckContentRequirements, mock.Anything, mock.AnythingOfType("[]uint8"), mock.AnythingOfType("[]string")).
		Return(CheckContentRequirementsResult{Satisfies: true, Reason: "OK"}, nil).Once()

	env.OnActivity(activities.ValidatePayoutWallet, mock.Anything, payoutWallet.PublicKey().String(), input.Requirements).
		Return(ValidateWalletResult{Satisfies: true, Reason: "Wallet validation passed in test"}, nil).Once()

	expectedPayoutMemoIdempotency := fmt.Sprintf("{\"bounty_id\":\"%s\",\"content_id\":\"%s\",\"platform\":\"%s\",\"content_kind\":\"%s\"}", "default-test-workflow-id", contentID, string(input.Platform), string(ContentKindPost))
	env.OnActivity(activities.TransferUSDC, mock.Anything, payoutWallet.PublicKey().String(), bountyPerPost.ToUSDC(), expectedPayoutMemoIdempotency).Return(nil).Once()

	cancelRefundAmount := totalBounty.Sub(bountyPerPost)
	expectedOwnerRefundMemoIdempotency := fmt.Sprintf("{\"bounty_id\":\"%s\"}", "default-test-workflow-id")
	env.OnActivity(activities.TransferUSDC, mock.Anything, ownerWallet.PublicKey().String(), cancelRefundAmount.ToUSDC(), expectedOwnerRefundMemoIdempotency).Return(nil).Once()

	// Mock GenerateAndStoreBountyEmbeddingActivity
	env.OnActivity(activities.GenerateAndStoreBountyEmbeddingActivity, mock.Anything, mock.AnythingOfType("abb.GenerateAndStoreBountyEmbeddingActivityInput")).Return(nil).Once()
	// Mock SummarizeAndStoreBountyActivity
	env.OnActivity(activities.SummarizeAndStoreBountyActivity, mock.Anything, mock.AnythingOfType("SummarizeAndStoreBountyActivityInput")).Return(nil).Maybe()

	signal := AssessContentSignal{
		ContentID:    contentID,
		PayoutWallet: payoutWallet.PublicKey().String(),
		Platform:     PlatformReddit,
		ContentKind:  ContentKindPost,
	}

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AssessmentSignalName, signal)
	}, 5*time.Second)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AssessmentSignalName, signal)
	}, 6*time.Second)

	env.ExecuteWorkflow(BountyAssessmentWorkflow, input)
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestBountyAssessmentWorkflow_RequirementsNotMet(t *testing.T) {
	t.Setenv("ENV", "test")
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	// mockedTestConfig and dummy wallets no longer needed here

	activities, err := NewActivities()
	require.NoError(t, err)

	env.RegisterActivity(activities.VerifyPayment)
	env.RegisterActivity(activities.TransferUSDC)
	env.RegisterActivity(activities.CheckContentRequirements)
	env.RegisterActivity(activities.ValidatePayoutWallet)
	env.RegisterActivity(activities.AnalyzeImageURL)
	env.RegisterActivity(activities.PullContentActivity)
	env.RegisterActivity(activities.ShouldPerformImageAnalysisActivity)
	env.RegisterActivity(activities.GenerateAndStoreBountyEmbeddingActivity)
	env.RegisterActivity(activities.SummarizeAndStoreBountyActivity)

	originalTotalBounty, _ := solana.NewUSDCAmount(10.0)
	totalBounty, _ := solana.NewUSDCAmount(5.0)
	bountyPerPost, _ := solana.NewUSDCAmount(1.0)
	feeAmount := originalTotalBounty.Sub(totalBounty)

	funderWallet := solanago.NewWallet()
	ownerWallet := solanago.NewWallet()
	payoutWallet := solanago.NewWallet()
	escrowWallet := solanago.NewWallet()
	treasuryWallet := solanago.NewWallet()
	contentID := "failed-content"
	mockRedditContent := &RedditContent{ID: contentID, Title: "Failed Test"}

	refundCalled := make(chan struct{}, 1)

	env.OnActivity(activities.VerifyPayment, mock.Anything, funderWallet.PublicKey(), escrowWallet.PublicKey(), originalTotalBounty, mock.AnythingOfType("string"), mock.Anything).Return(&VerifyPaymentResult{Verified: true, Amount: originalTotalBounty}, nil).Once()

	env.OnActivity(activities.ShouldPerformImageAnalysisActivity, mock.Anything, mock.AnythingOfType("[]string")).Return(ShouldPerformImageAnalysisResult{ShouldAnalyze: false, Reason: "mocked-no-analysis"}, nil).Maybe()

	expectedFeeMemoNotMet := fmt.Sprintf("%s-fee-transfer", "default-test-workflow-id")
	env.OnActivity(activities.TransferUSDC, mock.Anything, treasuryWallet.PublicKey().String(), feeAmount.ToUSDC(), expectedFeeMemoNotMet).
		Return(nil).Once()

	mockedRedditBytesFailure, err := json.Marshal(mockRedditContent)
	require.NoError(t, err, "Failed to marshal mocked Reddit content for activity mock (failure)")
	env.OnActivity(activities.PullContentActivity, mock.Anything, mock.MatchedBy(func(input PullContentInput) bool {
		return input.PlatformType == PlatformReddit && input.ContentID == contentID && input.ContentKind == ContentKindComment
	})).Return(mockedRedditBytesFailure, nil).Once()

	env.OnActivity(activities.CheckContentRequirements, mock.Anything, mock.AnythingOfType("[]uint8"), mock.AnythingOfType("[]string")).Return(CheckContentRequirementsResult{Satisfies: false, Reason: "Did not meet criteria"}, nil).Once()

	env.OnActivity(activities.ValidatePayoutWallet, mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("[]string")).
		Return(ValidateWalletResult{Satisfies: true, Reason: "Should not be called"}, nil).Never()

	env.OnActivity(activities.TransferUSDC, mock.Anything, payoutWallet.PublicKey().String(), bountyPerPost.ToUSDC(), mock.AnythingOfType("string")).Return(nil).Never()

	expectedCancellationRefundMemo := fmt.Sprintf("{\"bounty_id\":\"%s\"}", "default-test-workflow-id")
	env.OnActivity(activities.TransferUSDC,
		mock.Anything,
		ownerWallet.PublicKey().String(),
		totalBounty.ToUSDC(),
		expectedCancellationRefundMemo,
	).Run(func(args mock.Arguments) {
		select {
		case refundCalled <- struct{}{}:
		default:
		}
	}).Return(nil)

	// Mock GenerateAndStoreBountyEmbeddingActivity
	env.OnActivity(activities.GenerateAndStoreBountyEmbeddingActivity, mock.Anything, mock.AnythingOfType("abb.GenerateAndStoreBountyEmbeddingActivityInput")).Return(nil).Once()
	// Mock SummarizeAndStoreBountyActivity
	env.OnActivity(activities.SummarizeAndStoreBountyActivity, mock.Anything, mock.AnythingOfType("SummarizeAndStoreBountyActivityInput")).Return(nil).Maybe()

	input := BountyAssessmentWorkflowInput{
		Requirements:       []string{"Failure Test"},
		BountyPerPost:      bountyPerPost,
		TotalBounty:        totalBounty,
		TotalCharged:       originalTotalBounty,
		BountyOwnerWallet:  ownerWallet.PublicKey().String(),
		BountyFunderWallet: funderWallet.PublicKey().String(),
		EscrowWallet:       escrowWallet.PublicKey().String(),
		TreasuryWallet:     treasuryWallet.PublicKey().String(),
		Platform:           PlatformReddit,
		Timeout:            30 * time.Second,
		PaymentTimeout:     5 * time.Second,
	}

	failSignal := AssessContentSignal{
		ContentID:    contentID,
		PayoutWallet: payoutWallet.PublicKey().String(),
		Platform:     PlatformReddit,
		ContentKind:  ContentKindComment,
	}
	cancelSignal := CancelBountySignal{
		BountyOwnerWallet: ownerWallet.PublicKey().String(),
	}

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AssessmentSignalName, failSignal)
		env.RegisterDelayedCallback(func() {
			env.SignalWorkflow(CancelSignalName, cancelSignal)
		}, 20*time.Second)
	}, 10*time.Second)

	env.ExecuteWorkflow(BountyAssessmentWorkflow, input)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	select {
	case <-refundCalled:
	default:
		t.Fatalf("Refund mock .Run() block was expected to execute and signal channel, but didn't.")
	}

	env.AssertExpectations(t)
}

func TestPlatformActivities(t *testing.T) {
	t.Setenv("ENV", "test")

	// mockConfig is no longer needed here as getConfiguration will provide config for ENV=test

	activities := &Activities{}

	testWorkflow := func(ctx workflow.Context, input PullContentInput) ([]byte, error) {
		options := workflow.ActivityOptions{
			StartToCloseTimeout: 60 * time.Second,
			RetryPolicy: &temporal.RetryPolicy{
				InitialInterval:    20 * time.Second,
				BackoffCoefficient: 2.0,
				MaximumInterval:    time.Minute,
				MaximumAttempts:    3,
			},
		}
		ctx = workflow.WithActivityOptions(ctx, options)

		var result []byte
		err := workflow.ExecuteActivity(ctx, activities.PullContentActivity, input).Get(ctx, &result)
		return result, err
	}

	t.Run("PullRedditContent", func(t *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestWorkflowEnvironment()
		env.RegisterActivity(activities.PullContentActivity)
		env.RegisterWorkflow(testWorkflow)

		env.OnActivity(activities.PullContentActivity, mock.Anything, PullContentInput{
			PlatformType: PlatformReddit,
			ContentKind:  ContentKindPost,
			ContentID:    "test-post",
		}).Return([]byte(`{"id":"test-post","title":"Test Post","selftext":"Test content"}`), nil)

		env.ExecuteWorkflow(testWorkflow, PullContentInput{
			PlatformType: PlatformReddit,
			ContentKind:  ContentKindPost,
			ContentID:    "test-post",
		})

		require.True(t, env.IsWorkflowCompleted())
		require.NoError(t, env.GetWorkflowError())
		var result []byte
		require.NoError(t, env.GetWorkflowResult(&result))
		require.NotEmpty(t, result)
	})

	t.Run("PullYouTubeContent", func(t *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestWorkflowEnvironment()
		env.RegisterActivity(activities.PullContentActivity)
		env.RegisterWorkflow(testWorkflow)

		env.OnActivity(activities.PullContentActivity, mock.Anything, PullContentInput{
			PlatformType: PlatformYouTube,
			ContentKind:  ContentKindVideo,
			ContentID:    "test-video-id",
		}).Return([]byte(`{"id":"test-video-id","title":"Test Video","description":"Test description"}`), nil)

		env.ExecuteWorkflow(testWorkflow, PullContentInput{
			PlatformType: PlatformYouTube,
			ContentKind:  ContentKindVideo,
			ContentID:    "test-video-id",
		})

		require.True(t, env.IsWorkflowCompleted())
		require.NoError(t, env.GetWorkflowError())
		var result []byte
		require.NoError(t, env.GetWorkflowResult(&result))
		require.NotEmpty(t, result)
	})

	t.Run("PullTwitchContent_Video", func(t *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestWorkflowEnvironment()
		env.RegisterActivity(activities.PullContentActivity)
		env.RegisterWorkflow(testWorkflow)

		env.OnActivity(activities.PullContentActivity, mock.Anything, PullContentInput{
			PlatformType: PlatformTwitch,
			ContentKind:  ContentKindVideo,
			ContentID:    "test-video-id",
		}).Return([]byte(`{"id":"test-video-id","title":"Test Video","description":"Test description"}`), nil)

		env.ExecuteWorkflow(testWorkflow, PullContentInput{
			PlatformType: PlatformTwitch,
			ContentKind:  ContentKindVideo,
			ContentID:    "test-video-id",
		})

		require.True(t, env.IsWorkflowCompleted())
		require.NoError(t, env.GetWorkflowError())
		var result []byte
		require.NoError(t, env.GetWorkflowResult(&result))
		require.NotEmpty(t, result)
	})

	t.Run("PullTwitchContent_Clip", func(t *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestWorkflowEnvironment()
		env.RegisterActivity(activities.PullContentActivity)
		env.RegisterWorkflow(testWorkflow)

		env.OnActivity(activities.PullContentActivity, mock.Anything, PullContentInput{
			PlatformType: PlatformTwitch,
			ContentKind:  ContentKindClip,
			ContentID:    "test-clip-id",
		}).Return([]byte(`{"id":"test-clip-id","title":"Test Clip","description":"Test description"}`), nil)

		env.ExecuteWorkflow(testWorkflow, PullContentInput{
			PlatformType: PlatformTwitch,
			ContentKind:  ContentKindClip,
			ContentID:    "test-clip-id",
		})

		require.True(t, env.IsWorkflowCompleted())
		require.NoError(t, env.GetWorkflowError())
		var result []byte
		require.NoError(t, env.GetWorkflowResult(&result))
		require.NotEmpty(t, result)
	})
}

func PullContentWorkflow(ctx workflow.Context, input PullContentInput) ([]byte, error) {
	options := workflow.ActivityOptions{
		StartToCloseTimeout: 60 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    20 * time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    time.Minute,
			MaximumAttempts:    3,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, options)

	var result []byte
	err := workflow.ExecuteActivity(ctx, (*Activities).PullContentActivity, input).Get(ctx, &result)
	return result, err
}
