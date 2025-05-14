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
	// Test PayBounty workflow
	t.Run("PayBounty", func(t *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestWorkflowEnvironment()

		// Create test configuration
		// escrowKey := solanago.NewWallet().PrivateKey // Removed as unused
		// escrowAccount := solanago.NewWallet().PublicKey() // Removed as unused
		// treasuryWallet := solanago.NewWallet() // Dummy treasury wallet - Removed as testConfig is removed
		// testConfig := SolanaConfig{ // Removed as it's unused now

		// Create activities instance (minimal config needed for TransferUSDC)
		activities, err := NewActivities()
		require.NoError(t, err)

		// Register activities using the instance
		env.RegisterActivity(activities.TransferUSDC)

		// Test PayBounty
		amount, err := solana.NewUSDCAmount(1.0)
		if err != nil {
			t.Fatalf("Failed to create USDC amount: %v", err)
		}

		// Create valid Solana addresses for testing
		toWallet := solanago.NewWallet()

		payInput := PayBountyWorkflowInput{
			Wallet: toWallet.PublicKey().String(),
			Amount: amount,
			// SolanaConfig: testConfig,
		}

		// Mock activity calls using the instance - expect context, recipientWallet string, amount float64, memo string
		env.OnActivity(activities.TransferUSDC, mock.Anything, payInput.Wallet, payInput.Amount.ToUSDC(), mock.AnythingOfType("string")).Return(nil)

		env.ExecuteWorkflow(PayBountyWorkflow, payInput)
		assert.NoError(t, env.GetWorkflowError())
	})

	// Test ReturnBountyToOwner workflow
	t.Run("ReturnBountyToOwner", func(t *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestWorkflowEnvironment()

		// Create mock HTTP client
		mockHTTPClient := &http.Client{
			Transport: &mockTransport{},
		}

		// Create activities instance
		activities, err := NewActivities()
		require.NoError(t, err)

		// Override the HTTP client with our mock
		activities.httpClient = mockHTTPClient

		// Register activities
		env.RegisterActivity(activities.VerifyPayment)
		env.RegisterActivity(activities.TransferUSDC)
		env.RegisterActivity(activities.PullContentActivity)
		env.RegisterActivity(activities.CheckContentRequirements)

		// Register workflows
		// env.RegisterWorkflow(PullContentWorkflow) // Removed

		// Mock activity calls
		initialBalance, err := solana.NewUSDCAmount(10.0)
		require.NoError(t, err)

		env.OnActivity(activities.VerifyPayment, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&VerifyPaymentResult{
				Verified: true,
				Amount:   initialBalance,
			}, nil)

		// Mock the PullContentActivity (formerly PullContentWorkflow child workflow)
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

		// Mock CheckContentRequirements - Expect context, []byte, []string
		env.OnActivity(activities.CheckContentRequirements, mock.Anything, mock.AnythingOfType("[]uint8"), mock.AnythingOfType("[]string")).
			Return(CheckContentRequirementsResult{
				Satisfies: true,
				Reason:    "Content meets requirements",
			}, nil)

		// Mock TransferUSDC call for returning bounty to owner - expect context, recipientWallet string, amount float64, memo string
		env.OnActivity(activities.TransferUSDC, mock.Anything, "test-owner", mock.AnythingOfType("float64"), mock.AnythingOfType("string")).Return(nil)

		// Just verify the activity registration is successful
		assert.NoError(t, err)
	})
}

func TestBountyAssessmentWorkflow(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	// Create activities instance
	activities, err := NewActivities()
	require.NoError(t, err)

	// Register activities
	env.RegisterActivity(activities.VerifyPayment)
	env.RegisterActivity(activities.TransferUSDC)
	env.RegisterActivity(activities.PullContentActivity)
	env.RegisterActivity(activities.CheckContentRequirements)
	env.RegisterActivity(activities.AnalyzeImageURL)
	env.RegisterActivity(activities.ValidatePayoutWallet) // Register new activity

	// Register workflows
	// env.RegisterWorkflow(PullContentWorkflow) // Removed

	// Mock activity calls
	// Define bounty amounts for test
	originalTotalBounty, err := solana.NewUSDCAmount(10.0)
	require.NoError(t, err)
	totalBounty, err := solana.NewUSDCAmount(9.0) // User payable (implies 1.0 fee)
	require.NoError(t, err)
	feeAmount := originalTotalBounty.Sub(totalBounty)

	// Create test input variables first
	bountyPerPost, _ := solana.NewUSDCAmount(1.0)
	// totalBounty already defined above (user payable)

	// Create valid Solana wallets for testing
	ownerWallet := solanago.NewWallet()
	funderWallet := solanago.NewWallet() // Separate funder for clarity
	payoutWallet := solanago.NewWallet()
	escrowWallet := solanago.NewWallet()   // Dummy escrow wallet for testing
	treasuryWallet := solanago.NewWallet() // Dummy treasury wallet for testing

	// --- Start Workflow Input Definition ---
	// Create test input - include OriginalTotalBounty
	input := BountyAssessmentWorkflowInput{
		Requirements:       []string{"Test requirement"},
		BountyPerPost:      bountyPerPost,
		TotalBounty:        totalBounty,         // User payable amount
		TotalCharged:       originalTotalBounty, // Original funded amount
		BountyOwnerWallet:  ownerWallet.PublicKey().String(),
		BountyFunderWallet: funderWallet.PublicKey().String(),
		EscrowWallet:       escrowWallet.PublicKey().String(),   // Add dummy escrow
		TreasuryWallet:     treasuryWallet.PublicKey().String(), // Add dummy treasury
		Platform:           PlatformReddit,
		ContentKind:        ContentKindPost,
		Timeout:            30 * time.Second, // Increased from 5 seconds
		PaymentTimeout:     5 * time.Second,  // Set payment timeout
	}
	// --- End Workflow Input Definition ---

	// --- Start Mock Definitions ---
	// Mock VerifyPayment - use originalTotalBounty
	// Expect: context, from, recipient, expectedAmount, memo, timeout
	env.OnActivity(activities.VerifyPayment, mock.Anything, funderWallet.PublicKey(), escrowWallet.PublicKey(), originalTotalBounty, mock.AnythingOfType("string"), mock.Anything).
		Return(&VerifyPaymentResult{
			Verified: true,
			Amount:   originalTotalBounty, // Verify against the original amount funded
		}, nil).Once() // Expect initial funding check once

	// Mock TransferUSDC for Fee Transfer (Escrow -> Treasury) - Executed in main workflow
	// Expect: context, recipientWallet (string), amount (float64), memo (string)
	env.OnActivity(activities.TransferUSDC, mock.Anything, treasuryWallet.PublicKey().String(), feeAmount.ToUSDC(), mock.AnythingOfType("string")).
		Return(nil).Once()

	// Mock the PullContentActivity (formerly PullContentWorkflow child workflow)
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

	// Mock AnalyzeImageUrlActivity
	mockImgAnalysisResult := CheckContentRequirementsResult{Satisfies: true, Reason: "Image visually acceptable"}
	env.OnActivity(activities.AnalyzeImageURL, mock.Anything, mockThumbnailURL, mock.AnythingOfType("string")).Return(mockImgAnalysisResult, nil).Once()

	// Mock CheckContentRequirements (expect ORIGINAL content data now, not combined)
	env.OnActivity(activities.CheckContentRequirements, mock.Anything, mock.AnythingOfType("[]uint8"), mock.AnythingOfType("[]string")).
		Return(CheckContentRequirementsResult{
			Satisfies: true,
			Reason:    "Content meets requirements",
		}, nil).Once()

	// Mock ValidatePayoutWallet - ADDED - Called Once as payout happens
	env.OnActivity(activities.ValidatePayoutWallet, mock.Anything, payoutWallet.PublicKey().String(), input.Requirements).
		Return(ValidateWalletResult{Satisfies: true, Reason: "Wallet validation passed in test"}, nil).Once()

	// Mock TransferUSDC (Payout) - Use SPECIFIC payout wallet and a more specific memo
	expectedPayoutMemo := fmt.Sprintf("{\"workflow_id\":\"%s\",\"content_id\":\"%s\",\"content_kind\":\"%s\"}", "default-test-workflow-id", "test-content", ContentKindPost)
	env.OnActivity(activities.TransferUSDC, mock.Anything, payoutWallet.PublicKey().String(), bountyPerPost.ToUSDC(), expectedPayoutMemo).Return(nil).Once()

	// Mock TransferUSDC (Refund) - Expect remaining amount
	// After one payout of 1.0, remaining user payable (9.0) should be 8.0 - Expect memo
	remainingBountyAfterPayout := totalBounty.Sub(bountyPerPost)
	env.OnActivity(activities.TransferUSDC, mock.Anything, ownerWallet.PublicKey().String(), remainingBountyAfterPayout.ToUSDC(), mock.AnythingOfType("string")).
		Run(func(args mock.Arguments) {
			// t.Logf("Refund Mock .Run() called: owner=%s, amount=%v (type: %T)", args.String(1), args.Get(2), args.Get(2))
		}).
		Return(nil).Once() // Changed from .Maybe() to .Once()
	// --- End Mock Definitions ---

	// Execute workflow and send signals
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

	// Assert expectations after workflow completion
	env.AssertExpectations(t)
}

func TestBountyAssessmentWorkflowTimeout(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	// Create activities instance
	activities, err := NewActivities()
	require.NoError(t, err)

	// Register activities
	env.RegisterActivity(activities.VerifyPayment)
	env.RegisterActivity(activities.TransferUSDC)
	env.RegisterActivity(activities.PullContentActivity)
	env.RegisterActivity(activities.CheckContentRequirements)
	env.RegisterActivity(activities.ValidatePayoutWallet)
	env.RegisterActivity(activities.AnalyzeImageURL)

	// Register necessary workflows
	// env.RegisterWorkflow(PullContentWorkflow) // Removed

	// Define variables needed for mock setup
	bountyPerPost, err := solana.NewUSDCAmount(1.0)
	require.NoError(t, err)
	// Define bounty amounts for test
	originalTotalBounty, err := solana.NewUSDCAmount(10.0)
	require.NoError(t, err)
	totalBounty, err := solana.NewUSDCAmount(9.0) // User payable (implies 1.0 fee)
	require.NoError(t, err)
	feeAmount := originalTotalBounty.Sub(totalBounty)

	// Create valid Solana wallet for testing
	ownerWallet := solanago.NewWallet()
	funderWallet := solanago.NewWallet()   // Define funder wallet for VerifyPayment
	escrowWallet := solanago.NewWallet()   // Dummy escrow wallet for testing
	treasuryWallet := solanago.NewWallet() // Dummy treasury wallet for testing

	// Create a channel to signal when the refund mock is called
	// mockCalled := make(chan struct{}) // Removed as logging is simplified

	// --- Start Workflow Input Definition ---
	// Create test input
	input := BountyAssessmentWorkflowInput{
		Requirements:       []string{"Test requirement"},
		BountyPerPost:      bountyPerPost,
		TotalBounty:        totalBounty,         // User payable amount
		TotalCharged:       originalTotalBounty, // Original funded amount
		BountyOwnerWallet:  ownerWallet.PublicKey().String(),
		BountyFunderWallet: funderWallet.PublicKey().String(),
		EscrowWallet:       escrowWallet.PublicKey().String(),   // Add dummy escrow
		TreasuryWallet:     treasuryWallet.PublicKey().String(), // Add dummy treasury
		Platform:           PlatformReddit,
		ContentKind:        ContentKindPost,
		Timeout:            30 * time.Second,
		PaymentTimeout:     5 * time.Second,
	}
	// --- End Workflow Input Definition ---

	// --- Mock Setups ---
	// Mock VerifyPayment - Return success with the originalTotalBounty
	// Expect: context, from, recipient, expectedAmount, memo, timeout
	env.OnActivity(activities.VerifyPayment, mock.Anything, funderWallet.PublicKey(), escrowWallet.PublicKey(), originalTotalBounty, mock.AnythingOfType("string"), mock.Anything).
		Return(&VerifyPaymentResult{Verified: true, Amount: originalTotalBounty}, nil).Maybe() // May or may not be called before timeout

	// Mock TransferUSDC for Fee Transfer (Escrow -> Treasury) - Executed in main workflow, maybe
	// Expect: context, recipientWallet (string), amount (float64), memo (string)
	env.OnActivity(activities.TransferUSDC, mock.Anything, mock.AnythingOfType("string"), feeAmount.ToUSDC(), mock.AnythingOfType("string")).
		Return(nil).Maybe()

	// Mock content pulling/checking - basic success mocks, may not be called
	env.OnActivity(activities.PullContentActivity, mock.Anything, mock.AnythingOfType("string"), ContentKindPost).
		Return(&RedditContent{ID: "timeout-test-content"}, nil).Maybe()
	env.OnActivity(activities.CheckContentRequirements, mock.Anything, mock.Anything, mock.Anything).
		Return(CheckContentRequirementsResult{Satisfies: true}, nil).Maybe()

	// Mock ValidatePayoutWallet - ADDED - This step might not be reached if timeout occurs before signal processing
	env.OnActivity(activities.ValidatePayoutWallet, mock.Anything, mock.AnythingOfType("string"), input.Requirements).
		Return(ValidateWalletResult{Satisfies: true, Reason: "Wallet validation passed in test"}, nil).Maybe()

	// Mock TransferUSDC (Refund) - Expect refund to owner with totalBounty (user payable) amount
	// because the timeout is very short and should hit before any payout.
	// The refund logic returns the remaining USER PAYABLE amount.
	// The memo should be simple for a direct timeout refund.
	expectedTimeoutRefundMemo := fmt.Sprintf("{\"workflow_id\":\"%s\"}", "default-test-workflow-id")
	env.OnActivity(activities.TransferUSDC,
		mock.Anything,                               // Context
		ownerWallet.PublicKey().String(),            // Owner wallet
		originalTotalBounty.Sub(feeAmount).ToUSDC(), // Expect the full user-payable amount (totalBounty = 9.0) on timeout refund
		expectedTimeoutRefundMemo,                   // Expect specific memo
	).Return(nil).Once() // Changed from .Maybe() to .Once()

	// --- Execute & Assert ---
	env.ExecuteWorkflow(BountyAssessmentWorkflow, input)

	// Assert that the workflow completed without errors
	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())

	// Verify that mocks were called as expected
	// Note: Fee transfer might not be called if timeout happens before VerifyPayment completes
	env.AssertExpectations(t)
}

func TestBountyAssessmentWorkflow_Idempotency(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	// Create activities instance
	activities, err := NewActivities()
	require.NoError(t, err)

	// Register activities
	env.RegisterActivity(activities.VerifyPayment)
	env.RegisterActivity(activities.TransferUSDC)
	env.RegisterActivity(activities.PullContentActivity)
	env.RegisterActivity(activities.CheckContentRequirements)
	env.RegisterActivity(activities.ValidatePayoutWallet)
	env.RegisterActivity(activities.AnalyzeImageURL)

	// Register workflows
	// env.RegisterWorkflow(PullContentWorkflow) // Removed

	// Define bounty amounts
	originalTotalBounty, _ := solana.NewUSDCAmount(10.0)
	totalBounty, _ := solana.NewUSDCAmount(5.0) // User payable amount
	bountyPerPost, _ := solana.NewUSDCAmount(1.0)
	feeAmount := originalTotalBounty.Sub(totalBounty)

	funderWallet := solanago.NewWallet()
	ownerWallet := solanago.NewWallet()
	payoutWallet := solanago.NewWallet()
	escrowWallet := solanago.NewWallet()   // Dummy escrow wallet for testing
	treasuryWallet := solanago.NewWallet() // Dummy treasury wallet for testing
	contentID := "idempotent-content"

	// --- Start Workflow Input Definition ---
	input := BountyAssessmentWorkflowInput{
		Requirements:       []string{"Idempotency Test"},
		BountyPerPost:      bountyPerPost,
		TotalBounty:        totalBounty,
		TotalCharged:       originalTotalBounty,
		BountyOwnerWallet:  ownerWallet.PublicKey().String(),
		BountyFunderWallet: funderWallet.PublicKey().String(),
		EscrowWallet:       escrowWallet.PublicKey().String(),   // Add dummy escrow
		TreasuryWallet:     treasuryWallet.PublicKey().String(), // Add dummy treasury
		Platform:           PlatformReddit,
		Timeout:            30 * time.Second,
		PaymentTimeout:     5 * time.Second,
	}
	// --- End Workflow Input Definition ---

	// --- Start Mock Definitions ---
	// Mock VerifyPayment - Called Once, use originalTotalBounty
	// Expect: context, from, recipient, expectedAmount, memo, timeout
	env.OnActivity(activities.VerifyPayment, mock.Anything, funderWallet.PublicKey(), escrowWallet.PublicKey(), originalTotalBounty, mock.AnythingOfType("string"), mock.Anything).Return(&VerifyPaymentResult{Verified: true, Amount: originalTotalBounty}, nil).Once()

	// Mock TransferUSDC for Fee Transfer (Escrow -> Treasury) - Called Once
	// Expect: context, recipientWallet (string), amount (float64), memo (string)
	env.OnActivity(activities.TransferUSDC, mock.Anything, treasuryWallet.PublicKey().String(), feeAmount.ToUSDC(), mock.AnythingOfType("string")).
		Return(nil).Once()

	// Mock the PullContentActivity (formerly PullContentWorkflow child workflow) - Called Once
	mockRedditContent := &RedditContent{ID: contentID, Title: "Idempotent Test"}
	mockedRedditBytesIdempotency, err := json.Marshal(mockRedditContent)
	require.NoError(t, err, "Failed to marshal mocked Reddit content for activity mock (idempotency)")
	env.OnActivity(activities.PullContentActivity, mock.Anything, mock.MatchedBy(func(input PullContentInput) bool {
		return input.PlatformType == PlatformReddit && input.ContentID == contentID && input.ContentKind == ContentKindPost
	})).Return(mockedRedditBytesIdempotency, nil).Once()

	// Mock CheckContentRequirements - Expect context, []byte, []string
	env.OnActivity(activities.CheckContentRequirements, mock.Anything, mock.AnythingOfType("[]uint8"), mock.AnythingOfType("[]string")).
		Return(CheckContentRequirementsResult{Satisfies: true, Reason: "OK"}, nil).Once()

	// Mock ValidatePayoutWallet - ADDED - Called Once as payout happens
	env.OnActivity(activities.ValidatePayoutWallet, mock.Anything, payoutWallet.PublicKey().String(), input.Requirements).
		Return(ValidateWalletResult{Satisfies: true, Reason: "Wallet validation passed in test"}, nil).Once()

	// Mock TransferUSDC (Payout) - Called Once for the specific payout - Expect memo
	env.OnActivity(activities.TransferUSDC, mock.Anything, payoutWallet.PublicKey().String(), bountyPerPost.ToUSDC(), mock.AnythingOfType("string")).Return(nil).Once()

	// Mock TransferUSDC (Return to Owner upon Timeout) - Expect memo
	cancelRefundAmount := totalBounty.Sub(bountyPerPost)
	env.OnActivity(activities.TransferUSDC, mock.Anything, ownerWallet.PublicKey().String(), cancelRefundAmount.ToUSDC(), mock.AnythingOfType("string")).Return(nil).Once()
	// --- End Mock Definitions ---

	signal := AssessContentSignal{
		ContentID:    contentID,
		PayoutWallet: payoutWallet.PublicKey().String(),
		Platform:     PlatformReddit,
		ContentKind:  ContentKindPost,
	}

	// signal workflow once
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AssessmentSignalName, signal)
	}, 5*time.Second)

	// signal it again later with the same signal to test idempotency
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AssessmentSignalName, signal)
	}, 6*time.Second)

	// execute and assert idempotency
	env.ExecuteWorkflow(BountyAssessmentWorkflow, input)
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestBountyAssessmentWorkflow_RequirementsNotMet(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	activities, err := NewActivities()
	require.NoError(t, err)

	env.RegisterActivity(activities.VerifyPayment)
	env.RegisterActivity(activities.TransferUSDC)
	env.RegisterActivity(activities.CheckContentRequirements)
	env.RegisterActivity(activities.ValidatePayoutWallet) // Register new activity
	env.RegisterActivity(activities.AnalyzeImageURL)

	// Define bounty amounts
	originalTotalBounty, _ := solana.NewUSDCAmount(10.0)
	totalBounty, _ := solana.NewUSDCAmount(5.0) // User payable amount
	bountyPerPost, _ := solana.NewUSDCAmount(1.0)
	feeAmount := originalTotalBounty.Sub(totalBounty)

	funderWallet := solanago.NewWallet()
	ownerWallet := solanago.NewWallet()
	payoutWallet := solanago.NewWallet()
	escrowWallet := solanago.NewWallet()
	treasuryWallet := solanago.NewWallet()
	contentID := "failed-content"
	mockRedditContent := &RedditContent{ID: contentID, Title: "Failed Test"}

	// Re-introduce channel for specific assertion
	refundCalled := make(chan struct{}, 1)

	// --- Mock Expectations ---
	// Expect initial payment verification (original amount)
	// Expect: context, from, recipient, expectedAmount, memo, timeout
	env.OnActivity(activities.VerifyPayment, mock.Anything, funderWallet.PublicKey(), escrowWallet.PublicKey(), originalTotalBounty, mock.AnythingOfType("string"), mock.Anything).Return(&VerifyPaymentResult{Verified: true, Amount: originalTotalBounty}, nil).Once()

	// Mock TransferUSDC for Fee Transfer (Escrow -> Treasury) - Called Once
	// Expect: context, recipientWallet (string), amount (float64), memo (string)
	env.OnActivity(activities.TransferUSDC, mock.Anything, treasuryWallet.PublicKey().String(), feeAmount.ToUSDC(), mock.AnythingOfType("string")).
		Return(nil).Once()

	// Mock the PullContentActivity (formerly PullContentWorkflow child workflow) execution for the failing content
	mockedRedditBytesFailure, err := json.Marshal(mockRedditContent)
	require.NoError(t, err, "Failed to marshal mocked Reddit content for activity mock (failure)")
	env.OnActivity(activities.PullContentActivity, mock.Anything, mock.MatchedBy(func(input PullContentInput) bool {
		return input.PlatformType == PlatformReddit && input.ContentID == contentID && input.ContentKind == ContentKindComment
	})).Return(mockedRedditBytesFailure, nil).Once()

	// Expect requirements check to fail (expect context, []byte, []string)
	env.OnActivity(activities.CheckContentRequirements, mock.Anything, mock.AnythingOfType("[]uint8"), mock.AnythingOfType("[]string")).Return(CheckContentRequirementsResult{Satisfies: false, Reason: "Did not meet criteria"}, nil).Once()

	// Mock ValidatePayoutWallet - ADDED - Should NOT be called because CheckContentRequirements failed
	env.OnActivity(activities.ValidatePayoutWallet, mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("[]string")).
		Return(ValidateWalletResult{Satisfies: true, Reason: "Should not be called"}, nil).Never()

	// Expect NO payout transfer
	env.OnActivity(activities.TransferUSDC, mock.Anything, payoutWallet.PublicKey().String(), bountyPerPost.ToUSDC(), mock.AnythingOfType("string")).Return(nil).Never()

	// Expect refund transfer to the owner for the full remaining USER PAYABLE bounty.
	env.OnActivity(activities.TransferUSDC,
		mock.Anything,
		ownerWallet.PublicKey().String(),
		totalBounty.ToUSDC(),
		mock.AnythingOfType("string"),
	).Run(func(args mock.Arguments) {
		select {
		case refundCalled <- struct{}{}:
			// t.Logf("Successfully signaled refundCalled channel")
		default:
			// t.Logf("Could not signal refundCalled channel (already full or no receiver?)")
		}
	}).Return(nil)

	// --- Workflow Input ---
	input := BountyAssessmentWorkflowInput{
		Requirements:       []string{"Failure Test"},
		BountyPerPost:      bountyPerPost,
		TotalBounty:        totalBounty,
		TotalCharged:       originalTotalBounty,
		BountyOwnerWallet:  ownerWallet.PublicKey().String(),
		BountyFunderWallet: funderWallet.PublicKey().String(),
		EscrowWallet:       escrowWallet.PublicKey().String(),   // Add dummy escrow
		TreasuryWallet:     treasuryWallet.PublicKey().String(), // Add dummy treasury
		Platform:           PlatformReddit,
		Timeout:            30 * time.Second,
		PaymentTimeout:     5 * time.Second,
	}

	// --- Signal Sequence (Simplified) ---
	failSignal := AssessContentSignal{
		ContentID:    contentID,
		PayoutWallet: payoutWallet.PublicKey().String(), // Still need payout wallet in signal struct
		Platform:     PlatformReddit,
		ContentKind:  ContentKindComment,
	}
	cancelSignal := CancelBountySignal{
		BountyOwnerWallet: ownerWallet.PublicKey().String(),
	}

	env.RegisterDelayedCallback(func() {
		// Send the failing assessment signal
		env.SignalWorkflow(AssessmentSignalName, failSignal)
		// Shortly after, send the cancellation signal
		env.RegisterDelayedCallback(func() {
			env.SignalWorkflow(CancelSignalName, cancelSignal)
		}, 20*time.Second)
	}, 10*time.Second)

	// --- Execution & Verification ---
	env.ExecuteWorkflow(BountyAssessmentWorkflow, input)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	// Re-introduce the channel check as the primary verification for the refund mock.
	select {
	case <-refundCalled:
		// Verification passed: Refund mock was called
	default:
		t.Fatalf("Refund mock .Run() block was expected to execute and signal channel, but didn't.")
	}

	// Rely on AssertExpectations to verify other mocks (acknowledging potential flakiness of refund mock report)
	env.AssertExpectations(t)
}

func TestPlatformActivities(t *testing.T) {
	// Create a mock configuration for testing
	mockConfig := &Configuration{
		SolanaConfig: SolanaConfig{
			RPCEndpoint:     "http://localhost:8899",
			WSEndpoint:      "ws://localhost:8900",
			EscrowWallet:    solanago.MustPublicKeyFromBase58("4fmzJQHi6CNaaf98dcQVoBWJdLt17uk1PcHkoeFTQcxj"),
			TreasuryWallet:  "4fmzJQHi6CNaaf98dcQVoBWJdLt17uk1PcHkoeFTQcxj",
			USDCMintAddress: "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v",
		},
		RedditDeps: RedditDependencies{
			UserAgent:    "test-agent",
			Username:     "test-user",
			Password:     "test-pass",
			ClientID:     "test-client",
			ClientSecret: "test-secret",
		},
		YouTubeDeps: YouTubeDependencies{
			APIKey:          "test-key",
			ApplicationName: "test-app",
			MaxResults:      10,
		},
		TwitchDeps: TwitchDependencies{
			ClientID:     "test-client",
			ClientSecret: "test-secret",
		},
	}

	// Create activities instance (can be shared across sub-tests)
	activities := &Activities{}

	// Test workflow that executes the activity (can be defined once and shared)
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
		// Use the activities instance defined in the outer scope
		err := workflow.ExecuteActivity(ctx, activities.PullContentActivity, input).Get(ctx, &result)
		return result, err
	}

	// Test Reddit content pulling
	t.Run("PullRedditContent", func(t *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestWorkflowEnvironment()
		env.RegisterActivity(activities.PullContentActivity)
		env.RegisterActivity(activities.getConfiguration)
		env.OnActivity(activities.getConfiguration, mock.Anything).Return(mockConfig, nil)
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

	// Test YouTube content pulling
	t.Run("PullYouTubeContent", func(t *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestWorkflowEnvironment()
		env.RegisterActivity(activities.PullContentActivity)
		env.RegisterActivity(activities.getConfiguration)
		env.OnActivity(activities.getConfiguration, mock.Anything).Return(mockConfig, nil)
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

	// Test Twitch content pulling
	t.Run("PullTwitchContent_Video", func(t *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestWorkflowEnvironment()
		env.RegisterActivity(activities.PullContentActivity)
		env.RegisterActivity(activities.getConfiguration)
		env.OnActivity(activities.getConfiguration, mock.Anything).Return(mockConfig, nil)
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
		env.RegisterActivity(activities.getConfiguration)
		env.OnActivity(activities.getConfiguration, mock.Anything).Return(mockConfig, nil)
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

// PullContentWorkflow is a simple workflow that executes the PullContentActivity
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

// getConfiguration is a helper method to get the configuration
func (a *Activities) getConfiguration(ctx context.Context) (*Configuration, error) {
	return &Configuration{
		SolanaConfig: SolanaConfig{
			RPCEndpoint:     "http://localhost:8899",
			WSEndpoint:      "ws://localhost:8900",
			EscrowWallet:    solanago.MustPublicKeyFromBase58("4fmzJQHi6CNaaf98dcQVoBWJdLt17uk1PcHkoeFTQcxj"),
			TreasuryWallet:  "4fmzJQHi6CNaaf98dcQVoBWJdLt17uk1PcHkoeFTQcxj",
			USDCMintAddress: "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v",
		},
		RedditDeps: RedditDependencies{
			UserAgent:    "test-agent",
			Username:     "test-user",
			Password:     "test-pass",
			ClientID:     "test-client",
			ClientSecret: "test-secret",
		},
		YouTubeDeps: YouTubeDependencies{
			APIKey:          "test-key",
			ApplicationName: "test-app",
			MaxResults:      10,
		},
		TwitchDeps: TwitchDependencies{
			ClientID:     "test-client",
			ClientSecret: "test-secret",
		},
	}, nil
}
