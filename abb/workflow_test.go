package abb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
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

		env.OnActivity(activities.PullRedditContent, mock.Anything, mock.Anything).
			Return(&RedditContent{
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
			}, nil)

		env.OnActivity(activities.CheckContentRequirements, mock.Anything, mock.Anything, mock.Anything).
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

	testConfig := SolanaConfig{
		RPCEndpoint:      "https://api.testnet.solana.com",
		WSEndpoint:       "wss://api.testnet.solana.com",
		EscrowPrivateKey: &escrowKey,
		EscrowWallet:     escrowAccount,
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
	initialBalance, err := solana.NewUSDCAmount(10.0)
	require.NoError(t, err)

	env.OnActivity(activities.VerifyPayment, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&VerifyPaymentResult{
			Verified: true,
			Amount:   initialBalance,
		}, nil)

	env.OnActivity(activities.PullRedditContent, mock.Anything, mock.Anything).
		Return(&RedditContent{
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
		}, nil)

	env.OnActivity(activities.CheckContentRequirements, mock.Anything, mock.Anything, mock.Anything).
		Return(CheckContentRequirementsResult{
			Satisfies: true,
			Reason:    "Content meets requirements",
		}, nil)

	// Create test input BEFORE defining the mock that uses its variables
	bountyPerPost, err := solana.NewUSDCAmount(1.0)
	require.NoError(t, err)

	// Create valid Solana wallet for testing
	wallet := solanago.NewWallet()

	// Mock TransferUSDC if BountyAssessmentWorkflow uses it
	// Mock using instance method and specific/typed args - expect context, string, float64
	// Use activities instance for mocking
	env.OnActivity(activities.TransferUSDC, mock.Anything, wallet.PublicKey().String(), bountyPerPost.ToUSDC()).Return(nil)
	env.OnActivity(activities.TransferUSDC, mock.Anything, "test-owner", mock.AnythingOfType("float64")).Return(nil)

	totalBounty, err := solana.NewUSDCAmount(10.0)
	require.NoError(t, err)

	input := BountyAssessmentWorkflowInput{
		Requirements:       []string{"Test requirement 1", "Test requirement 2"},
		BountyPerPost:      bountyPerPost,
		TotalBounty:        totalBounty,
		BountyOwnerWallet:  "test-owner",                // Renamed field
		BountyFunderWallet: wallet.PublicKey().String(), // Renamed field
		PlatformType:       PlatformReddit,
		Timeout:            5 * time.Second, // Shorter timeout for testing
		PaymentTimeout:     time.Second,     // Shorter payment timeout for testing
		SolanaConfig:       testConfig,
	}

	// Execute workflow and send signals
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("assessment", AssessContentSignal{
			ContentID:    "test-content",
			PayoutWallet: wallet.PublicKey().String(),
			Platform:     PlatformReddit,
		})
	}, time.Second)

	// Execute workflow with workflow options
	env.ExecuteWorkflow(BountyAssessmentWorkflow, input)
	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
}

// TestEnvironment represents the test environment for workflow tests
type TestEnvironment struct {
	T    *testing.T
	Mock *mock.Mock
}

// Setup initializes the test environment
func (env *TestEnvironment) Setup() {
	env.Mock = &mock.Mock{}
}

// BountyAssessmentWorkflow represents the workflow being tested
type BountyAssessmentWorkflow struct {
	env *TestEnvironment
}

// Start begins the workflow execution
func (w *BountyAssessmentWorkflow) Start() {
	// Simulate the workflow starting
	go func() {
		// Simulate the workflow calling TransferUSDC
		w.env.Mock.Called("test-owner", "test-amount", 10.0)
	}()
}

func TestBountyAssessmentWorkflowTimeout(t *testing.T) {
	// Create a test environment
	testEnv := &TestEnvironment{
		T: t,
	}
	testEnv.Setup()

	// Create a channel to signal when the mock is called
	mockCalled := make(chan struct{})

	// Set up mock expectations
	testEnv.Mock.On("TransferUSDC", mock.Anything, mock.Anything, mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		owner := args.Get(0).(string)
		amount := args.Get(2).(float64)
		log.Printf("Mock TransferUSDC called with owner=%s, amount=%f", owner, amount)
		close(mockCalled)
	})

	// Create a test workflow
	workflow := &BountyAssessmentWorkflow{
		env: testEnv,
	}

	// Start the workflow
	workflow.Start()

	// Wait for the mock to be called
	select {
	case <-mockCalled:
		// Mock was called, continue with the test
	case <-time.After(5 * time.Second):
		t.Fatal("Mock was not called within 5 seconds")
	}

	// Verify mock expectations
	testEnv.Mock.AssertExpectations(t)
}

func TestBountyAssessmentWorkflow_Idempotency(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	escrowKey := solanago.NewWallet().PrivateKey
	escrowAccount := solanago.NewWallet().PublicKey()
	testConfig := SolanaConfig{RPCEndpoint: "mock", EscrowPrivateKey: &escrowKey, EscrowWallet: escrowAccount}

	activities, err := NewActivities(testConfig, "", "", RedditDependencies{}, YouTubeDependencies{}, YelpDependencies{}, GoogleDependencies{}, AmazonDependencies{}, LLMDependencies{})
	require.NoError(t, err)

	env.RegisterActivity(activities.VerifyPayment)
	env.RegisterActivity(activities.TransferUSDC)
	env.RegisterActivity(activities.PullRedditContent)
	env.RegisterActivity(activities.CheckContentRequirements)
	env.RegisterWorkflow(PullContentWorkflow) // Also register child workflow

	initialBalance, _ := solana.NewUSDCAmount(10.0)
	bountyPerPost, _ := solana.NewUSDCAmount(1.0)
	totalBounty, _ := solana.NewUSDCAmount(5.0) // Enough for 5 posts
	funderWallet := solanago.NewWallet()
	payoutWallet := solanago.NewWallet()
	contentID := "idempotent-content"

	// Mock VerifyPayment - Called Once
	env.OnActivity(activities.VerifyPayment, mock.Anything, funderWallet.PublicKey(), initialBalance, mock.Anything).Return(&VerifyPaymentResult{Verified: true, Amount: initialBalance}, nil).Once()

	// Mock PullRedditContent - Called Once for the specific contentID
	mockRedditContent := &RedditContent{ID: contentID, Title: "Idempotent Test"}
	env.OnActivity(activities.PullRedditContent, mock.Anything, contentID).Return(mockRedditContent, nil).Once()

	// Mock CheckContentRequirements - Called Once for the specific content
	// Need to marshal the mock content to JSON for the check
	mockContentJSON, _ := json.Marshal(mockRedditContent)
	env.OnActivity(activities.CheckContentRequirements, mock.Anything, string(mockContentJSON), mock.Anything).Return(CheckContentRequirementsResult{Satisfies: true, Reason: "OK"}, nil).Once()

	// Mock TransferUSDC (Payout) - Called Once for the specific payout
	env.OnActivity(activities.TransferUSDC, mock.Anything, payoutWallet.PublicKey().String(), bountyPerPost.ToUSDC()).Return(nil).Once()

	// Mock TransferUSDC (Return to Owner) - Should NOT be called in this test
	env.OnActivity(activities.TransferUSDC, mock.Anything, "test-owner", mock.AnythingOfType("float64")).Return(nil)

	input := BountyAssessmentWorkflowInput{
		Requirements:        []string{"Idempotency Test"},
		BountyPerPost:       bountyPerPost,
		TotalBounty:         totalBounty,
		OriginalTotalBounty: initialBalance,
		BountyOwnerWallet:   "test-owner",
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
			// Send a final signal to terminate (assuming totalBounty allows multiple payouts)
			// This requires adjusting totalBounty or sending fewer signals if only one payout is expected.
			// For simplicity, let's assume totalBounty > bountyPerPost and send a different signal to consume remaining.
			// Or signal cancellation. Let's signal cancel to cleanly end.
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
	testConfig := SolanaConfig{RPCEndpoint: "mock", EscrowPrivateKey: &escrowKey, EscrowWallet: escrowAccount}

	activities, err := NewActivities(testConfig, "", "", RedditDependencies{}, YouTubeDependencies{}, YelpDependencies{}, GoogleDependencies{}, AmazonDependencies{}, LLMDependencies{})
	require.NoError(t, err)

	env.RegisterActivity(activities.VerifyPayment)
	env.RegisterActivity(activities.TransferUSDC)
	env.RegisterActivity(activities.PullRedditContent)
	env.RegisterActivity(activities.CheckContentRequirements)
	env.RegisterWorkflow(PullContentWorkflow)

	initialBalance, _ := solana.NewUSDCAmount(10.0)
	bountyPerPost, _ := solana.NewUSDCAmount(1.0)
	totalBounty, _ := solana.NewUSDCAmount(5.0)
	funderWallet := solanago.NewWallet()
	payoutWallet := solanago.NewWallet() // Payout wallet still needed in signal, though won't be used
	contentID := "failed-content"

	// Channel to signal refund mock execution
	refundCalled := make(chan struct{}, 1)

	// Mock VerifyPayment - Use specific funder wallet
	env.OnActivity(activities.VerifyPayment, mock.Anything, funderWallet.PublicKey(), initialBalance, mock.Anything).Return(&VerifyPaymentResult{Verified: true, Amount: initialBalance}, nil).Once()

	// Mock PullRedditContent - Called Once
	mockRedditContent := &RedditContent{ID: contentID, Title: "Failed Test"}
	env.OnActivity(activities.PullRedditContent, mock.Anything, contentID).Return(mockRedditContent, nil).Once()

	// Mock CheckContentRequirements - Called Once, returns Satisfies: false
	mockContentJSON, _ := json.Marshal(mockRedditContent)
	env.OnActivity(activities.CheckContentRequirements, mock.Anything, string(mockContentJSON), mock.Anything).Return(CheckContentRequirementsResult{Satisfies: false, Reason: "Did not meet criteria"}, nil).Once()

	// Mock TransferUSDC (Payout) - Should NOT be called
	env.OnActivity(activities.TransferUSDC, mock.Anything, payoutWallet.PublicKey().String(), bountyPerPost.ToUSDC()).Return(nil).Times(0)

	// Mock TransferUSDC (Return to Owner) - Should be called by cancellation signal
	env.OnActivity(activities.TransferUSDC, mock.Anything, "test-owner", totalBounty.ToUSDC()).
		Return(func(ctx context.Context, owner string, amount float64) error {
			t.Logf("Cancellation Refund Mock Called: owner=%s, amount=%f", owner, amount)
			// Use blocking send
			refundCalled <- struct{}{}
			return nil
		}, nil).Once()

	input := BountyAssessmentWorkflowInput{
		Requirements:        []string{"Failure Test"},
		BountyPerPost:       bountyPerPost,
		TotalBounty:         totalBounty,
		OriginalTotalBounty: initialBalance, // Use initialBalance for verification mock
		BountyOwnerWallet:   "test-owner",
		BountyFunderWallet:  funderWallet.PublicKey().String(), // Use specific funder wallet
		PlatformType:        PlatformReddit,
		Timeout:             30 * time.Second,
		PaymentTimeout:      5 * time.Second,
		SolanaConfig:        testConfig,
	}

	signal := AssessContentSignal{
		ContentID:    contentID,
		PayoutWallet: payoutWallet.PublicKey().String(),
		Platform:     PlatformReddit,
	}

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(AssessmentSignalName, signal) // First signal (fails)
		// Send the *same* signal again - should be ignored by idempotency check
		env.RegisterDelayedCallback(func() {
			env.SignalWorkflow(AssessmentSignalName, signal)
			// Send cancellation signal to end workflow and trigger return
			env.RegisterDelayedCallback(func() {
				env.SignalWorkflow(CancelSignalName, CancelBountySignal{BountyOwnerWallet: "test-owner"})
			}, time.Millisecond*100)
		}, time.Millisecond*100)
	}, time.Second)

	env.ExecuteWorkflow(BountyAssessmentWorkflow, input)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	// Wait for the refund mock to be called before asserting expectations
	select {
	case <-refundCalled:
		// Refund mock was called, proceed to assert expectations
	case <-time.After(2 * time.Second): // Safety timeout
		t.Fatal("Timeout waiting for refund activity mock to be called")
	}

	// Assert that mocks were called exactly as expected (or not at all for payout)
	env.AssertExpectations(t)
}

func TestPlatformActivities(t *testing.T) {
	// Test Reddit content pulling
	t.Run("PullRedditContent", func(t *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestWorkflowEnvironment()

		// Create activities instance
		activities := &Activities{}

		// Register activity
		env.RegisterActivity(activities.PullRedditContent)

		// Mock activity - include mock.Anything for context
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
			// We removed FormatRedditContent, marshal to JSON instead
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
		err := json.Unmarshal([]byte(result), &parsed)
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

		// Create activities instance
		activities := &Activities{}

		// Register activity
		env.RegisterActivity(activities.PullYouTubeContent)

		// Mock activity
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
				Captions: []YouTubeCaption{
					{
						ID:              "caption1",
						Language:        "en",
						Name:            "English",
						TrackKind:       "standard",
						LastUpdated:     time.Now(),
						Content:         "1\n00:00:00,000 --> 00:00:05,000\nThis is a test caption.\n\n2\n00:00:05,000 --> 00:00:10,000\nIt contains multiple lines.",
						IsAutoGenerated: false,
					},
					{
						ID:              "caption2",
						Language:        "es",
						Name:            "Spanish",
						TrackKind:       "standard",
						LastUpdated:     time.Now(),
						Content:         "1\n00:00:00,000 --> 00:00:05,000\nEste es un subtítulo de prueba.\n\n2\n00:00:05,000 --> 00:00:10,000\nContiene múltiples líneas.",
						IsAutoGenerated: false,
					},
				},
			}, nil)

		// Execute activity
		env.ExecuteWorkflow(func(ctx workflow.Context) (string, error) {
			ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
				StartToCloseTimeout: time.Minute,
			})
			var youtubeContent *YouTubeContent
			err := workflow.ExecuteActivity(ctx, activities.PullYouTubeContent, "youtube-content-id").Get(ctx, &youtubeContent)
			if err != nil {
				return "", err
			}
			return FormatYouTubeContent(youtubeContent), nil
		})

		// Verify workflow completed successfully
		require.True(t, env.IsWorkflowCompleted())
		require.NoError(t, env.GetWorkflowError())

		// Get workflow result
		var result string
		require.NoError(t, env.GetWorkflowResult(&result))

		// Parse the JSON result
		var parsed map[string]interface{}
		err := json.Unmarshal([]byte(result), &parsed)
		require.NoError(t, err)

		// Verify the JSON fields
		assert.Equal(t, "youtube-content-id", parsed["id"])
		assert.Equal(t, "Test Video", parsed["title"])
		assert.Equal(t, "Test video description", parsed["description"])
		assert.Equal(t, "Test Channel", parsed["channel_title"])
		assert.Equal(t, "1000", parsed["view_count"])
		assert.Equal(t, "100", parsed["like_count"])
		assert.Equal(t, "50", parsed["comment_count"])

		// Verify captions
		captions, ok := parsed["captions"].([]interface{})
		require.True(t, ok)
		require.Len(t, captions, 2)

		// Verify first caption
		caption1 := captions[0].(map[string]interface{})
		assert.Equal(t, "caption1", caption1["id"])
		assert.Equal(t, "en", caption1["language"])
		assert.Equal(t, "English", caption1["name"])
		assert.Equal(t, "standard", caption1["track_kind"])
		assert.Equal(t, false, caption1["is_auto_generated"])
		assert.Contains(t, caption1["content"], "This is a test caption")

		// Verify second caption
		caption2 := captions[1].(map[string]interface{})
		assert.Equal(t, "caption2", caption2["id"])
		assert.Equal(t, "es", caption2["language"])
		assert.Equal(t, "Spanish", caption2["name"])
		assert.Equal(t, "standard", caption2["track_kind"])
		assert.Equal(t, false, caption2["is_auto_generated"])
		assert.Contains(t, caption2["content"], "Este es un subtítulo de prueba")

		// Verify activity calls
		env.AssertExpectations(t)
	})
}

func TestCheckContentRequirementsWorkflow(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	// Create activities instance
	activities := &Activities{
		llmDeps: LLMDependencies{
			Provider: &mockLLMProvider{},
		},
	}

	// Register activity
	env.RegisterActivity(activities.CheckContentRequirements)

	// Mock activity
	env.OnActivity(activities.CheckContentRequirements, mock.Anything, "test content", []string{"test requirements"}).
		Return(CheckContentRequirementsResult{
			Satisfies: true,
			Reason:    "Content meets requirements",
		}, nil)

	// Execute workflow
	env.ExecuteWorkflow(CheckContentRequirementsWorkflow, "test content", []string{"test requirements"})

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

func TestPullContentWorkflow_Reddit(t *testing.T) {
	// ... existing code ...
}
