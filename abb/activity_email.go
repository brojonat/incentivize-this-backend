package abb

import (
	"context"
	"fmt"
	"net/smtp"
	"os"
	"strings"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

// EmailConfig holds configuration for sending emails.
type EmailConfig struct {
	Provider string `json:"provider"` // e.g., "sendgrid", "ses"
	APIKey   string `json:"api_key"`
	Sender   string `json:"sender"` // The 'From' email address
}

// SendTokenEmail sends an email containing a token to the specified address.
// This activity reads its required env vars directly.
// CRITICAL: This activity is non-retryable after attempting to send to prevent duplicate emails.
func (a *Activities) SendTokenEmail(ctx context.Context, email string, token string) error {
	logger := activity.GetLogger(ctx)
	logger.Info("Sending token email", "email", email, "token", token)

	// Get SMTP server and port from env
	smtpServer := os.Getenv(EnvEmailSMTPHost)
	if smtpServer == "" {
		// Configuration errors are non-retryable (won't fix themselves)
		return temporal.NewApplicationErrorWithOptions(
			"EMAIL_SMTP_HOST environment variable not set",
			"CONFIGURATION_ERROR",
			temporal.ApplicationErrorOptions{NonRetryable: true},
		)
	}
	smtpPort := os.Getenv(EnvEmailSMTPPort)
	if smtpPort == "" {
		return temporal.NewApplicationErrorWithOptions(
			"EMAIL_SMTP_PORT environment variable not set",
			"CONFIGURATION_ERROR",
			temporal.ApplicationErrorOptions{NonRetryable: true},
		)
	}

	// Get password from env
	pwd := os.Getenv(EnvEmailPassword)
	if pwd == "" {
		return temporal.NewApplicationErrorWithOptions(
			"EMAIL_PASSWORD environment variable not set",
			"CONFIGURATION_ERROR",
			temporal.ApplicationErrorOptions{NonRetryable: true},
		)
	}

	// Get sender email from env
	sender := os.Getenv(EnvEmailSender)
	if sender == "" {
		return temporal.NewApplicationErrorWithOptions(
			"EMAIL_SENDER environment variable not set",
			"CONFIGURATION_ERROR",
			temporal.ApplicationErrorOptions{NonRetryable: true},
		)
	}

	// Send using vanilla smtp client
	// CRITICAL: After attempting to send, we cannot retry because we don't know
	// if the email was delivered. Retrying could send duplicate emails.
	auth := smtp.PlainAuth("", sender, pwd, smtpServer)
	msg := []byte("To: " + email + "\r\n" +
		"Subject: Your IncentivizeThis Token\r\n" +
		"\r\n" +
		"Your token is: " + token + "\r\n")
	err := smtp.SendMail(smtpServer+":"+smtpPort, auth, sender, []string{email}, msg)
	if err != nil {
		// CRITICAL: Mark as non-retryable to prevent duplicate emails
		return temporal.NewApplicationErrorWithOptions(
			fmt.Sprintf("failed to send email: %s", err.Error()),
			"EMAIL_SEND_FAILED",
			temporal.ApplicationErrorOptions{NonRetryable: true},
		)
	}

	return nil
}

// SendContactUsEmail sends a contact us notification email to the admin.
// CRITICAL: This activity is non-retryable after attempting to send to prevent duplicate emails.
func (a *Activities) SendContactUsEmail(ctx context.Context, input ContactUsNotifyWorkflowInput) error {
	logger := activity.GetLogger(ctx)
	logger.Info("Sending contact us email")

	// Get SMTP server and port from env
	smtpServer := os.Getenv(EnvEmailSMTPHost)
	if smtpServer == "" {
		return temporal.NewApplicationErrorWithOptions(
			"EMAIL_SMTP_HOST environment variable not set",
			"CONFIGURATION_ERROR",
			temporal.ApplicationErrorOptions{NonRetryable: true},
		)
	}
	smtpPort := os.Getenv(EnvEmailSMTPPort)
	if smtpPort == "" {
		return temporal.NewApplicationErrorWithOptions(
			"EMAIL_SMTP_PORT environment variable not set",
			"CONFIGURATION_ERROR",
			temporal.ApplicationErrorOptions{NonRetryable: true},
		)
	}

	// Get password from env
	pwd := os.Getenv(EnvEmailPassword)
	if pwd == "" {
		return temporal.NewApplicationErrorWithOptions(
			"EMAIL_PASSWORD environment variable not set",
			"CONFIGURATION_ERROR",
			temporal.ApplicationErrorOptions{NonRetryable: true},
		)
	}

	// Get sender email from env
	sender := os.Getenv(EnvEmailSender)
	if sender == "" {
		return temporal.NewApplicationErrorWithOptions(
			"EMAIL_SENDER environment variable not set",
			"CONFIGURATION_ERROR",
			temporal.ApplicationErrorOptions{NonRetryable: true},
		)
	}

	// Get recipient email from env. The user wants to email HIMSELF.
	recipient := os.Getenv(EnvEmailAdminRecipient)
	if recipient == "" {
		return temporal.NewApplicationErrorWithOptions(
			"EMAIL_ADMIN_RECIPIENT environment variable not set",
			"CONFIGURATION_ERROR",
			temporal.ApplicationErrorOptions{NonRetryable: true},
		)
	}

	// Send using vanilla smtp client
	// CRITICAL: After attempting to send, we cannot retry because we don't know
	// if the email was delivered. Retrying could send duplicate emails to admin.
	auth := smtp.PlainAuth("", sender, pwd, smtpServer)
	subject := "New Contact Us Submission"
	body := fmt.Sprintf("You have a new contact us submission:\n\nName: %s\nEmail: %s\nMessage: %s", input.Name, input.Email, input.Message)
	msg := []byte("To: " + recipient + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"\r\n" +
		body + "\r\n")
	err := smtp.SendMail(smtpServer+":"+smtpPort, auth, sender, []string{recipient}, msg)
	if err != nil {
		// CRITICAL: Mark as non-retryable to prevent duplicate emails
		return temporal.NewApplicationErrorWithOptions(
			fmt.Sprintf("failed to send contact us email: %s", err.Error()),
			"EMAIL_SEND_FAILED",
			temporal.ApplicationErrorOptions{NonRetryable: true},
		)
	}

	return nil
}

// SendBountySummaryEmail sends a bounty assessment summary email to the admin.
// CRITICAL: This activity is non-retryable after attempting to send to prevent duplicate emails.
func (a *Activities) SendBountySummaryEmail(ctx context.Context, summary BountySummaryData) error {
	logger := activity.GetLogger(ctx)
	logger.Info("Sending bounty summary email")

	// Get SMTP server and port from env
	smtpServer := os.Getenv(EnvEmailSMTPHost)
	if smtpServer == "" {
		return temporal.NewApplicationErrorWithOptions(
			"EMAIL_SMTP_HOST environment variable not set",
			"CONFIGURATION_ERROR",
			temporal.ApplicationErrorOptions{NonRetryable: true},
		)
	}
	smtpPort := os.Getenv(EnvEmailSMTPPort)
	if smtpPort == "" {
		return temporal.NewApplicationErrorWithOptions(
			"EMAIL_SMTP_PORT environment variable not set",
			"CONFIGURATION_ERROR",
			temporal.ApplicationErrorOptions{NonRetryable: true},
		)
	}

	// Get password from env
	pwd := os.Getenv(EnvEmailPassword)
	if pwd == "" {
		return temporal.NewApplicationErrorWithOptions(
			"EMAIL_PASSWORD environment variable not set",
			"CONFIGURATION_ERROR",
			temporal.ApplicationErrorOptions{NonRetryable: true},
		)
	}

	// Get sender email from env
	sender := os.Getenv(EnvEmailSender)
	if sender == "" {
		return temporal.NewApplicationErrorWithOptions(
			"EMAIL_SENDER environment variable not set",
			"CONFIGURATION_ERROR",
			temporal.ApplicationErrorOptions{NonRetryable: true},
		)
	}

	// Get recipient email from env.
	recipient := os.Getenv(EnvEmailAdminRecipient)
	if recipient == "" {
		return temporal.NewApplicationErrorWithOptions(
			"EMAIL_ADMIN_RECIPIENT environment variable not set",
			"CONFIGURATION_ERROR",
			temporal.ApplicationErrorOptions{NonRetryable: true},
		)
	}

	// Send using vanilla smtp client
	// CRITICAL: After attempting to send, we cannot retry because we don't know
	// if the email was delivered. Retrying could send duplicate summary emails.
	auth := smtp.PlainAuth("", sender, pwd, smtpServer)
	subject := fmt.Sprintf("Bounty Assessment Summary: %s (%s)", summary.Title, summary.FinalStatus)

	var bodyBuilder strings.Builder
	bodyBuilder.WriteString(fmt.Sprintf("Bounty assessment for '%s' has completed.\n\n", summary.Title))
	bodyBuilder.WriteString(fmt.Sprintf("Bounty ID: %s\n", summary.BountyID))
	bodyBuilder.WriteString(fmt.Sprintf("Status: %s\n", summary.FinalStatus))

	if summary.ErrorDetails != "" {
		bodyBuilder.WriteString(fmt.Sprintf("Details: %s\n", summary.ErrorDetails))
	}

	if summary.TotalAmountPaid != nil && summary.TotalAmountPaid.IsPositive() {
		bodyBuilder.WriteString("\n--- Payouts ---\n")
		for _, p := range summary.Payouts {
			bodyBuilder.WriteString(fmt.Sprintf("  - To: %s\n", p.PayoutWallet))
			bodyBuilder.WriteString(fmt.Sprintf("    Amount: $%.2f\n", p.Amount.ToUSDC()))
			bodyBuilder.WriteString(fmt.Sprintf("    Content: %s/%s\n", p.Platform, p.ContentID))
			bodyBuilder.WriteString(fmt.Sprintf("    Timestamp: %s\n", p.Timestamp.Format(time.RFC1123)))
		}
	}

	if summary.AmountRefunded != nil && summary.AmountRefunded.IsPositive() {
		bodyBuilder.WriteString(fmt.Sprintf("\nRefunded to owner: $%.2f\n", summary.AmountRefunded.ToUSDC()))
	}

	msg := []byte("To: " + recipient + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"\r\n" +
		bodyBuilder.String() + "\r\n")
	err := smtp.SendMail(smtpServer+":"+smtpPort, auth, sender, []string{recipient}, msg)
	if err != nil {
		// CRITICAL: Mark as non-retryable to prevent duplicate emails
		return temporal.NewApplicationErrorWithOptions(
			fmt.Sprintf("failed to send bounty summary email: %s", err.Error()),
			"EMAIL_SEND_FAILED",
			temporal.ApplicationErrorOptions{NonRetryable: true},
		)
	}

	return nil
}
