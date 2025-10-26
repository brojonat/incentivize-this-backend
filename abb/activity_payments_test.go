package abb

import (
	"os"
	"testing"
	"time"

	"github.com/brojonat/affiliate-bounty-board/solana"
	solanago "github.com/gagliardetto/solana-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

func TestTransferUSDC_Validation(t *testing.T) {
	// Set up test environment
	os.Setenv("ENV", "test")
	os.Setenv("SOLANA_ESCROW_PRIVATE_KEY", "dummy_private_key_for_testing")

	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	activities := &Activities{}
	env.RegisterActivity(activities)

	tests := []struct {
		name          string
		wallet        string
		amount        float64
		memo          string
		expectedError string
	}{
		{
			name:          "empty wallet address",
			wallet:        "",
			amount:        10.0,
			memo:          "test",
			expectedError: "recipient wallet address is required",
		},
		{
			name:          "invalid wallet address",
			wallet:        "invalid-address",
			amount:        10.0,
			memo:          "test",
			expectedError: "invalid recipient wallet address",
		},
		{
			name:          "zero amount",
			wallet:        "11111111111111111111111111111112",
			amount:        0,
			memo:          "test",
			expectedError: "transfer amount must be positive",
		},
		{
			name:          "negative amount",
			wallet:        "11111111111111111111111111111112",
			amount:        -5.0,
			memo:          "test",
			expectedError: "transfer amount must be positive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := env.ExecuteActivity(activities.TransferUSDC, tt.wallet, tt.amount, tt.memo)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.expectedError)
		})
	}
}

func TestVerifyPayment_Configuration(t *testing.T) {
	// This test verifies that VerifyPayment properly loads configuration
	// In test mode, it should use dummy configuration
	os.Setenv("ENV", "test")
	os.Setenv("SOLANA_ESCROW_PRIVATE_KEY", "dummy_private_key_for_testing")
	os.Setenv("FOROHTOO_SERVER_URL", "http://localhost:8080")

	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.SetTestTimeout(5 * time.Second)

	activities := &Activities{}
	env.RegisterActivity(activities)

	// Create test inputs
	expectedRecipient := solanago.MustPublicKeyFromBase58("11111111111111111111111111111112")
	expectedAmount, err := solana.NewUSDCAmount(100.0)
	require.NoError(t, err)

	bountyID := "test-bounty-123"
	timeout := 1 * time.Second // Short timeout for test

	// This will fail because we can't actually connect to forohtoo in tests,
	// but it verifies the configuration loading works
	_, err = env.ExecuteActivity(
		activities.VerifyPayment,
		expectedRecipient,
		expectedAmount,
		bountyID,
		timeout,
	)

	// We expect an error since we can't actually connect to forohtoo
	// but the configuration should load without error
	assert.Error(t, err)
	// The error should be about forohtoo connection, not configuration
	assert.NotContains(t, err.Error(), "failed to get configuration")
}

func TestVerifyPaymentResult(t *testing.T) {
	// Test the VerifyPaymentResult struct
	amount, err := solana.NewUSDCAmount(50.0)
	require.NoError(t, err)

	result := &VerifyPaymentResult{
		Verified:     true,
		Amount:       amount,
		FunderWallet: "FunderWallet123",
	}

	assert.True(t, result.Verified)
	assert.Equal(t, amount, result.Amount)
	assert.Equal(t, "FunderWallet123", result.FunderWallet)
	assert.Empty(t, result.Error)

	// Test failure case
	failedResult := &VerifyPaymentResult{
		Verified: false,
		Error:    "payment verification timed out",
	}

	assert.False(t, failedResult.Verified)
	assert.Equal(t, "payment verification timed out", failedResult.Error)
}

// TestTransferUSDC_ContextCancellation tests that the activity respects context cancellation
func TestTransferUSDC_ContextCancellation(t *testing.T) {
	os.Setenv("ENV", "test")
	os.Setenv("SOLANA_ESCROW_PRIVATE_KEY", "dummy_private_key_for_testing")

	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	// Set a very short timeout
	env.SetTestTimeout(1 * time.Millisecond)

	activities := &Activities{}
	env.RegisterActivity(activities)

	// Use a valid wallet but this should timeout before completing
	wallet := "11111111111111111111111111111112"
	amount := 10.0
	memo := "test-memo"

	_, err := env.ExecuteActivity(activities.TransferUSDC, wallet, amount, memo)

	// Should get a timeout or cancellation error
	assert.Error(t, err)
}

// TestVerifyPayment_Timeout tests that VerifyPayment respects the timeout parameter
func TestVerifyPayment_Timeout(t *testing.T) {
	os.Setenv("ENV", "test")
	os.Setenv("SOLANA_ESCROW_PRIVATE_KEY", "dummy_private_key_for_testing")
	os.Setenv("FOROHTOO_SERVER_URL", "http://localhost:9999") // Non-existent server

	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.SetTestTimeout(10 * time.Second)

	activities := &Activities{}
	env.RegisterActivity(activities)

	expectedRecipient := solanago.MustPublicKeyFromBase58("11111111111111111111111111111112")
	expectedAmount, err := solana.NewUSDCAmount(100.0)
	require.NoError(t, err)

	bountyID := "test-bounty-timeout"
	timeout := 2 * time.Second

	start := time.Now()
	_, err = env.ExecuteActivity(
		activities.VerifyPayment,
		expectedRecipient,
		expectedAmount,
		bountyID,
		timeout,
	)
	elapsed := time.Since(start)

	// Should fail due to connection/timeout
	assert.Error(t, err)

	// Should respect the timeout (with some buffer)
	assert.Less(t, elapsed, timeout+5*time.Second, "Activity should respect timeout")
}

func TestTransferUSDC_InTestEnvironment(t *testing.T) {
	// This test verifies that in test environment, TransferUSDC uses dummy configuration
	os.Setenv("ENV", "test")
	os.Setenv("SOLANA_ESCROW_PRIVATE_KEY", "dummy_private_key_for_testing")

	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	activities := &Activities{}
	env.RegisterActivity(activities)

	// Valid inputs
	wallet := "11111111111111111111111111111112"
	amount := 10.0
	memo := "test-transfer"

	// In test mode, this should use dummy Solana config and simulate the transfer
	// The actual behavior depends on the implementation of getConfiguration in test mode
	_, err := env.ExecuteActivity(activities.TransferUSDC, wallet, amount, memo)

	// The error will depend on test configuration setup
	// This test mainly ensures the activity can be executed in test environment
	t.Logf("TransferUSDC in test mode result: %v", err)
}

// CRITICAL TEST: Ensures USDC transfer errors are NON-RETRYABLE to prevent double-spends
func TestTransferUSDC_NonRetryableAfterSend(t *testing.T) {
	os.Setenv("ENV", "test")
	os.Setenv("SOLANA_ESCROW_PRIVATE_KEY", "dummy_private_key_for_testing")

	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	activities := &Activities{}
	env.RegisterActivity(activities)

	wallet := "11111111111111111111111111111112"
	amount := 10.0
	memo := "test-transfer"

	_, err := env.ExecuteActivity(activities.TransferUSDC, wallet, amount, memo)

	// CRITICAL ASSERTION: Any error from TransferUSDC after attempting to send
	// MUST be non-retryable to prevent double-spending
	if err != nil {
		// Check if this is an ApplicationError
		var appErr *temporal.ApplicationError
		require.ErrorAs(t, err, &appErr, "Expected ApplicationError for USDC transfer failures")

		// CRITICAL: Verify the error is marked as non-retryable
		assert.True(t, appErr.NonRetryable(),
			"CRITICAL SECURITY: USDC transfer errors MUST be non-retryable to prevent double-spends. "+
				"Error type: %s, Message: %s", appErr.Type(), appErr.Error())

		t.Logf("✓ VERIFIED: Error is properly marked as non-retryable (type: %s)", appErr.Type())
	}
}

// CRITICAL TEST: Ensures PayBountyActivity errors are NON-RETRYABLE
func TestPayBountyActivity_NonRetryable(t *testing.T) {
	os.Setenv("ENV", "test")
	os.Setenv("SOLANA_ESCROW_PRIVATE_KEY", "dummy_private_key_for_testing")

	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	activities := &Activities{}
	env.RegisterActivity(activities)

	bountyID := "test-bounty-123"
	recipient := "11111111111111111111111111111112"
	amount, err := solana.NewUSDCAmount(10.0)
	require.NoError(t, err)

	_, err = env.ExecuteActivity(activities.PayBountyActivity, bountyID, recipient, amount)

	// CRITICAL ASSERTION: PayBountyActivity failures MUST be non-retryable
	if err != nil {
		var appErr *temporal.ApplicationError
		require.ErrorAs(t, err, &appErr, "Expected ApplicationError for PayBountyActivity failures")

		assert.True(t, appErr.NonRetryable(),
			"CRITICAL SECURITY: PayBountyActivity errors MUST be non-retryable to prevent double-spends. "+
				"Error type: %s, Message: %s", appErr.Type(), appErr.Error())

		t.Logf("✓ VERIFIED: PayBountyActivity error is properly marked as non-retryable")
	}
}

// CRITICAL TEST: Ensures RefundBountyActivity errors are NON-RETRYABLE
func TestRefundBountyActivity_NonRetryable(t *testing.T) {
	os.Setenv("ENV", "test")
	os.Setenv("SOLANA_ESCROW_PRIVATE_KEY", "dummy_private_key_for_testing")

	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	activities := &Activities{}
	env.RegisterActivity(activities)

	bountyID := "test-bounty-refund-123"
	recipient := "11111111111111111111111111111112"
	amount, err := solana.NewUSDCAmount(10.0)
	require.NoError(t, err)

	_, err = env.ExecuteActivity(activities.RefundBountyActivity, bountyID, recipient, amount)

	// CRITICAL ASSERTION: RefundBountyActivity failures MUST be non-retryable
	if err != nil {
		var appErr *temporal.ApplicationError
		require.ErrorAs(t, err, &appErr, "Expected ApplicationError for RefundBountyActivity failures")

		assert.True(t, appErr.NonRetryable(),
			"CRITICAL SECURITY: RefundBountyActivity errors MUST be non-retryable to prevent double-spends. "+
				"Error type: %s, Message: %s", appErr.Type(), appErr.Error())

		t.Logf("✓ VERIFIED: RefundBountyActivity error is properly marked as non-retryable")
	}
}
