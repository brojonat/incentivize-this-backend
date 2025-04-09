package abb

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/brojonat/affiliate-bounty-board/solana"
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

func TestBountyAssessmentWorkflow(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	// Create a mock LLM provider
	mockLLMProvider := &mockLLMProvider{}

	// Create activities instance
	activities := &Activities{
		httpClient: &http.Client{},
		serverURL:  "http://test-server",
		authToken:  "test-token",
		llmDeps: LLMDependencies{
			Provider: mockLLMProvider,
		},
	}

	// Register activities
	env.RegisterActivity(activities.CheckContentRequirements)
	env.RegisterActivity(activities.PayBounty)
	env.RegisterActivity(activities.ReturnBountyToOwner)

	// Mock the child workflow for pulling content
	env.OnWorkflow(PullContentWorkflow, mock.Anything, mock.MatchedBy(func(input PullContentWorkflowInput) bool {
		return input.ContentID == "test-content-1"
	})).Return("test-content-1", nil).Once()

	env.OnWorkflow(PullContentWorkflow, mock.Anything, mock.MatchedBy(func(input PullContentWorkflowInput) bool {
		return input.ContentID == "test-content-2"
	})).Return("test-content-2", nil).Once()

	// Create test input
	bountyPerPost, err := solana.NewUSDCAmount(10)
	require.NoError(t, err)
	totalBounty, err := solana.NewUSDCAmount(20)
	require.NoError(t, err)

	// Create Reddit dependencies for testing
	redditDeps := RedditDependencies{
		UserAgent: "test-agent",
		Username:  "test-user",
		Password:  "test-pass",
		ClientID:  "test-client",
	}

	input := BountyAssessmentWorkflowInput{
		RequirementsDescription: "Test requirements",
		BountyPerPost:           bountyPerPost,
		TotalBounty:             totalBounty,
		OwnerID:                 "test-owner",
		SolanaWallet:            "test-wallet",
		USDCAccount:             "test-usdc",
		ServerURL:               "http://test-server",
		AuthToken:               "test-token",
		PlatformType:            PlatformReddit,
		PlatformDependencies:    redditDeps,
		Timeout:                 5 * time.Minute,
	}

	// Set activity expectations
	env.OnActivity(activities.CheckContentRequirements, mock.Anything, "test-content-1", "Test requirements").
		Return(CheckContentRequirementsResult{
			Satisfies: true,
			Reason:    "Content meets all requirements",
		}, nil).Once()

	env.OnActivity(activities.CheckContentRequirements, mock.Anything, "test-content-2", "Test requirements").
		Return(CheckContentRequirementsResult{
			Satisfies: true,
			Reason:    "Content meets all requirements",
		}, nil).Once()

	env.OnActivity(activities.PayBounty, mock.Anything, "test-user-1", float64(10)).
		Return(nil).Once()

	env.OnActivity(activities.PayBounty, mock.Anything, "test-user-2", float64(10)).
		Return(nil).Once()

	// Execute workflow and immediately send signals
	env.RegisterDelayedCallback(func() {
		// Send signals in sequence
		env.SignalWorkflow("assessment", BountyAssessmentSignal{
			ContentID: "test-content-1",
			UserID:    "test-user-1",
			Platform:  PlatformReddit,
		})
	}, 0)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("assessment", BountyAssessmentSignal{
			ContentID: "test-content-2",
			UserID:    "test-user-2",
			Platform:  PlatformReddit,
		})
	}, time.Second)

	// No need to send cancel signal since bounty will be zero after both payments

	// Start workflow execution
	env.ExecuteWorkflow(BountyAssessmentWorkflow, input)

	// Verify workflow completed successfully
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	// Verify activity calls
	env.AssertExpectations(t)
}

func TestBountyAssessmentWorkflowTimeout(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	// Create activities instance
	activities := &Activities{
		httpClient: &http.Client{},
		serverURL:  "http://test-server",
		authToken:  "test-token",
		llmDeps: LLMDependencies{
			Provider: &mockLLMProvider{},
		},
	}

	// Register activities
	env.RegisterActivity(activities.CheckContentRequirements)
	env.RegisterActivity(activities.PayBounty)
	env.RegisterActivity(activities.ReturnBountyToOwner)

	// Create test input with a very short timeout
	bountyPerPost, err := solana.NewUSDCAmount(10)
	require.NoError(t, err)
	totalBounty, err := solana.NewUSDCAmount(20)
	require.NoError(t, err)

	// Create Reddit dependencies for testing
	redditDeps := RedditDependencies{
		UserAgent: "test-agent",
		Username:  "test-user",
		Password:  "test-pass",
		ClientID:  "test-client",
	}

	input := BountyAssessmentWorkflowInput{
		RequirementsDescription: "Test requirements",
		BountyPerPost:           bountyPerPost,
		TotalBounty:             totalBounty,
		OwnerID:                 "test-owner",
		SolanaWallet:            "test-wallet",
		USDCAccount:             "test-usdc",
		ServerURL:               "http://test-server",
		AuthToken:               "test-token",
		PlatformType:            PlatformReddit,
		PlatformDependencies:    redditDeps,
		Timeout:                 1 * time.Second, // Very short timeout for testing
	}

	// Set activity expectations - only expect ReturnBountyToOwner to be called with the full amount
	env.OnActivity(activities.ReturnBountyToOwner, mock.Anything, "test-owner", float64(20)).
		Return(nil).Once()

	// Start workflow execution
	env.ExecuteWorkflow(BountyAssessmentWorkflow, input)

	// Verify workflow completed successfully
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	// Verify activity calls
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

		// Mock activity
		env.OnActivity(activities.PullRedditContent, "reddit-content-id").
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
			return FormatRedditContent(redditContent), nil
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
	env.OnActivity(activities.CheckContentRequirements, mock.Anything, "test content", "test requirements").
		Return(CheckContentRequirementsResult{
			Satisfies: true,
			Reason:    "Content meets requirements",
		}, nil)

	// Execute workflow
	env.ExecuteWorkflow(CheckContentRequirementsWorkflow, "test content", "test requirements")

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

	// Create activities instance
	activities := &Activities{
		solanaConfig: solana.SolanaConfig{},
	}

	// Register activity
	env.RegisterActivity(activities.TransferUSDC)

	// Mock activity
	env.OnActivity(activities.TransferUSDC, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil)

	// Create test input
	amount, err := solana.NewUSDCAmount(10)
	require.NoError(t, err)

	input := PayBountyWorkflowInput{
		FromAccount:  "DRpbCBMxVnDK7maPM5tPv6dpHGZPWQVr7zr7DgRv9YTB",
		ToAccount:    "8dUmBqpvjqJvXKxdbhWDtWgYz6tNQzqbT6hF4Vz1Vy8h",
		Amount:       amount,
		SolanaConfig: solana.SolanaConfig{},
	}

	// Execute workflow
	env.ExecuteWorkflow(PayBountyWorkflow, input)

	// Verify workflow completed successfully
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	// Verify activity calls
	env.AssertExpectations(t)
}

func TestReturnBountyToOwnerWorkflow(t *testing.T) {
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestWorkflowEnvironment()

	// Create activities instance
	activities := &Activities{
		httpClient: &http.Client{},
		serverURL:  "http://test-server",
		authToken:  "test-token",
	}

	// Register activity
	env.RegisterActivity(activities.ReturnBountyToOwner)

	// Mock activity
	env.OnActivity(activities.ReturnBountyToOwner, mock.Anything, mock.Anything, mock.Anything).
		Return(nil)

	// Create test input
	amount, err := solana.NewUSDCAmount(10)
	require.NoError(t, err)

	input := ReturnBountyToOwnerWorkflowInput{
		ToAccount:    "DRpbCBMxVnDK7maPM5tPv6dpHGZPWQVr7zr7DgRv9YTB",
		Amount:       amount,
		SolanaConfig: solana.SolanaConfig{},
	}

	// Execute workflow
	env.ExecuteWorkflow(ReturnBountyToOwnerWorkflow, input)

	// Verify workflow completed successfully
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	// Verify activity calls
	env.AssertExpectations(t)
}
