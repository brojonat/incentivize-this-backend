package rbb

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/brojonat/reddit-bounty-board/solana"
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
			Return("Reddit content", nil)

		// Execute activity
		env.ExecuteWorkflow(func(ctx workflow.Context) (string, error) {
			ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
				StartToCloseTimeout: time.Minute,
			})
			var result string
			err := workflow.ExecuteActivity(ctx, activities.PullRedditContent, "reddit-content-id").Get(ctx, &result)
			return result, err
		})

		// Verify workflow completed successfully
		require.True(t, env.IsWorkflowCompleted())
		require.NoError(t, env.GetWorkflowError())

		// Get workflow result
		var result string
		require.NoError(t, env.GetWorkflowResult(&result))
		assert.Equal(t, "Reddit content", result)

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
			Return("YouTube content", nil)

		// Execute activity
		env.ExecuteWorkflow(func(ctx workflow.Context) (string, error) {
			ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
				StartToCloseTimeout: time.Minute,
			})
			var result string
			err := workflow.ExecuteActivity(ctx, activities.PullYouTubeContent, "youtube-content-id").Get(ctx, &result)
			return result, err
		})

		// Verify workflow completed successfully
		require.True(t, env.IsWorkflowCompleted())
		require.NoError(t, env.GetWorkflowError())

		// Get workflow result
		var result string
		require.NoError(t, env.GetWorkflowResult(&result))
		assert.Equal(t, "YouTube content", result)

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
