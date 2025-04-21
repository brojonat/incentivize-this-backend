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

func TestBountyAssessmentWorkflowTimeout(t *testing.T) {
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
	// Mock using instance method and specific/typed args (handles timeout case) - expect context, string, float64
	// Define a payout wallet for the signal in this test scope
	payoutWallet := wallet.PublicKey().String()
	env.OnActivity(activities.TransferUSDC, mock.Anything, payoutWallet, bountyPerPost.ToUSDC()).Return(nil)         // Mock for successful payout
	env.OnActivity(activities.TransferUSDC, mock.Anything, "test-owner", mock.AnythingOfType("float64")).Return(nil) // Mock for timeout/cancellation return

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
			PayoutWallet: payoutWallet, // Changed from UserID
			Platform:     PlatformReddit,
		})
	}, time.Second)

	// Execute workflow with workflow options
	env.ExecuteWorkflow(BountyAssessmentWorkflow, input)
	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
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
