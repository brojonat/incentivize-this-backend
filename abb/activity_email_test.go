package abb

import (
	"os"
	"testing"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// CRITICAL TEST: Ensures email activities are non-retryable to prevent duplicate emails

func TestSendTokenEmail_ConfigErrors_NonRetryable(t *testing.T) {
	// Clear all email env vars to force configuration errors
	os.Unsetenv("EMAIL_SMTP_HOST")
	os.Unsetenv("EMAIL_SMTP_PORT")
	os.Unsetenv("EMAIL_PASSWORD")
	os.Unsetenv("EMAIL_SENDER")

	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	activities := &Activities{}
	env.RegisterActivity(activities)

	_, err := env.ExecuteActivity(activities.SendTokenEmail, "test@example.com", "test-token-123")

	// CRITICAL ASSERTION: Configuration errors must be non-retryable
	require.Error(t, err)
	var appErr *temporal.ApplicationError
	require.ErrorAs(t, err, &appErr, "Expected ApplicationError for email config failures")
	assert.True(t, appErr.NonRetryable(),
		"CRITICAL: Email configuration errors MUST be non-retryable. "+
			"Error type: %s", appErr.Type())
	assert.Equal(t, "CONFIGURATION_ERROR", appErr.Type())
}

func TestSendContactUsEmail_ConfigErrors_NonRetryable(t *testing.T) {
	// Clear all email env vars to force configuration errors
	os.Unsetenv("EMAIL_SMTP_HOST")
	os.Unsetenv("EMAIL_SMTP_PORT")
	os.Unsetenv("EMAIL_PASSWORD")
	os.Unsetenv("EMAIL_SENDER")
	os.Unsetenv("EMAIL_ADMIN_RECIPIENT")

	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	activities := &Activities{}
	env.RegisterActivity(activities)

	input := ContactUsNotifyWorkflowInput{
		Name:    "Test User",
		Email:   "user@example.com",
		Message: "Test message",
	}

	_, err := env.ExecuteActivity(activities.SendContactUsEmail, input)

	// CRITICAL ASSERTION: Configuration errors must be non-retryable
	require.Error(t, err)
	var appErr *temporal.ApplicationError
	require.ErrorAs(t, err, &appErr, "Expected ApplicationError for email config failures")
	assert.True(t, appErr.NonRetryable(),
		"CRITICAL: Email configuration errors MUST be non-retryable. "+
			"Error type: %s", appErr.Type())
	assert.Equal(t, "CONFIGURATION_ERROR", appErr.Type())
}

func TestSendBountySummaryEmail_ConfigErrors_NonRetryable(t *testing.T) {
	// Clear all email env vars to force configuration errors
	os.Unsetenv("EMAIL_SMTP_HOST")
	os.Unsetenv("EMAIL_SMTP_PORT")
	os.Unsetenv("EMAIL_PASSWORD")
	os.Unsetenv("EMAIL_SENDER")
	os.Unsetenv("EMAIL_ADMIN_RECIPIENT")

	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()

	activities := &Activities{}
	env.RegisterActivity(activities)

	summary := BountySummaryData{
		BountyID:    "test-bounty-123",
		Title:       "Test Bounty",
		FinalStatus: "completed",
	}

	_, err := env.ExecuteActivity(activities.SendBountySummaryEmail, summary)

	// CRITICAL ASSERTION: Configuration errors must be non-retryable
	require.Error(t, err)
	var appErr *temporal.ApplicationError
	require.ErrorAs(t, err, &appErr, "Expected ApplicationError for email config failures")
	assert.True(t, appErr.NonRetryable(),
		"CRITICAL: Email configuration errors MUST be non-retryable. "+
			"Error type: %s", appErr.Type())
	assert.Equal(t, "CONFIGURATION_ERROR", appErr.Type())
}

// Note: Testing actual SMTP send failures requires a mock SMTP server or integration test.
// The critical protection is that once we attempt to send (call smtp.SendMail), any error
// is marked as non-retryable. This is verified in the code with APPLICATION_ERROR type
// EMAIL_SEND_FAILED with NonRetryable: true.
