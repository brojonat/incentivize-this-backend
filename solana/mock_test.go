package solana

import (
	"context"
	"encoding/binary"
	"testing"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/gagliardetto/solana-go/rpc/ws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockRPCClient is a mock implementation of the RPC client
type MockRPCClient struct {
	*rpc.Client
	mock.Mock
}

func (m *MockRPCClient) GetMinimumBalanceForRentExemption(ctx context.Context, size uint64, commitment rpc.CommitmentType) (uint64, error) {
	args := m.Called(ctx, size, commitment)
	return args.Get(0).(uint64), args.Error(1)
}

func (m *MockRPCClient) GetAccountInfo(ctx context.Context, account solana.PublicKey) (*rpc.GetAccountInfoResult, error) {
	args := m.Called(ctx, account)
	return args.Get(0).(*rpc.GetAccountInfoResult), args.Error(1)
}

func (m *MockRPCClient) SendTransaction(ctx context.Context, tx *solana.Transaction) (solana.Signature, error) {
	args := m.Called(ctx, tx)
	return args.Get(0).(solana.Signature), args.Error(1)
}

// MockWSClient is a mock implementation of the WS client
type MockWSClient struct {
	*ws.Client
	mock.Mock
}

func TestNewSolanaClient(t *testing.T) {
	tests := []struct {
		name    string
		config  SolanaConfig
		wantErr bool
	}{
		{
			name: "valid config",
			config: SolanaConfig{
				RPCEndpoint:        "https://api.mainnet-beta.solana.com",
				WSEndpoint:         "wss://api.mainnet-beta.solana.com",
				EscrowPrivateKey:   &solana.NewWallet().PrivateKey,
				EscrowTokenAccount: solana.NewWallet().PublicKey(),
			},
			wantErr: false,
		},
		{
			name: "missing RPC endpoint",
			config: SolanaConfig{
				WSEndpoint:         "wss://api.mainnet-beta.solana.com",
				EscrowPrivateKey:   &solana.NewWallet().PrivateKey,
				EscrowTokenAccount: solana.NewWallet().PublicKey(),
			},
			wantErr: true,
		},
		{
			name: "missing WS endpoint",
			config: SolanaConfig{
				RPCEndpoint:        "https://api.mainnet-beta.solana.com",
				EscrowPrivateKey:   &solana.NewWallet().PrivateKey,
				EscrowTokenAccount: solana.NewWallet().PublicKey(),
			},
			wantErr: true,
		},
		{
			name: "missing escrow private key",
			config: SolanaConfig{
				RPCEndpoint:        "https://api.mainnet-beta.solana.com",
				WSEndpoint:         "wss://api.mainnet-beta.solana.com",
				EscrowTokenAccount: solana.NewWallet().PublicKey(),
			},
			wantErr: true,
		},
		{
			name: "missing escrow token account",
			config: SolanaConfig{
				RPCEndpoint:      "https://api.mainnet-beta.solana.com",
				WSEndpoint:       "wss://api.mainnet-beta.solana.com",
				EscrowPrivateKey: &solana.NewWallet().PrivateKey,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewSolanaClient(tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewSolanaClient() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && client == nil {
				t.Error("NewSolanaClient() returned nil")
			}
		})
	}
}

func TestCreateTokenAccount(t *testing.T) {
	// Create mock clients
	mockRPC := new(MockRPCClient)
	mockWS := new(MockWSClient)

	// Create test client
	config := SolanaConfig{
		RPCEndpoint:        "https://api.mainnet-beta.solana.com",
		WSEndpoint:         "wss://api.mainnet-beta.solana.com",
		EscrowPrivateKey:   &solana.NewWallet().PrivateKey,
		EscrowTokenAccount: solana.NewWallet().PublicKey(),
	}
	client := &solanaClient{
		config: config,
		rpc:    mockRPC,
		ws:     mockWS,
	}

	// Test data
	owner := solana.NewWallet().PublicKey()
	rent := uint64(1000)

	// Set up expectations
	mockRPC.On("GetMinimumBalanceForRentExemption", mock.Anything, uint64(165), rpc.CommitmentFinalized).
		Return(rent, nil)

	mockRPC.On("SendTransaction", mock.Anything, mock.AnythingOfType("*solana.Transaction")).
		Return(solana.Signature{}, nil)

	// Execute test
	ctx := context.Background()
	account, err := client.CreateTokenAccount(ctx, owner)

	// Verify results
	assert.NoError(t, err)
	assert.NotZero(t, account)
	mockRPC.AssertExpectations(t)
}

func TestTransferUSDC(t *testing.T) {
	// Create mock clients
	mockRPC := new(MockRPCClient)
	mockWS := new(MockWSClient)

	// Create test client
	config := SolanaConfig{
		RPCEndpoint:        "https://api.mainnet-beta.solana.com",
		WSEndpoint:         "wss://api.mainnet-beta.solana.com",
		EscrowPrivateKey:   &solana.NewWallet().PrivateKey,
		EscrowTokenAccount: solana.NewWallet().PublicKey(),
	}
	client := &solanaClient{
		config: config,
		rpc:    mockRPC,
		ws:     mockWS,
	}

	// Test data
	from := solana.NewWallet().PublicKey()
	to := solana.NewWallet().PublicKey()
	amount, _ := NewUSDCAmount(1.5)

	// Set up expectations
	mockRPC.On("SendTransaction", mock.Anything, mock.AnythingOfType("*solana.Transaction")).
		Return(solana.Signature{}, nil)

	// Execute test
	ctx := context.Background()
	err := client.TransferUSDC(ctx, from, to, amount)

	// Verify results
	assert.NoError(t, err)
	mockRPC.AssertExpectations(t)
}

func TestGetUSDCBalance(t *testing.T) {
	// Create mock clients
	mockRPC := new(MockRPCClient)
	mockWS := new(MockWSClient)

	// Create test client
	config := SolanaConfig{
		RPCEndpoint:        "https://api.mainnet-beta.solana.com",
		WSEndpoint:         "wss://api.mainnet-beta.solana.com",
		EscrowPrivateKey:   &solana.NewWallet().PrivateKey,
		EscrowTokenAccount: solana.NewWallet().PublicKey(),
	}
	client := &solanaClient{
		config: config,
		rpc:    mockRPC,
		ws:     mockWS,
	}

	// Test data
	account := solana.NewWallet().PublicKey()
	balance := uint64(1500000) // 1.5 USDC in smallest unit

	// Create mock account data
	accountData := make([]byte, 165)
	binary.LittleEndian.PutUint64(accountData[64:72], balance)

	// Set up expectations
	mockRPC.On("GetAccountInfo", mock.Anything, account).
		Return(&rpc.GetAccountInfoResult{
			Value: &rpc.Account{
				Data: rpc.DataBytesOrJSONFromBytes(accountData),
			},
		}, nil)

	// Execute test
	ctx := context.Background()
	usdcAmount, err := client.GetUSDCBalance(ctx, account)

	// Verify results
	assert.NoError(t, err)
	assert.NotNil(t, usdcAmount)
	assert.Equal(t, float64(1.5), usdcAmount.ToUSDC())
	mockRPC.AssertExpectations(t)
}

func TestEscrowUSDC(t *testing.T) {
	// Create mock clients
	mockRPC := new(MockRPCClient)
	mockWS := new(MockWSClient)

	// Create test client
	config := SolanaConfig{
		RPCEndpoint:        "https://api.mainnet-beta.solana.com",
		WSEndpoint:         "wss://api.mainnet-beta.solana.com",
		EscrowPrivateKey:   &solana.NewWallet().PrivateKey,
		EscrowTokenAccount: solana.NewWallet().PublicKey(),
	}
	client := &solanaClient{
		config: config,
		rpc:    mockRPC,
		ws:     mockWS,
	}

	// Test data
	from := solana.NewWallet().PublicKey()
	amount, _ := NewUSDCAmount(1.5)

	// Set up expectations
	mockRPC.On("SendTransaction", mock.Anything, mock.AnythingOfType("*solana.Transaction")).
		Return(solana.Signature{}, nil)

	// Execute test
	ctx := context.Background()
	err := client.EscrowUSDC(ctx, from, amount)

	// Verify results
	assert.NoError(t, err)
	mockRPC.AssertExpectations(t)
}

func TestReleaseEscrow(t *testing.T) {
	// Create mock clients
	mockRPC := new(MockRPCClient)
	mockWS := new(MockWSClient)

	// Create test client
	config := SolanaConfig{
		RPCEndpoint:        "https://api.mainnet-beta.solana.com",
		WSEndpoint:         "wss://api.mainnet-beta.solana.com",
		EscrowPrivateKey:   &solana.NewWallet().PrivateKey,
		EscrowTokenAccount: solana.NewWallet().PublicKey(),
	}
	client := &solanaClient{
		config: config,
		rpc:    mockRPC,
		ws:     mockWS,
	}

	// Test data
	to := solana.NewWallet().PublicKey()
	amount, _ := NewUSDCAmount(1.5)

	// Set up expectations
	mockRPC.On("SendTransaction", mock.Anything, mock.AnythingOfType("*solana.Transaction")).
		Return(solana.Signature{}, nil)

	// Execute test
	ctx := context.Background()
	err := client.ReleaseEscrow(ctx, to, amount)

	// Verify results
	assert.NoError(t, err)
	mockRPC.AssertExpectations(t)
}

func TestValidateWalletAddress(t *testing.T) {
	tests := []struct {
		name    string
		address string
		wantErr bool
	}{
		{
			name:    "valid address",
			address: solana.NewWallet().PublicKey().String(),
			wantErr: false,
		},
		{
			name:    "empty address",
			address: "",
			wantErr: true,
		},
		{
			name:    "invalid address",
			address: "invalid-address",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateWalletAddress(tt.address)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateWalletAddress() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
