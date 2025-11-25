package http

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/skip2/go-qrcode"
	solanago "github.com/gagliardetto/solana-go"
)

// PaymentInvoice represents payment information for funding a bounty
type PaymentInvoice struct {
	BountyID                string    `json:"bounty_id"`
	PayToAddress            string    `json:"pay_to_address"`
	USDCMint                string    `json:"usdc_mint"`
	Amount                  float64   `json:"amount"`                   // Human-readable USDC amount
	Memo                    string    `json:"memo"`                     // Required in payment txn (workflow ID)
	ExpiresAt               time.Time `json:"expires_at"`               // Payment deadline
	PaymentURL              string    `json:"payment_url"`              // Solana Pay URL for wallet apps
	QRCodeData              string    `json:"qr_code_data"`             // Base64 encoded QR code image
}

// generatePaymentInvoice creates a payment invoice for bounty funding.
// The memo is set to the workflow ID (bountyID) so the payment can be tracked.
func generatePaymentInvoice(
	escrowWallet solanago.PublicKey,
	usdcMint solanago.PublicKey,
	amount float64,
	bountyID string,
	paymentTimeout time.Duration,
) (PaymentInvoice, error) {
	memo := bountyID // The workflow ID is used as the memo
	now := time.Now().UTC()

	// Build Solana Pay URL for USDC payment
	paymentURL := buildSolanaPayURL(
		escrowWallet.String(),
		amount,
		usdcMint.String(),
		memo,
	)

	// Generate QR code
	qrCodeData, err := generateQRCode(paymentURL)
	if err != nil {
		return PaymentInvoice{}, fmt.Errorf("failed to generate QR code: %w", err)
	}

	return PaymentInvoice{
		BountyID:     bountyID,
		PayToAddress: escrowWallet.String(),
		USDCMint:     usdcMint.String(),
		Amount:       amount,
		Memo:         memo,
		ExpiresAt:    now.Add(paymentTimeout),
		PaymentURL:   paymentURL,
		QRCodeData:   qrCodeData,
	}, nil
}

// buildSolanaPayURL creates a Solana Pay-compatible URL for USDC payment.
// Format: solana:{recipient}?amount={amount}&spl-token={usdcMint}&memo={memo}
// See: https://docs.solanapay.com/spec#transfer-request
func buildSolanaPayURL(recipient string, amount float64, usdcMint, memo string) string {
	// Format amount without trailing zeros for better wallet compatibility
	amountStr := formatAmount(amount)

	// Build URL manually to ensure proper encoding
	// Note: We only URL-encode values that need it, to maximize wallet compatibility
	params := url.Values{}
	params.Set("amount", amountStr)
	params.Set("spl-token", usdcMint)

	// Add memo for transaction tracking
	if memo != "" {
		params.Set("memo", memo)
	}

	return fmt.Sprintf("solana:%s?%s", recipient, params.Encode())
}

// formatAmount formats a float amount, stripping unnecessary trailing zeros
// Examples: 1.0 -> "1", 1.5 -> "1.5", 0.000001 -> "0.000001"
func formatAmount(amount float64) string {
	// Format with 6 decimal places (USDC precision)
	s := fmt.Sprintf("%.6f", amount)
	// Strip trailing zeros
	s = strings.TrimRight(s, "0")
	// Strip trailing decimal point if no decimals remain
	s = strings.TrimRight(s, ".")
	return s
}

// generateQRCode creates a QR code image from a payment URL and returns it as base64-encoded PNG.
func generateQRCode(data string) (string, error) {
	// Generate QR code with medium error correction
	qr, err := qrcode.New(data, qrcode.Medium)
	if err != nil {
		return "", fmt.Errorf("failed to create QR code: %w", err)
	}

	// Encode as PNG (256x256 pixels)
	png, err := qr.PNG(256)
	if err != nil {
		return "", fmt.Errorf("failed to encode QR code as PNG: %w", err)
	}

	// Return base64-encoded PNG for easy embedding in JSON/HTML
	return base64.StdEncoding.EncodeToString(png), nil
}

