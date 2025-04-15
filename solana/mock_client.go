package solana

import (
	"context"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/stretchr/testify/mock"
)

// MockRPCClient is a mock implementation of RPCClient for testing
type MockRPCClient struct {
	mock.Mock
}

// GetMinimumBalanceForRentExemption mocks the GetMinimumBalanceForRentExemption method
func (m *MockRPCClient) GetMinimumBalanceForRentExemption(ctx context.Context, size uint64, commitment rpc.CommitmentType) (uint64, error) {
	args := m.Called(ctx, size, commitment)
	return args.Get(0).(uint64), args.Error(1)
}

// GetAccountInfo mocks the GetAccountInfo method
func (m *MockRPCClient) GetAccountInfo(ctx context.Context, account solana.PublicKey) (*rpc.GetAccountInfoResult, error) {
	args := m.Called(ctx, account)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*rpc.GetAccountInfoResult), args.Error(1)
}

// SendTransaction mocks the SendTransaction method
func (m *MockRPCClient) SendTransaction(ctx context.Context, tx *solana.Transaction) (solana.Signature, error) {
	args := m.Called(ctx, tx)
	return args.Get(0).(solana.Signature), args.Error(1)
}

// MockWSClient is a mock implementation of WSClient for testing
type MockWSClient struct {
	mock.Mock
}

// Connect mocks the Connect method
func (m *MockWSClient) Connect(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

// Close mocks the Close method
func (m *MockWSClient) Close() error {
	args := m.Called()
	return args.Error(0)
}

// MockSolanaClient is a mock implementation of SolanaClient for testing
type MockSolanaClient struct {
	mock.Mock
	config SolanaConfig
	rpc    *MockRPCClient
	ws     *MockWSClient
}

// NewMockSolanaClient creates a new mock Solana client
func NewMockSolanaClient(config SolanaConfig) *MockSolanaClient {
	mockClient := &MockSolanaClient{
		config: config,
		rpc:    &MockRPCClient{},
		ws:     &MockWSClient{},
	}

	// Set up default mock behavior for WebSocket connection
	mockClient.ws.On("Connect", mock.Anything).Return(nil)
	mockClient.ws.On("Close").Return(nil)

	return mockClient
}

// GetUSDCBalance mocks the GetUSDCBalance method
func (m *MockSolanaClient) GetUSDCBalance(ctx context.Context, account solana.PublicKey) (*USDCAmount, error) {
	args := m.Called(ctx, account)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*USDCAmount), args.Error(1)
}

// TransferUSDC mocks the TransferUSDC method
func (m *MockSolanaClient) TransferUSDC(ctx context.Context, from, to solana.PublicKey, amount *USDCAmount) error {
	args := m.Called(ctx, from, to, amount)
	return args.Error(0)
}

// ReleaseEscrow mocks the ReleaseEscrow method
func (m *MockSolanaClient) ReleaseEscrow(ctx context.Context, to solana.PublicKey, amount *USDCAmount) error {
	args := m.Called(ctx, to, amount)
	return args.Error(0)
}

// CreateTokenAccount mocks the CreateTokenAccount method
func (m *MockSolanaClient) CreateTokenAccount(ctx context.Context, owner solana.PublicKey) (solana.PublicKey, error) {
	args := m.Called(ctx, owner)
	if args.Get(0) == nil {
		return solana.PublicKey{}, args.Error(1)
	}
	return args.Get(0).(solana.PublicKey), args.Error(1)
}

// EscrowUSDC mocks the EscrowUSDC method
func (m *MockSolanaClient) EscrowUSDC(ctx context.Context, from solana.PublicKey, amount *USDCAmount) error {
	args := m.Called(ctx, from, amount)
	return args.Error(0)
}
