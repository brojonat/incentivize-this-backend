package solana

import (
	"context"
	"testing"

	"github.com/gagliardetto/solana-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestMockSolanaClient(t *testing.T) {
	// Create test configuration
	escrowKey := solana.NewWallet().PrivateKey
	escrowAccount := solana.NewWallet().PublicKey()

	config := SolanaConfig{
		RPCEndpoint:        "https://api.testnet.solana.com",
		WSEndpoint:         "wss://api.testnet.solana.com",
		EscrowPrivateKey:   &escrowKey,
		EscrowTokenAccount: escrowAccount,
	}

	// Create mock client
	mockClient := NewMockSolanaClient(config)

	// Test GetUSDCBalance
	balance, err := NewUSDCAmount(100.0)
	assert.NoError(t, err)

	mockClient.On("GetUSDCBalance", mock.Anything, mock.Anything).Return(balance, nil)
	result, err := mockClient.GetUSDCBalance(context.Background(), solana.NewWallet().PublicKey())
	assert.NoError(t, err)
	assert.Equal(t, balance, result)

	// Test TransferUSDC
	mockClient.On("TransferUSDC", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	err = mockClient.TransferUSDC(context.Background(), solana.NewWallet().PublicKey(), solana.NewWallet().PublicKey(), balance)
	assert.NoError(t, err)

	// Test ReleaseEscrow
	mockClient.On("ReleaseEscrow", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	err = mockClient.ReleaseEscrow(context.Background(), solana.NewWallet().PublicKey(), balance)
	assert.NoError(t, err)

	// Test CreateTokenAccount
	newAccount := solana.NewWallet().PublicKey()
	mockClient.On("CreateTokenAccount", mock.Anything, mock.Anything).Return(newAccount, nil)
	account, err := mockClient.CreateTokenAccount(context.Background(), solana.NewWallet().PublicKey())
	assert.NoError(t, err)
	assert.Equal(t, newAccount, account)

	// Test EscrowUSDC
	mockClient.On("EscrowUSDC", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	err = mockClient.EscrowUSDC(context.Background(), solana.NewWallet().PublicKey(), balance)
	assert.NoError(t, err)
}
