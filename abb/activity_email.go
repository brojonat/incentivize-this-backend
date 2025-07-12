package abb

import (
	"context"
	"fmt"
	"net/smtp"
	"os"

	"go.temporal.io/sdk/activity"
)

// EmailConfig holds configuration for sending emails.
type EmailConfig struct {
	Provider string `json:"provider"` // e.g., "sendgrid", "ses"
	APIKey   string `json:"api_key"`
	Sender   string `json:"sender"` // The 'From' email address
}

// SendTokenEmail sends an email containing a token to the specified address.
// This activity reads its required env vars directly.
func (a *Activities) SendTokenEmail(ctx context.Context, email string, token string) error {
	logger := activity.GetLogger(ctx)
	logger.Info("Sending token email", "email", email, "token", token)

	// Get SMTP server and port from env
	smtpServer := os.Getenv(EnvEmailSMTPHost)
	if smtpServer == "" {
		return fmt.Errorf("EMAIL_SMTP_HOST environment variable not set")
	}
	smtpPort := os.Getenv(EnvEmailSMTPPort)
	if smtpPort == "" {
		return fmt.Errorf("EMAIL_SMTP_PORT environment variable not set")
	}

	// Get password from env
	pwd := os.Getenv(EnvEmailPassword)
	if pwd == "" {
		return fmt.Errorf("EMAIL_PASSWORD environment variable not set")
	}

	// Get sender email from env
	sender := os.Getenv(EnvEmailSender)
	if sender == "" {
		return fmt.Errorf("EMAIL_SENDER environment variable not set")
	}

	// Send using vanilla smtp client
	auth := smtp.PlainAuth("", sender, pwd, smtpServer)
	msg := []byte("To: " + email + "\r\n" +
		"Subject: Your IncentivizeThis Token\r\n" +
		"\r\n" +
		"Your token is: " + token + "\r\n")
	err := smtp.SendMail(smtpServer+":"+smtpPort, auth, sender, []string{email}, msg)
	if err != nil {
		return fmt.Errorf("failed to send email: %w", err)
	}

	return nil
}

func (a *Activities) SendContactUsEmail(ctx context.Context, input ContactUsNotifyWorkflowInput) error {
	logger := activity.GetLogger(ctx)
	logger.Info("Sending contact us email")

	// Get SMTP server and port from env
	smtpServer := os.Getenv(EnvEmailSMTPHost)
	if smtpServer == "" {
		return fmt.Errorf("EMAIL_SMTP_HOST environment variable not set")
	}
	smtpPort := os.Getenv(EnvEmailSMTPPort)
	if smtpPort == "" {
		return fmt.Errorf("EMAIL_SMTP_PORT environment variable not set")
	}

	// Get password from env
	pwd := os.Getenv(EnvEmailPassword)
	if pwd == "" {
		return fmt.Errorf("EMAIL_PASSWORD environment variable not set")
	}

	// Get sender email from env
	sender := os.Getenv(EnvEmailSender)
	if sender == "" {
		return fmt.Errorf("EMAIL_SENDER environment variable not set")
	}

	// Get recipient email from env. The user wants to email HIMSELF.
	recipient := os.Getenv(EnvEmailAdminRecipient)
	if recipient == "" {
		return fmt.Errorf("EMAIL_ADMIN_RECIPIENT environment variable not set")
	}

	// Send using vanilla smtp client
	auth := smtp.PlainAuth("", sender, pwd, smtpServer)
	subject := "New Contact Us Submission"
	body := fmt.Sprintf("You have a new contact us submission:\n\nName: %s\nEmail: %s\nMessage: %s", input.Name, input.Email, input.Message)
	msg := []byte("To: " + recipient + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"\r\n" +
		body + "\r\n")
	err := smtp.SendMail(smtpServer+":"+smtpPort, auth, sender, []string{recipient}, msg)
	if err != nil {
		return fmt.Errorf("failed to send contact us email: %w", err)
	}

	return nil
}
