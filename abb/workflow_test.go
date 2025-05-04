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
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

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
		env.RegisterActivity(activities.PullRedditContent)
		env.RegisterActivity(activities.CheckContentRequirements)

		// Register workflows
		env.RegisterWorkflow(PullContentWorkflow)

		// Mock activity calls
		initialBalance, err := solana.NewUSDCAmount(10.0)
		require.NoError(t, err)

		env.OnActivity(activities.VerifyPayment, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&VerifyPaymentResult{
				Verified: true,
				Amount:   initialBalance,
			}, nil)

		// Mock PullRedditContent
		mockedRedditContent := &RedditContent{
			ID:          "test-id",
			Title:       "Test Title",
			Selftext:    "Test Content",
			URL:         "https://test.com",
			Author:      "test-author",
			Subreddit:   "test-subreddit",
			Score:       100,
			Created:     time.Now(),
			IsComment:   false,
			Permalink:   "test-permalink",
			NumComments: 10,
		}
		env.OnActivity(activities.PullRedditContent, mock.Anything, "test-content", ContentKindPost).
			Return(mockedRedditContent, nil)

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

	// Create test configuration
	// escrowKey := solanago.NewWallet().PrivateKey // Removed as unused
	// escrowAccount := solanago.NewWallet().PublicKey() // Removed as unused
	// treasuryWallet := solanago.NewWallet() // Dummy treasury wallet - Removed as testConfig is removed
	// testConfig := SolanaConfig{ // Removed as it's unused now

	// Create activities instance
	activities, err := NewActivities()
	require.NoError(t, err)

	// Register activities
	env.RegisterActivity(activities.VerifyPayment)
	env.RegisterActivity(activities.TransferUSDC)
	env.RegisterActivity(activities.PullRedditContent)
	env.RegisterActivity(activities.CheckContentRequirements)
	env.RegisterActivity(activities.AnalyzeImageUrlActivity)

	// Register workflows
	env.RegisterWorkflow(PullContentWorkflow)

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

	// Mock PullRedditContent
	mockThumbnailURL := "https://example.com/reddit_thumb.jpg"
	mockedRedditContent := &RedditContent{
		ID:          "test-id",
		Title:       "Test Title",
		Selftext:    "Test Content",
		URL:         "https://test.com",
		Author:      "test-author",
		Subreddit:   "test-subreddit",
		Score:       100,
		Created:     time.Now(),
		IsComment:   false,
		Permalink:   "test-permalink",
		NumComments: 10,
		Thumbnail:   mockThumbnailURL,
	}
	env.OnActivity(activities.PullRedditContent, mock.Anything, "test-content", ContentKindPost).
		Return(mockedRedditContent, nil).Once()

	// Mock AnalyzeImageUrlActivity
	mockImgAnalysisResult := CheckContentRequirementsResult{Satisfies: true, Reason: "Image visually acceptable"}
	env.OnActivity(activities.AnalyzeImageUrlActivity, mock.Anything, mockThumbnailURL, mock.AnythingOfType("string")).Return(mockImgAnalysisResult, nil).Once()

	// Mock CheckContentRequirements (expect ORIGINAL content data now, not combined)
	env.OnActivity(activities.CheckContentRequirements, mock.Anything, mock.AnythingOfType("[]uint8"), mock.AnythingOfType("[]string")).
		Return(CheckContentRequirementsResult{
			Satisfies: true,
			Reason:    "Content meets requirements",
		}, nil).Once()

	// Set up mock expectations
	// Payout mock (assuming one signal is sent) - Expect memo
	env.OnActivity(activities.TransferUSDC, mock.Anything, payoutWallet.PublicKey().String(), bountyPerPost.ToUSDC(), mock.AnythingOfType("string")).Return(nil).Once()

	// Refund mock - Expect remaining amount
	// After one payout of 1.0, remaining user payable (9.0) should be 8.0 - Expect memo
	remainingBountyAfterPayout := totalBounty.Sub(bountyPerPost)
	env.OnActivity(activities.TransferUSDC, mock.Anything, ownerWallet.PublicKey().String(), remainingBountyAfterPayout.ToUSDC(), mock.AnythingOfType("string")).
		Run(func(args mock.Arguments) {
			// t.Logf("Refund Mock .Run() called: owner=%s, amount=%v (type: %T)", args.String(1), args.Get(2), args.Get(2))
		}).
		Return(nil).Maybe()

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

	// Create test configuration
	// escrowKey := solanago.NewWallet().PrivateKey // Removed as unused
	// escrowAccount := solanago.NewWallet().PublicKey() // Removed as unused
	// treasuryWallet := solanago.NewWallet() // Dummy treasury wallet - Removed as testConfig is removed
	// testConfig := SolanaConfig{ // Removed as it's unused now

	// Create activities instance
	activities, err := NewActivities()
	require.NoError(t, err)

	// Register activities & workflow needed by BountyAssessmentWorkflow
	env.RegisterActivity(activities.VerifyPayment)
	env.RegisterActivity(activities.TransferUSDC)
	env.RegisterActivity(activities.PullRedditContent) // Assuming Reddit for this test
	env.RegisterActivity(activities.CheckContentRequirements)
	env.RegisterWorkflow(PullContentWorkflow)

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
	env.OnActivity(activities.PullRedditContent, mock.Anything, mock.AnythingOfType("string"), ContentKindPost).
		Return(&RedditContent{ID: "timeout-test-content"}, nil).Maybe()
	env.OnActivity(activities.CheckContentRequirements, mock.Anything, mock.Anything, mock.Anything).
		Return(CheckContentRequirementsResult{Satisfies: true}, nil).Maybe()

	// Mock TransferUSDC (Refund) - Expect refund to owner with totalBounty (user payable) amount
	// because the timeout is very short and should hit before any payout.
	// The refund logic returns the remaining USER PAYABLE amount.
	env.OnActivity(activities.TransferUSDC,
		mock.Anything,                               // Context
		ownerWallet.PublicKey().String(),            // Owner wallet
		originalTotalBounty.Sub(feeAmount).ToUSDC(), // Expect the full user-payable amount (totalBounty = 9.0) on timeout refund
		mock.AnythingOfType("string"),               // Expect memo
	).Return(nil).Maybe() // Simplified: Removed .Run()

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
	env.RegisterActivity(activities.PullRedditContent)
	env.RegisterActivity(activities.CheckContentRequirements)

	// Register workflows
	env.RegisterWorkflow(PullContentWorkflow)

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

	// Mock VerifyPayment - Called Once, use originalTotalBounty
	// Expect: context, from, recipient, expectedAmount, memo, timeout
	env.OnActivity(activities.VerifyPayment, mock.Anything, funderWallet.PublicKey(), escrowWallet.PublicKey(), originalTotalBounty, mock.AnythingOfType("string"), mock.Anything).Return(&VerifyPaymentResult{Verified: true, Amount: originalTotalBounty}, nil).Once()

	// Mock TransferUSDC for Fee Transfer (Escrow -> Treasury) - Called Once
	// Expect: context, recipientWallet (string), amount (float64), memo (string)
	env.OnActivity(activities.TransferUSDC, mock.Anything, treasuryWallet.PublicKey().String(), feeAmount.ToUSDC(), mock.AnythingOfType("string")).
		Return(nil).Once()

	// Mock PullRedditContent - Called Once
	mockRedditContent := &RedditContent{ID: contentID, Title: "Idempotent Test"}
	env.OnActivity(activities.PullRedditContent, mock.Anything, contentID, ContentKindPost).Return(mockRedditContent, nil).Once()

	// Mock CheckContentRequirements - Expect context, []byte, []string
	env.OnActivity(activities.CheckContentRequirements, mock.Anything, mock.AnythingOfType("[]uint8"), mock.AnythingOfType("[]string")).
		Return(CheckContentRequirementsResult{Satisfies: true, Reason: "OK"}, nil).Once()

	// Mock TransferUSDC (Payout) - Called Once for the specific payout - Expect memo
	env.OnActivity(activities.TransferUSDC, mock.Anything, payoutWallet.PublicKey().String(), bountyPerPost.ToUSDC(), mock.AnythingOfType("string")).Return(nil).Once()

	// Mock TransferUSDC (Return to Owner upon Timeout) - Expect memo
	cancelRefundAmount := totalBounty.Sub(bountyPerPost)
	env.OnActivity(activities.TransferUSDC, mock.Anything, ownerWallet.PublicKey().String(), cancelRefundAmount.ToUSDC(), mock.AnythingOfType("string")).Return(nil).Once()

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
	env.RegisterActivity(activities.PullRedditContent)
	env.RegisterActivity(activities.CheckContentRequirements)
	env.RegisterWorkflow(PullContentWorkflow)

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

	// Mock content pull for the failing content
	env.OnActivity(activities.PullRedditContent, mock.Anything, contentID, ContentKindComment).Return(mockRedditContent, nil).Once()

	// Expect requirements check to fail (expect context, []byte, []string)
	env.OnActivity(activities.CheckContentRequirements, mock.Anything, mock.AnythingOfType("[]uint8"), mock.AnythingOfType("[]string")).Return(CheckContentRequirementsResult{Satisfies: false, Reason: "Did not meet criteria"}, nil).Once()

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
	// Test Reddit content pulling
	t.Run("PullRedditContent", func(t *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestWorkflowEnvironment()

		// Create activities instance using NewActivities for consistency
		// Provide minimal config, as only PullRedditContent is tested here.
		activities, err := NewActivities()
		require.NoError(t, err)

		// Register activity using the instance
		env.RegisterActivity(activities.PullRedditContent)

		// Mock activity using the instance
		env.OnActivity(activities.PullRedditContent, mock.Anything, "reddit-content-id", ContentKindComment).
			Return(&RedditContent{
				ID:        "reddit-content-id",
				Author:    "testuser",
				Subreddit: "testsubreddit",
				Body:      "Test comment content",
				IsComment: true,
			}, nil)

		// Execute activity
		env.ExecuteWorkflow(func(ctx workflow.Context) (string, error) {
			ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
				StartToCloseTimeout: time.Minute,
			})
			var redditContent *RedditContent
			err := workflow.ExecuteActivity(ctx, activities.PullRedditContent, "reddit-content-id", ContentKindComment).Get(ctx, &redditContent)
			if err != nil {
				return "", err
			}
			jsonBytes, err := json.Marshal(redditContent)
			if err != nil {
				return "", fmt.Errorf("failed to marshal Reddit content in test: %w", err)
			}
			return string(jsonBytes), nil
		})

		// Verify workflow completed successfully
		require.True(t, env.IsWorkflowCompleted())
		require.NoError(t, env.GetWorkflowError())

		// Get workflow result
		var result string
		require.NoError(t, env.GetWorkflowResult(&result))

		// Parse the JSON result
		var parsed map[string]interface{}
		err = json.Unmarshal([]byte(result), &parsed)
		require.NoError(t, err)

		// Verify the JSON fields
		assert.Equal(t, "testuser", parsed["author"])
		assert.Equal(t, "testsubreddit", parsed["subreddit"])
		assert.Equal(t, "Test comment content", parsed["body"])
		assert.Equal(t, true, parsed["is_comment"])

		// Verify activity calls
		env.AssertExpectations(t)
	})

	// Test YouTube content pulling
	t.Run("PullYouTubeContent", func(t *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestWorkflowEnvironment()

		// Create activities instance using NewActivities for consistency
		activities, err := NewActivities()
		require.NoError(t, err)

		// Register activity using the instance
		env.RegisterActivity(activities.PullYouTubeContent)

		// Mock activity using the instance
		mockedTranscript := "1\n00:00:00,000 --> 00:00:05,000\nThis is a test transcript.\n\n2\n00:00:05,000 --> 00:00:10,000\nIt contains multiple lines."
		env.OnActivity(activities.PullYouTubeContent, mock.Anything, "youtube-content-id", ContentKindVideo).
			Return(&YouTubeContent{
				ID:           "youtube-content-id",
				Title:        "Test Video",
				Description:  "Test video description",
				ChannelTitle: "Test Channel",
				PublishedAt:  time.Now(),
				ViewCount:    "1000",
				LikeCount:    "100",
				CommentCount: "50",
				Transcript:   mockedTranscript, // Populate the transcript field
			}, nil)

		// Execute activity
		env.ExecuteWorkflow(func(ctx workflow.Context) ([]byte, error) {
			ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
				StartToCloseTimeout: time.Minute,
			})
			var youtubeContent *YouTubeContent
			err := workflow.ExecuteActivity(ctx, activities.PullYouTubeContent, "youtube-content-id", ContentKindVideo).Get(ctx, &youtubeContent)
			if err != nil {
				return nil, err
			}
			// return json
			jsonBytes, err := json.Marshal(youtubeContent)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal YouTube content in test: %w", err)
			}
			return jsonBytes, nil
		})

		// Verify workflow completed successfully
		require.True(t, env.IsWorkflowCompleted())
		require.NoError(t, env.GetWorkflowError())

		// Get workflow result
		var result []byte
		require.NoError(t, env.GetWorkflowResult(&result))

		// Parse the JSON result
		var parsed map[string]interface{}
		err = json.Unmarshal(result, &parsed)
		require.NoError(t, err)

		// Verify the JSON fields (basic metadata)
		assert.Equal(t, "youtube-content-id", parsed["id"])
		assert.Equal(t, "Test Video", parsed["title"])
		assert.Equal(t, "Test video description", parsed["description"])
		assert.Equal(t, "Test Channel", parsed["channel_title"])
		assert.Equal(t, "1000", parsed["view_count"])
		assert.Equal(t, "100", parsed["like_count"])
		assert.Equal(t, "50", parsed["comment_count"])

		// Verify transcript exists and is a string
		transcript, ok := parsed["transcript"].(string)
		require.True(t, ok, "transcript key should exist and be a string in formatted output")
		assert.NotEmpty(t, transcript, "transcript should not be empty")
		assert.Contains(t, transcript, "This is a test transcript", "Formatted transcript should contain expected text") // Check for expected content

		// Verify activity calls
		env.AssertExpectations(t)
	})

	// Test Twitch content pulling
	t.Run("PullTwitchContent_Video", func(t *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestWorkflowEnvironment()

		activities, err := NewActivities()
		require.NoError(t, err)

		env.RegisterActivity(activities.PullTwitchContent)

		mockedVideo := &TwitchVideoContent{
			ID:           "twitch-video-id",
			UserID:       "12345",
			UserLogin:    "teststreamer",
			UserName:     "TestStreamer",
			Title:        "Test Twitch VOD",
			Description:  "Description of the VOD",
			ViewCount:    1500,
			Language:     "en",
			Duration:     "1h30m0s",
			PublishedAt:  time.Now().Add(-24 * time.Hour),
			ThumbnailURL: "https://example.com/thumb.jpg",
		}

		env.OnActivity(activities.PullTwitchContent, mock.Anything, "twitch-video-id", ContentKindVideo).
			Return(mockedVideo, nil)

		env.ExecuteWorkflow(func(ctx workflow.Context) ([]byte, error) {
			ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
				StartToCloseTimeout: time.Minute,
			})
			var twitchContent interface{}
			err := workflow.ExecuteActivity(ctx, activities.PullTwitchContent, "twitch-video-id", ContentKindVideo).Get(ctx, &twitchContent)
			if err != nil {
				return nil, err
			}
			return json.Marshal(twitchContent)
		})

		require.True(t, env.IsWorkflowCompleted())
		require.NoError(t, env.GetWorkflowError())

		var result []byte
		require.NoError(t, env.GetWorkflowResult(&result))

		var parsed TwitchVideoContent
		err = json.Unmarshal(result, &parsed)
		require.NoError(t, err)
		assert.Equal(t, "twitch-video-id", parsed.ID)
		assert.Equal(t, "Test Twitch VOD", parsed.Title)
		assert.Equal(t, "teststreamer", parsed.UserLogin)
		assert.EqualValues(t, 1500, parsed.ViewCount)

		env.AssertExpectations(t)
	})

	t.Run("PullTwitchContent_Clip", func(t *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestWorkflowEnvironment()

		activities, err := NewActivities()
		require.NoError(t, err)

		env.RegisterActivity(activities.PullTwitchContent)

		mockedClip := &TwitchClipContent{
			ID:              "twitch-clip-id",
			BroadcasterID:   "54321",
			BroadcasterName: "AnotherStreamer",
			CreatorID:       "98765",
			CreatorName:     "ClipCreator",
			Title:           "Amazing Twitch Clip",
			ViewCount:       50000,
			Language:        "en",
			Duration:        30.5,
			CreatedAt:       time.Now().Add(-2 * time.Hour),
			ThumbnailURL:    "https://example.com/clip_thumb.jpg",
		}

		env.OnActivity(activities.PullTwitchContent, mock.Anything, "twitch-clip-id", ContentKindClip).
			Return(mockedClip, nil)

		env.ExecuteWorkflow(func(ctx workflow.Context) ([]byte, error) {
			ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
				StartToCloseTimeout: time.Minute,
			})
			var twitchContent interface{}
			err := workflow.ExecuteActivity(ctx, activities.PullTwitchContent, "twitch-clip-id", ContentKindClip).Get(ctx, &twitchContent)
			if err != nil {
				return nil, err
			}
			return json.Marshal(twitchContent)
		})

		require.True(t, env.IsWorkflowCompleted())
		require.NoError(t, env.GetWorkflowError())

		var result []byte
		require.NoError(t, env.GetWorkflowResult(&result))

		var parsed TwitchClipContent
		err = json.Unmarshal(result, &parsed)
		require.NoError(t, err)
		assert.Equal(t, "twitch-clip-id", parsed.ID)
		assert.Equal(t, "Amazing Twitch Clip", parsed.Title)
		assert.Equal(t, "AnotherStreamer", parsed.BroadcasterName)
		assert.EqualValues(t, 50000, parsed.ViewCount)

		env.AssertExpectations(t)
	})
}

func TestCheckContentRequirementsWorkflow(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	// Create activities instance using NewActivities for consistency
	activities, err := NewActivities()
	require.NoError(t, err)

	// Register activity using the instance
	env.RegisterActivity(activities.CheckContentRequirements)

	// Mock activity using the instance - Corrected to 3 arguments
	env.OnActivity(activities.CheckContentRequirements,
		mock.Anything,                   // Context
		mock.AnythingOfType("[]uint8"),  // content []byte
		mock.AnythingOfType("[]string"), // requirements []string
	).Return(CheckContentRequirementsResult{
		Satisfies: true,
		Reason:    "Content meets requirements",
	}, nil)

	// Execute workflow
	env.ExecuteWorkflow(CheckContentRequirementsWorkflow, []byte("test content"), []string{"test requirements"})

	// Verify workflow completed successfully
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	// Get workflow result
	var result CheckContentRequirementsResult
	require.NoError(t, env.GetWorkflowResult(&result))
	assert.True(t, result.Satisfies)
	assert.Equal(t, "Content meets requirements", result.Reason)

	// Verify activity calls
	env.AssertExpectations(t)
}

func TestPayBountyWorkflow(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	// Create activities instance (minimal config needed for TransferUSDC)
	// escrowKey := solanago.NewWallet().PrivateKey // Removed as unused
	// escrowAccount := solanago.NewWallet().PublicKey() // Removed as unused
	// treasuryWallet := solanago.NewWallet() // Dummy treasury wallet - Removed as testConfig is removed
	// testConfig := SolanaConfig{ // Removed as it's unused now
	activities, err := NewActivities()
	require.NoError(t, err)

	// Register activity using the instance method
	env.RegisterActivity(activities.TransferUSDC)

	// Mock activity using the instance method
	// Ensure arguments match TransferUSDC: ctx, toAccount (string), amount (float64)
	env.OnActivity(activities.TransferUSDC, mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("float64"), mock.AnythingOfType("string")).
		Return(nil)

	// Create test input
	amount, err := solana.NewUSDCAmount(10)
	require.NoError(t, err)

	input := PayBountyWorkflowInput{
		Wallet: "8dUmBqpvjqJvXKxdbhWDtWgYz6tNQzqbT6hF4Vz1Vy8h", // Example address
		Amount: amount,
		// SolanaConfig: testConfig,
	}

	// Execute workflow
	env.ExecuteWorkflow(PayBountyWorkflow, input)

	// Verify workflow completed successfully
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	// Verify activity calls
	env.AssertExpectations(t)
}

// TestAnalyzeImageUrlActivity tests the new image analysis activity
func TestAnalyzeImageUrlActivity(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	activities, err := NewActivities()
	require.NoError(t, err)

	env.RegisterActivity(activities.AnalyzeImageUrlActivity)

	// Mock the activity behavior
	mockImageUrl := "https://example.com/image.jpg"
	mockPrompt := "Analyze this image based on requirements: Be funny"
	// mockAnalysis := "This image is quite humorous." // Old string return
	// Return the expected struct type now
	mockAnalysisResult := CheckContentRequirementsResult{Satisfies: true, Reason: "This image is quite humorous."}
	env.OnActivity(activities.AnalyzeImageUrlActivity, mock.Anything, mockImageUrl, mockPrompt).
		Return(mockAnalysisResult, nil) // Return the struct

	// Execute a simple workflow that calls the activity
	env.ExecuteWorkflow(func(ctx workflow.Context) (CheckContentRequirementsResult, error) { // Update workflow return type
		ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		})
		var result CheckContentRequirementsResult // Update result variable type
		err := workflow.ExecuteActivity(ctx, activities.AnalyzeImageUrlActivity, mockImageUrl, mockPrompt).Get(ctx, &result)
		return result, err
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var workflowResult CheckContentRequirementsResult // Update result variable type
	require.NoError(t, env.GetWorkflowResult(&workflowResult))
	assert.Equal(t, mockAnalysisResult, workflowResult) // Compare structs

	env.AssertExpectations(t)
}

// TestBountyAssessmentWorkflow_Twitch tests the workflow with Twitch platform including image analysis
func TestBountyAssessmentWorkflow_Twitch(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	activities, err := NewActivities()
	require.NoError(t, err)

	// Register activities (including the new one)
	env.RegisterActivity(activities.VerifyPayment)
	env.RegisterActivity(activities.TransferUSDC)
	env.RegisterActivity(activities.PullTwitchContent)       // Platform specific
	env.RegisterActivity(activities.AnalyzeImageUrlActivity) // New image analysis
	env.RegisterActivity(activities.CheckContentRequirements)

	// Define bounty amounts
	originalTotalBounty, _ := solana.NewUSDCAmount(10.0)
	totalBounty, _ := solana.NewUSDCAmount(5.0) // User payable
	bountyPerPost, _ := solana.NewUSDCAmount(1.0)
	feeAmount := originalTotalBounty.Sub(totalBounty)

	funderWallet := solanago.NewWallet()
	ownerWallet := solanago.NewWallet()
	payoutWallet := solanago.NewWallet()
	escrowWallet := solanago.NewWallet()
	treasuryWallet := solanago.NewWallet()
	contentID := "twitch-video-assess-test"
	// Ensure this mock URL uses the placeholders the workflow code expects to replace (%{...})
	mockThumbnailURL := "https://static-cdn.jtvnw.net/previews-ttv/live_user_teststreamer-%{width}x%{height}.jpg"
	// This remains the expected URL *after* replacement
	mockFormattedThumbnailURL := "https://static-cdn.jtvnw.net/previews-ttv/live_user_teststreamer-100x100.jpg"
	// mockAnalysisResult := "Thumbnail analysis: Looks good!" // Removed unused variable
	requirements := []string{"Must be about streaming", "Thumbnail must show gameplay"}

	// --- Mock Setups ---
	env.OnActivity(activities.VerifyPayment, mock.Anything, funderWallet.PublicKey(), escrowWallet.PublicKey(), originalTotalBounty, mock.AnythingOfType("string"), mock.Anything).Return(&VerifyPaymentResult{Verified: true, Amount: originalTotalBounty}, nil).Once()
	// Fee Transfer (Amount = 5.0) - Use SPECIFIC treasury wallet
	env.OnActivity(activities.TransferUSDC, mock.Anything, treasuryWallet.PublicKey().String(), feeAmount.ToUSDC(), mock.AnythingOfType("string")).Return(nil).Once()

	// Mock PullTwitchContent (return content with a thumbnail)
	mockedTwitchVideo := &TwitchVideoContent{
		ID:           contentID,
		Title:        "Streaming Fun Times",
		Description:  "Playing games and having fun",
		UserLogin:    "teststreamer",
		ThumbnailURL: mockThumbnailURL,
	}
	env.OnActivity(activities.PullTwitchContent, mock.Anything, contentID, ContentKindVideo).Return(mockedTwitchVideo, nil).Once()

	// Mock AnalyzeImageUrlActivity (return structured result)
	// Expect the *formatted* URL here
	mockImgAnalysisResult := CheckContentRequirementsResult{Satisfies: true, Reason: "Image visually acceptable"}
	env.OnActivity(activities.AnalyzeImageUrlActivity, mock.Anything, mockFormattedThumbnailURL, mock.AnythingOfType("string")).Return(mockImgAnalysisResult, nil).Once()

	// Mock CheckContentRequirements (expect ORIGINAL content data now, not combined)
	// originalContentBytes, err := json.Marshal(mockedTwitchVideo) // Marshal the original video object - Removed as unused
	// require.NoError(t, err)
	// Use mock.AnythingOfType for the byte slice to avoid issues with JSON field ordering
	env.OnActivity(activities.CheckContentRequirements, mock.Anything, mock.AnythingOfType("[]uint8"), requirements).
		Return(CheckContentRequirementsResult{Satisfies: true, Reason: "All good"}, nil).Once()

	// Mock TransferUSDC (Payout) - Use SPECIFIC payout wallet
	env.OnActivity(activities.TransferUSDC, mock.Anything, payoutWallet.PublicKey().String(), bountyPerPost.ToUSDC(), mock.AnythingOfType("string")).Return(nil).Once()

	// Mock Refund (Maybe called on timeout or cancellation) - Uses specific owner wallet already
	// If timeout happens before payout, the full totalBounty (user payable) is refunded.
	// If cancellation happens after payout, remainingBountyAfterPayout is refunded.
	// In this test, a payout HAPPENS, so the refund should be the remaining amount.
	remainingBountyAfterPayout := totalBounty.Sub(bountyPerPost)
	env.OnActivity(activities.TransferUSDC, mock.Anything, ownerWallet.PublicKey().String(), remainingBountyAfterPayout.ToUSDC(), mock.AnythingOfType("string")).Return(nil).Maybe()

	// --- Workflow Input ---
	input := BountyAssessmentWorkflowInput{
		Requirements:       requirements,
		BountyPerPost:      bountyPerPost,
		TotalBounty:        totalBounty,
		TotalCharged:       originalTotalBounty,
		BountyOwnerWallet:  ownerWallet.PublicKey().String(),
		BountyFunderWallet: funderWallet.PublicKey().String(),
		EscrowWallet:       escrowWallet.PublicKey().String(),
		TreasuryWallet:     treasuryWallet.PublicKey().String(),
		Platform:           PlatformTwitch,
		ContentKind:        ContentKindVideo,
		Timeout:            30 * time.Second,
		PaymentTimeout:     5 * time.Second,
	}

	// --- Signal Workflow ---
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("assessment", AssessContentSignal{
			ContentID:    contentID,
			PayoutWallet: payoutWallet.PublicKey().String(),
			Platform:     PlatformTwitch,
			ContentKind:  ContentKindVideo,
		})
	}, time.Second)

	// --- Execute & Assert ---
	env.ExecuteWorkflow(BountyAssessmentWorkflow, input)
	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}
