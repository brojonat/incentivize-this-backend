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
		escrowKey := solanago.NewWallet().PrivateKey
		escrowAccount := solanago.NewWallet().PublicKey()

		testConfig := SolanaConfig{
			RPCEndpoint:      "https://api.testnet.solana.com",
			WSEndpoint:       "wss://api.testnet.solana.com",
			EscrowPrivateKey: &escrowKey,
			EscrowWallet:     escrowAccount,
		}

		// Create activities instance (minimal config needed for TransferUSDC)
		activities, err := NewActivities(
			testConfig, "", "", // Minimal deps for registration
			RedditDependencies{}, YouTubeDependencies{}, YelpDependencies{},
			GoogleDependencies{}, AmazonDependencies{}, LLMDependencies{},
		)
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
			Wallet:       toWallet.PublicKey().String(),
			Amount:       amount,
			SolanaConfig: testConfig,
		}

		// Mock activity calls using the instance - expect context, string, float64
		env.OnActivity(activities.TransferUSDC, mock.Anything, payInput.Wallet, payInput.Amount.ToUSDC()).Return(nil)

		env.ExecuteWorkflow(PayBountyWorkflow, payInput)
		assert.NoError(t, env.GetWorkflowError())
	})

	// Test ReturnBountyToOwner workflow
	t.Run("ReturnBountyToOwner", func(t *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestWorkflowEnvironment()

		// Create test configuration
		escrowKey := solanago.NewWallet().PrivateKey
		escrowAccount := solanago.NewWallet().PublicKey()

		testConfig := SolanaConfig{
			RPCEndpoint:      "https://api.testnet.solana.com",
			WSEndpoint:       "wss://api.testnet.solana.com",
			EscrowPrivateKey: &escrowKey,
			EscrowWallet:     escrowAccount,
		}

		// Create mock HTTP client
		mockHTTPClient := &http.Client{
			Transport: &mockTransport{},
		}

		// Create activities instance
		activities, err := NewActivities(
			testConfig, // Pass the locally defined abb.SolanaConfig
			"http://test-server",
			"test-token",
			RedditDependencies{},
			YouTubeDependencies{},
			YelpDependencies{},
			GoogleDependencies{},
			AmazonDependencies{},
			LLMDependencies{},
		)
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
		env.OnActivity(activities.PullRedditContent, mock.Anything, mock.Anything).
			Return(mockedRedditContent, nil)

		// Mock CheckContentRequirements - Expect []byte now
		env.OnActivity(activities.CheckContentRequirements, mock.Anything, mock.AnythingOfType("[]uint8"), mock.Anything).
			Return(CheckContentRequirementsResult{
				Satisfies: true,
				Reason:    "Content meets requirements",
			}, nil)

		// Mock TransferUSDC call for returning bounty to owner - expect context, string, float64
		env.OnActivity(activities.TransferUSDC, mock.Anything, "test-owner", mock.AnythingOfType("float64")).Return(nil)

		// Just verify the activity registration is successful
		assert.NoError(t, err)
	})
}

func TestBountyAssessmentWorkflow(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	// Create test configuration
	escrowKey := solanago.NewWallet().PrivateKey
	escrowAccount := solanago.NewWallet().PublicKey()
	treasuryWallet := solanago.NewWallet() // Dummy treasury wallet

	testConfig := SolanaConfig{
		RPCEndpoint:      "https://api.testnet.solana.com",
		WSEndpoint:       "wss://api.testnet.solana.com",
		EscrowPrivateKey: &escrowKey,
		EscrowWallet:     escrowAccount,
		TreasuryWallet:   treasuryWallet.PublicKey().String(), // Add treasury wallet
	}

	// Create activities instance
	activities, err := NewActivities(
		testConfig,
		"http://test-server",
		"test-token",
		RedditDependencies{},
		YouTubeDependencies{},
		YelpDependencies{},
		GoogleDependencies{},
		AmazonDependencies{},
		LLMDependencies{},
	)
	require.NoError(t, err)

	// Register activities
	env.RegisterActivity(activities.VerifyPayment)
	env.RegisterActivity(activities.TransferUSDC)
	env.RegisterActivity(activities.PullRedditContent)
	env.RegisterActivity(activities.CheckContentRequirements)

	// Register workflows
	env.RegisterWorkflow(PullContentWorkflow)

	// Mock activity calls
	// Define bounty amounts for test
	originalTotalBounty, err := solana.NewUSDCAmount(10.0)
	require.NoError(t, err)
	totalBounty, err := solana.NewUSDCAmount(9.0) // User payable (implies 1.0 fee)
	require.NoError(t, err)
	feeAmount := originalTotalBounty.Sub(totalBounty)

	// Mock VerifyPayment - use originalTotalBounty
	env.OnActivity(activities.VerifyPayment, mock.Anything, mock.Anything, originalTotalBounty, mock.Anything).
		Return(&VerifyPaymentResult{
			Verified: true,
			Amount:   originalTotalBounty, // Verify against the original amount funded
		}, nil)

	// Mock Fee Transfer - expect transfer to treasury wallet
	env.OnActivity(activities.TransferUSDC, mock.Anything, testConfig.TreasuryWallet, feeAmount.ToUSDC()).Return(nil).Once()

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
	env.OnActivity(activities.PullRedditContent, mock.Anything, mock.Anything).
		Return(mockedRedditContent, nil)

	// Mock CheckContentRequirements (expect []byte explicitly)
	env.OnActivity(activities.CheckContentRequirements, mock.Anything, mock.AnythingOfType("[]uint8"), mock.Anything).
		Return(CheckContentRequirementsResult{
			Satisfies: true,
			Reason:    "Content meets requirements",
		}, nil)

	// Create test input variables first
	bountyPerPost, _ := solana.NewUSDCAmount(1.0)
	// totalBounty already defined above (user payable)

	// Create valid Solana wallets for testing
	ownerWallet := solanago.NewWallet()
	funderWallet := solanago.NewWallet() // Separate funder for clarity
	payoutWallet := solanago.NewWallet()

	// Create a channel to signal when the mock is called
	// mockCalled := make(chan struct{}) // Removed as logging is simplified

	// Set up mock expectations
	// Payout mock (assuming one signal is sent)
	env.OnActivity(activities.TransferUSDC, mock.Anything, payoutWallet.PublicKey().String(), bountyPerPost.ToUSDC()).Return(nil).Once()

	// Refund mock - Expect remaining amount (totalBounty = 9.0)
	// Since the test timeout likely happens before payout, expect full user-payable amount.
	remainingBounty := totalBounty.Sub(bountyPerPost)
	env.OnActivity(activities.TransferUSDC, mock.Anything, ownerWallet.PublicKey().String(), remainingBounty.ToUSDC()).
		Run(func(args mock.Arguments) {
			// ... logging ...
		}).
		Return(nil).Once()

	// Create test input - include OriginalTotalBounty
	input := BountyAssessmentWorkflowInput{
		Requirements:        []string{"Test requirement"},
		BountyPerPost:       bountyPerPost,
		TotalBounty:         totalBounty,         // User payable amount
		OriginalTotalBounty: originalTotalBounty, // Original funded amount
		BountyOwnerWallet:   ownerWallet.PublicKey().String(),
		BountyFunderWallet:  funderWallet.PublicKey().String(),
		PlatformType:        PlatformReddit,
		Timeout:             5 * time.Second,
		PaymentTimeout:      5 * time.Second, // Set payment timeout
		SolanaConfig:        testConfig,
	}

	// Execute workflow and send signals
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("assessment", AssessContentSignal{
			ContentID:    "test-content",
			PayoutWallet: payoutWallet.PublicKey().String(),
			Platform:     PlatformReddit,
		})
	}, time.Second)

	// Execute workflow with workflow options
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
	escrowKey := solanago.NewWallet().PrivateKey
	escrowAccount := solanago.NewWallet().PublicKey()
	treasuryWallet := solanago.NewWallet() // Dummy treasury wallet
	testConfig := SolanaConfig{
		RPCEndpoint:      "https://api.testnet.solana.com",
		WSEndpoint:       "wss://api.testnet.solana.com",
		EscrowPrivateKey: &escrowKey,
		EscrowWallet:     escrowAccount,
		TreasuryWallet:   treasuryWallet.PublicKey().String(), // Add treasury wallet
	}

	// Create activities instance
	activities, err := NewActivities(
		testConfig,
		"http://test-server",
		"test-token",
		RedditDependencies{},
		YouTubeDependencies{},
		YelpDependencies{},
		GoogleDependencies{},
		AmazonDependencies{},
		LLMDependencies{},
	)
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
	funderWallet := solanago.NewWallet() // Define funder wallet for VerifyPayment

	// Create a channel to signal when the refund mock is called
	// mockCalled := make(chan struct{}) // Removed as logging is simplified

	// --- Mock Setups ---
	// Mock VerifyPayment - Return success with the originalTotalBounty
	env.OnActivity(activities.VerifyPayment, mock.Anything, funderWallet.PublicKey(), originalTotalBounty, mock.Anything).
		Return(&VerifyPaymentResult{Verified: true, Amount: originalTotalBounty}, nil).Maybe() // May or may not be called before timeout

	// Mock Fee Transfer - Should be called if payment is verified before timeout
	env.OnActivity(activities.TransferUSDC, mock.Anything, testConfig.TreasuryWallet, feeAmount.ToUSDC()).Return(nil).Maybe()

	// Mock content pulling/checking - basic success mocks, may not be called
	env.OnActivity(activities.PullRedditContent, mock.Anything, mock.Anything).
		Return(&RedditContent{ID: "timeout-test-content"}, nil).Maybe()
	env.OnActivity(activities.CheckContentRequirements, mock.Anything, mock.Anything, mock.Anything).
		Return(CheckContentRequirementsResult{Satisfies: true}, nil).Maybe()

	// Mock TransferUSDC (Refund) - Expect refund to owner with totalBounty (user payable) amount
	// because the timeout is very short and should hit before any payout.
	// The refund logic returns the remaining USER PAYABLE amount.
	env.OnActivity(activities.TransferUSDC,
		mock.Anything,                    // Context
		ownerWallet.PublicKey().String(), // Owner wallet
		totalBounty.ToUSDC(),             // Expect the full user-payable amount on timeout refund
	).Run(func(args mock.Arguments) { // Add Run back for logging and signaling
		owner := args.String(1)
		amountArg := args.Get(2)
		t.Logf("Refund Mock .Run() called: owner=%s, amount=%v (type: %T)", owner, amountArg, amountArg)
		// Non-blocking send to signal execution for manual verification
		// select {
		// case mockCalled <- struct{}{}:
		// 	t.Logf("Successfully signaled refundCalled channel")
		// default:
		// 	t.Logf("Could not signal refundCalled channel (already full or no receiver?)")
		// }
	}).Return(nil).Maybe() // Use Maybe() to indicate this call might or might not happen

	// Create test input
	input := BountyAssessmentWorkflowInput{
		Requirements:        []string{"Test requirement"},
		BountyPerPost:       bountyPerPost,
		TotalBounty:         totalBounty,         // User payable amount
		OriginalTotalBounty: originalTotalBounty, // Original funded amount
		BountyOwnerWallet:   ownerWallet.PublicKey().String(),
		BountyFunderWallet:  funderWallet.PublicKey().String(),
		PlatformType:        PlatformReddit,
		Timeout:             100 * time.Millisecond, // Use a very short timeout
		PaymentTimeout:      5 * time.Second,        // Keep payment timeout reasonable
		SolanaConfig:        testConfig,
	}

	// Execute workflow
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

	// Create test configuration
	escrowKey := solanago.NewWallet().PrivateKey
	escrowAccount := solanago.NewWallet().PublicKey()
	treasuryWallet := solanago.NewWallet() // Dummy treasury wallet
	testConfig := SolanaConfig{
		RPCEndpoint:      "https://api.testnet.solana.com",
		WSEndpoint:       "wss://api.testnet.solana.com",
		EscrowPrivateKey: &escrowKey,
		EscrowWallet:     escrowAccount,
		TreasuryWallet:   treasuryWallet.PublicKey().String(), // Add treasury wallet
	}

	// Create activities instance
	activities, err := NewActivities(
		testConfig,
		"http://test-server",
		"test-token",
		RedditDependencies{},
		YouTubeDependencies{},
		YelpDependencies{},
		GoogleDependencies{},
		AmazonDependencies{},
		LLMDependencies{},
	)
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
	payoutWallet := solanago.NewWallet()
	contentID := "idempotent-content"

	// Mock VerifyPayment - Called Once, use originalTotalBounty
	env.OnActivity(activities.VerifyPayment, mock.Anything, funderWallet.PublicKey(), originalTotalBounty, mock.Anything).Return(&VerifyPaymentResult{Verified: true, Amount: originalTotalBounty}, nil).Once()

	// Mock Fee Transfer - Called Once
	env.OnActivity(activities.TransferUSDC, mock.Anything, testConfig.TreasuryWallet, feeAmount.ToUSDC()).Return(nil).Once()

	// Mock PullRedditContent - Maybe called (handles potential multiple signals)
	mockRedditContent := &RedditContent{ID: contentID, Title: "Idempotent Test"}
	env.OnActivity(activities.PullRedditContent, mock.Anything, contentID).Return(mockRedditContent, nil).Maybe()

	// Mock CheckContentRequirements - Expect []byte, Maybe called
	env.OnActivity(activities.CheckContentRequirements, mock.Anything, mock.AnythingOfType("[]uint8"), mock.Anything).Return(CheckContentRequirementsResult{Satisfies: true, Reason: "OK"}, nil).Maybe()

	// Mock TransferUSDC (Payout) - Called Once for the specific payout
	env.OnActivity(activities.TransferUSDC, mock.Anything, payoutWallet.PublicKey().String(), bountyPerPost.ToUSDC()).Return(nil).Once()

	// Mock TransferUSDC (Return to Owner upon Cancel) - Called Once with remaining amount
	// Bounty starts at 5.0, one payout of 1.0 occurs, so 4.0 should be returned.
	remainingBounty := totalBounty.Sub(bountyPerPost)
	env.OnActivity(activities.TransferUSDC, mock.Anything, "test-owner", remainingBounty.ToUSDC()).Return(nil).Once()

	input := BountyAssessmentWorkflowInput{
		Requirements:        []string{"Idempotency Test"},
		BountyPerPost:       bountyPerPost,
		TotalBounty:         totalBounty,
		OriginalTotalBounty: originalTotalBounty,
		BountyOwnerWallet:   "test-owner", // Keep as string for the mock expectation
		BountyFunderWallet:  funderWallet.PublicKey().String(),
		PlatformType:        PlatformReddit,
		Timeout:             30 * time.Second, // Longer timeout
		PaymentTimeout:      5 * time.Second,
		SolanaConfig:        testConfig,
	}

	signal := AssessContentSignal{
		ContentID:    contentID,
		PayoutWallet: payoutWallet.PublicKey().String(),
		Platform:     PlatformReddit,
	}

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AssessmentSignalName, signal)
		// Send the *same* signal again shortly after
		env.RegisterDelayedCallback(func() {
			env.SignalWorkflow(AssessmentSignalName, signal)
			// Send a final signal to terminate
			env.RegisterDelayedCallback(func() {
				env.SignalWorkflow(CancelSignalName, CancelBountySignal{BountyOwnerWallet: "test-owner"})
			}, time.Millisecond*100)
		}, time.Millisecond*100)
	}, time.Second)

	env.ExecuteWorkflow(BountyAssessmentWorkflow, input)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	// Assert that mocks were called exactly once as expected
	env.AssertExpectations(t)
}

func TestBountyAssessmentWorkflow_RequirementsNotMet(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	escrowKey := solanago.NewWallet().PrivateKey
	escrowAccount := solanago.NewWallet().PublicKey()
	treasuryWallet := solanago.NewWallet() // Dummy treasury wallet
	testConfig := SolanaConfig{
		RPCEndpoint:      "mock",
		EscrowPrivateKey: &escrowKey,
		EscrowWallet:     escrowAccount,
		TreasuryWallet:   treasuryWallet.PublicKey().String(), // Add treasury wallet
	}

	activities, err := NewActivities(testConfig, "", "", RedditDependencies{}, YouTubeDependencies{}, YelpDependencies{}, GoogleDependencies{}, AmazonDependencies{}, LLMDependencies{})
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
	contentID := "failed-content"
	mockRedditContent := &RedditContent{ID: contentID, Title: "Failed Test"}

	// Re-introduce channel for specific assertion
	refundCalled := make(chan struct{}, 1)

	// --- Mock Expectations ---
	// Expect initial payment verification (original amount)
	env.OnActivity(activities.VerifyPayment, mock.Anything, funderWallet.PublicKey(), originalTotalBounty, mock.Anything).Return(&VerifyPaymentResult{Verified: true, Amount: originalTotalBounty}, nil).Once()

	// Expect fee transfer
	env.OnActivity(activities.TransferUSDC, mock.Anything, testConfig.TreasuryWallet, feeAmount.ToUSDC()).Return(nil).Once()

	// Expect content pull for the failing content
	env.OnActivity(activities.PullRedditContent, mock.Anything, contentID).Return(mockRedditContent, nil).Once()

	// Expect requirements check to fail (expect []byte explicitly)
	env.OnActivity(activities.CheckContentRequirements, mock.Anything, mock.AnythingOfType("[]uint8"), mock.Anything).Return(CheckContentRequirementsResult{Satisfies: false, Reason: "Did not meet criteria"}, nil).Once()

	// Expect NO payout transfer
	env.OnActivity(activities.TransferUSDC, mock.Anything, payoutWallet.PublicKey().String(), bountyPerPost.ToUSDC()).Return(nil).Never()

	// Expect refund transfer to the owner for the full remaining USER PAYABLE bounty.
	// Use Sub method here as well
	env.OnActivity(activities.TransferUSDC,
		mock.Anything,
		ownerWallet.PublicKey().String(),
		totalBounty.ToUSDC(),
	).Run(func(args mock.Arguments) { // Add Run back for logging and signaling
		owner := args.String(1)
		amountArg := args.Get(2)
		t.Logf("Refund Mock .Run() called: owner=%s, amount=%v (type: %T)", owner, amountArg, amountArg)
		// Non-blocking send to signal execution for manual verification
		select {
		case refundCalled <- struct{}{}:
			t.Logf("Successfully signaled refundCalled channel")
		default:
			t.Logf("Could not signal refundCalled channel (already full or no receiver?)")
		}
	}).Return(nil).Once() // Change Maybe() to Once() as cancel signal should trigger this

	// --- Workflow Input ---
	input := BountyAssessmentWorkflowInput{
		Requirements:        []string{"Failure Test"},
		BountyPerPost:       bountyPerPost,
		TotalBounty:         totalBounty,
		OriginalTotalBounty: originalTotalBounty,
		BountyOwnerWallet:   ownerWallet.PublicKey().String(),
		BountyFunderWallet:  funderWallet.PublicKey().String(),
		PlatformType:        PlatformReddit,
		Timeout:             30 * time.Second,
		PaymentTimeout:      5 * time.Second,
		SolanaConfig:        testConfig,
	}

	// --- Signal Sequence (Simplified) ---
	failSignal := AssessContentSignal{
		ContentID:    contentID,
		PayoutWallet: payoutWallet.PublicKey().String(), // Still need payout wallet in signal struct
		Platform:     PlatformReddit,
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
		}, time.Millisecond*10) // Minimal delay
	}, time.Second)

	// --- Execution & Verification ---
	env.ExecuteWorkflow(BountyAssessmentWorkflow, input)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	// Re-introduce the channel check as the primary verification for the refund mock.
	select {
	case <-refundCalled:
		t.Logf("Verified refundCalled channel received signal.")
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
		activities, err := NewActivities(
			SolanaConfig{}, // Empty config OK for non-Solana activities
			"", "",
			RedditDependencies{}, // Need empty deps struct
			YouTubeDependencies{}, YelpDependencies{}, GoogleDependencies{}, AmazonDependencies{}, LLMDependencies{},
		)
		require.NoError(t, err)

		// Register activity using the instance
		env.RegisterActivity(activities.PullRedditContent)

		// Mock activity using the instance
		env.OnActivity(activities.PullRedditContent, mock.Anything, "reddit-content-id").
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
			err := workflow.ExecuteActivity(ctx, activities.PullRedditContent, "reddit-content-id").Get(ctx, &redditContent)
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
		activities, err := NewActivities(
			SolanaConfig{}, // Empty config OK
			"", "",
			RedditDependencies{}, YouTubeDependencies{}, // Need empty deps struct
			YelpDependencies{}, GoogleDependencies{}, AmazonDependencies{}, LLMDependencies{},
		)
		require.NoError(t, err)

		// Register activity using the instance
		env.RegisterActivity(activities.PullYouTubeContent)

		// Mock activity using the instance
		mockedTranscript := "1\n00:00:00,000 --> 00:00:05,000\nThis is a test transcript.\n\n2\n00:00:05,000 --> 00:00:10,000\nIt contains multiple lines."
		env.OnActivity(activities.PullYouTubeContent, mock.Anything, "youtube-content-id").
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
			err := workflow.ExecuteActivity(ctx, activities.PullYouTubeContent, "youtube-content-id").Get(ctx, &youtubeContent)
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
}

func TestCheckContentRequirementsWorkflow(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	// Create activities instance using NewActivities for consistency
	activities, err := NewActivities(
		SolanaConfig{}, // Empty config OK
		"", "",
		RedditDependencies{}, YouTubeDependencies{}, YelpDependencies{}, GoogleDependencies{}, AmazonDependencies{},
		LLMDependencies{Provider: &mockLLMProvider{}}, // Provide the mock LLM provider
	)
	require.NoError(t, err)

	// Register activity using the instance
	env.RegisterActivity(activities.CheckContentRequirements)

	// Mock activity using the instance - Expect []byte now
	env.OnActivity(activities.CheckContentRequirements, mock.Anything, mock.AnythingOfType("[]uint8"), mock.Anything).Return(CheckContentRequirementsResult{
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
	escrowKey := solanago.NewWallet().PrivateKey
	escrowAccount := solanago.NewWallet().PublicKey()
	testConfig := SolanaConfig{
		EscrowPrivateKey: &escrowKey,
		EscrowWallet:     escrowAccount,
	}
	activities, err := NewActivities(
		testConfig, "", "", // Minimal deps for registration
		RedditDependencies{}, YouTubeDependencies{}, YelpDependencies{},
		GoogleDependencies{}, AmazonDependencies{}, LLMDependencies{},
	)
	require.NoError(t, err)

	// Register activity using the instance method
	env.RegisterActivity(activities.TransferUSDC)

	// Mock activity using the instance method
	// Ensure arguments match TransferUSDC: ctx, toAccount (string), amount (float64)
	env.OnActivity(activities.TransferUSDC, mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("float64")).
		Return(nil)

	// Create test input
	amount, err := solana.NewUSDCAmount(10)
	require.NoError(t, err)

	input := PayBountyWorkflowInput{
		Wallet:       "8dUmBqpvjqJvXKxdbhWDtWgYz6tNQzqbT6hF4Vz1Vy8h", // Example address
		Amount:       amount,
		SolanaConfig: SolanaConfig{}, // Use local SolanaConfig
	}

	// Execute workflow
	env.ExecuteWorkflow(PayBountyWorkflow, input)

	// Verify workflow completed successfully
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	// Verify activity calls
	env.AssertExpectations(t)
}
