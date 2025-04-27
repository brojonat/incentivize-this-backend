package http

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/brojonat/affiliate-bounty-board/abb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/mocks"
)

func TestHandlePayBounty(t *testing.T) {
	// Set up environment variables
	escrowPrivateKey := "4rQanLjnJBqLfvK1yQXWFohkXY6tCTnoCWVbQfRKEBQAQYmMYHR6eGUNzFQ8hbLuGZJA1PLHUMkwtRhXpNigmz7M"
	escrowWallet := "DRpbCBMxVnDK7maPM5tPv6dpHGZPWQVr7zr7DgRv9YTB"

	os.Setenv(EnvSolanaEscrowPrivateKey, escrowPrivateKey)
	os.Setenv(EnvSolanaEscrowWallet, escrowWallet)
	os.Setenv(EnvSolanaRPCEndpoint, "https://api.testnet.solana.com")
	os.Setenv(EnvSolanaWSEndpoint, "wss://api.testnet.solana.com")
	os.Setenv(EnvSolanaUSDCMintAddress, "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v")

	// Create logger
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// Test cases now only need amount and wallet
	testCases := []struct {
		name    string
		request PayBountyRequest
	}{
		{
			name: "valid payment request",
			request: PayBountyRequest{
				Amount: 10.0,
				Wallet: "8dUmBqpvjqJvXKxdbhWDtWgYz6tNQzqbT6hF4Vz1Vy8h",
			},
		},
		// Add more cases if needed, e.g., invalid wallet, zero amount (handled by handler validation)
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Reset mocks for each test case if necessary (using testify/mock)
			mockClient := &mocks.Client{}
			mockWorkflowRun := &mocks.WorkflowRun{}

			// Set up expectations
			mockClient.On("ExecuteWorkflow",
				mock.Anything, // context
				mock.Anything, // options
				mock.AnythingOfType("func(internal.Context, abb.PayBountyWorkflowInput) error"), // workflow func type
				mock.MatchedBy(func(input abb.PayBountyWorkflowInput) bool { // input
					// Verify input parameters match our request
					amountMatch := input.Amount.ToUSDC() == tc.request.Amount
					walletMatch := input.Wallet == tc.request.Wallet
					return amountMatch && walletMatch
				})).Return(mockWorkflowRun, nil).Once()

			mockWorkflowRun.On("Get", mock.Anything, nil).Return(nil).Once()

			// Create handler
			handler := handlePayBounty(logger, mockClient)

			// Encode request to JSON
			reqBody, err := json.Marshal(tc.request)
			require.NoError(t, err)

			// Create request
			httpReq, err := http.NewRequest("POST", "/bounties/pay", bytes.NewBuffer(reqBody))
			require.NoError(t, err)
			httpReq.Header.Set("Content-Type", "application/json")

			// Create response recorder
			rr := httptest.NewRecorder()

			// Call handler
			handler(rr, httpReq)

			// Verify response
			assert.Equal(t, http.StatusOK, rr.Code)

			// Verify response body
			var resp DefaultJSONResponse
			err = json.NewDecoder(rr.Body).Decode(&resp)
			require.NoError(t, err)

			// Verify the message contains expected elements
			assert.Contains(t, resp.Message, "Successfully executed payment")
			assert.Contains(t, resp.Message, "10 USDC")
			// Removed check for source account
			assert.Contains(t, resp.Message, tc.request.Wallet)

			// Verify mock expectations were met for this test case
			mockClient.AssertExpectations(t)
			mockWorkflowRun.AssertExpectations(t)
		})
	}

	// No overall AssertExpectations needed here as mocks are created per test case
}
