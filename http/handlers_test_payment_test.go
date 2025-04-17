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
	escrowTokenAccount := "DRpbCBMxVnDK7maPM5tPv6dpHGZPWQVr7zr7DgRv9YTB"

	os.Setenv("SOLANA_ESCROW_PRIVATE_KEY", escrowPrivateKey)
	os.Setenv("SOLANA_ESCROW_TOKEN_ACCOUNT", escrowTokenAccount)
	os.Setenv("SOLANA_RPC_ENDPOINT", "https://api.testnet.solana.com")
	os.Setenv("SOLANA_WS_ENDPOINT", "wss://api.testnet.solana.com")

	// Create logger
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// Create mock temporal client
	mockClient := &mocks.Client{}
	mockWorkflowRun := &mocks.WorkflowRun{}

	// Test both with and without FromAccount specified
	testCases := []struct {
		name           string
		request        PayBountyRequest
		expectedSource string
	}{
		{
			name: "with_from_account_specified",
			request: PayBountyRequest{
				UserID:      "test-user",
				FromAccount: "8jJYwDLBx9fPNcKbj6cJoNXeGMWKPhDxcnf7ahrtHX2Z", // Custom source account
				ToAccount:   "8dUmBqpvjqJvXKxdbhWDtWgYz6tNQzqbT6hF4Vz1Vy8h",
				Amount:      10.0,
			},
			expectedSource: "8jJYwDLBx9fPNcKbj6cJoNXeGMWKPhDxcnf7ahrtHX2Z",
		},
		{
			name: "without_from_account",
			request: PayBountyRequest{
				UserID:    "test-user",
				ToAccount: "8dUmBqpvjqJvXKxdbhWDtWgYz6tNQzqbT6hF4Vz1Vy8h",
				Amount:    10.0,
			},
			expectedSource: escrowTokenAccount, // Should default to escrow account
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Set up expectations
			mockClient.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.MatchedBy(func(input abb.PayBountyWorkflowInput) bool {
				// Verify input parameters match our request
				amountMatch := input.Amount.ToUSDC() == tc.request.Amount
				fromMatch := input.FromAccount == tc.expectedSource
				toMatch := input.ToAccount == tc.request.ToAccount
				return amountMatch && fromMatch && toMatch
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
			assert.Contains(t, resp.Message, tc.expectedSource)
			assert.Contains(t, resp.Message, tc.request.ToAccount)
		})
	}

	// Verify mock expectations were met
	mockClient.AssertExpectations(t)
	mockWorkflowRun.AssertExpectations(t)
}
