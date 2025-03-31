package solana

import (
	"context"
	"encoding/binary"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/gagliardetto/solana-go/rpc/ws"
)

// USDC token mint address on Solana mainnet
const USDC_MINT = "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"

// USDC constants
const (
	USDC_DECIMALS   = 6
	USDC_MULTIPLIER = 1_000_000 // 10^6
)

// USDCAmount represents a USDC amount in its smallest unit
type USDCAmount struct {
	Value *big.Int
}

// NewUSDCAmount creates a new USDC amount from a float
func NewUSDCAmount(amount float64) (*USDCAmount, error) {
	// Convert to string with 6 decimal places to avoid floating point issues
	str := fmt.Sprintf("%.6f", amount)

	// Split into whole and decimal parts
	parts := strings.Split(str, ".")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid amount format")
	}

	// Combine parts and convert to big.Int
	combined := parts[0] + parts[1]
	value := new(big.Int)
	value.SetString(combined, 10)

	return &USDCAmount{Value: value}, nil
}

// ToSmallestUnit returns the amount in micro-USDC
func (a *USDCAmount) ToSmallestUnit() *big.Int {
	return a.Value
}

// ToUSDC returns the amount as a float64 (for display only)
func (a *USDCAmount) ToUSDC() float64 {
	str := a.Value.String()
	if len(str) <= USDC_DECIMALS {
		str = strings.Repeat("0", USDC_DECIMALS-len(str)+1) + str
	}
	whole := str[:len(str)-USDC_DECIMALS]
	decimal := str[len(str)-USDC_DECIMALS:]
	result, _ := strconv.ParseFloat(whole+"."+decimal, 64)
	return result
}

// Zero returns a new USDCAmount with value 0
func Zero() *USDCAmount {
	return &USDCAmount{Value: new(big.Int)}
}

// Add adds two USDC amounts
func (a *USDCAmount) Add(b *USDCAmount) *USDCAmount {
	if a == nil || b == nil {
		return nil
	}
	result := new(big.Int)
	result.Add(a.Value, b.Value)
	return &USDCAmount{Value: result}
}

// Sub subtracts two USDC amounts
func (a *USDCAmount) Sub(b *USDCAmount) *USDCAmount {
	if a == nil || b == nil {
		return nil
	}
	result := new(big.Int)
	result.Sub(a.Value, b.Value)
	return &USDCAmount{Value: result}
}

// Cmp compares two USDC amounts
func (a *USDCAmount) Cmp(b *USDCAmount) int {
	if a == nil || b == nil {
		return 0
	}
	return a.Value.Cmp(b.Value)
}

// IsZero returns true if the amount is zero
func (a *USDCAmount) IsZero() bool {
	if a == nil || a.Value == nil {
		return true
	}
	return a.Value.Sign() == 0
}

// IsNegative returns true if the amount is negative
func (a *USDCAmount) IsNegative() bool {
	if a == nil || a.Value == nil {
		return false
	}
	return a.Value.Sign() < 0
}

// IsPositive returns true if the amount is positive
func (a *USDCAmount) IsPositive() bool {
	if a == nil || a.Value == nil {
		return false
	}
	return a.Value.Sign() > 0
}

// SolanaConfig holds configuration for Solana operations
type SolanaConfig struct {
	RPCEndpoint        string
	WSEndpoint         string
	EscrowPrivateKey   *solana.PrivateKey
	EscrowTokenAccount solana.PublicKey
}

// RPCClient interface defines the methods we need from the RPC client
type RPCClient interface {
	GetMinimumBalanceForRentExemption(ctx context.Context, size uint64, commitment rpc.CommitmentType) (uint64, error)
	GetAccountInfo(ctx context.Context, account solana.PublicKey) (*rpc.GetAccountInfoResult, error)
	SendTransaction(ctx context.Context, tx *solana.Transaction) (solana.Signature, error)
}

// WSClient interface defines the methods we need from the WS client
type WSClient interface {
	// No methods needed since we only use ws.Connect to create the client
}

// SolanaClient interface defines the methods for Solana blockchain operations
type SolanaClient interface {
	CreateTokenAccount(ctx context.Context, owner solana.PublicKey) (solana.PublicKey, error)
	TransferUSDC(ctx context.Context, from, to solana.PublicKey, amount *USDCAmount) error
	GetUSDCBalance(ctx context.Context, account solana.PublicKey) (*USDCAmount, error)
	EscrowUSDC(ctx context.Context, from solana.PublicKey, amount *USDCAmount) error
	ReleaseEscrow(ctx context.Context, to solana.PublicKey, amount *USDCAmount) error
}

// solanaClient implements the SolanaClient interface
type solanaClient struct {
	config SolanaConfig
	rpc    RPCClient
	ws     WSClient
}

// NewSolanaClient creates a new Solana client
func NewSolanaClient(config SolanaConfig) (SolanaClient, error) {
	if config.RPCEndpoint == "" {
		return nil, fmt.Errorf("RPC endpoint is required")
	}
	if config.WSEndpoint == "" {
		return nil, fmt.Errorf("WS endpoint is required")
	}
	if config.EscrowPrivateKey == nil {
		return nil, fmt.Errorf("escrow private key is required")
	}
	if config.EscrowTokenAccount.IsZero() {
		return nil, fmt.Errorf("escrow token account is required")
	}

	rpcClient := rpc.New(config.RPCEndpoint)
	wsClient, err := ws.Connect(context.Background(), config.WSEndpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Solana WS: %w", err)
	}

	return &solanaClient{
		config: config,
		rpc:    rpcClient,
		ws:     wsClient,
	}, nil
}

// CreateTokenAccount creates a new USDC token account for a user
func (c *solanaClient) CreateTokenAccount(ctx context.Context, owner solana.PublicKey) (solana.PublicKey, error) {
	if owner.IsZero() {
		return solana.PublicKey{}, fmt.Errorf("owner address is required")
	}

	// Create a new account for the token account
	account := solana.NewWallet()
	rent, err := c.rpc.GetMinimumBalanceForRentExemption(ctx, 165, rpc.CommitmentFinalized)
	if err != nil {
		return solana.PublicKey{}, fmt.Errorf("failed to get rent: %w", err)
	}

	// Convert rent to bytes
	rentBytes := make([]byte, 8)
	binary.LittleEndian.PutUint64(rentBytes, rent)

	// Create the token account instruction
	instruction := solana.NewInstruction(
		solana.TokenProgramID,
		solana.AccountMetaSlice{
			{PublicKey: account.PublicKey(), IsSigner: true, IsWritable: true},
			{PublicKey: owner, IsSigner: false, IsWritable: false},
			{PublicKey: solana.MustPublicKeyFromBase58(USDC_MINT), IsSigner: false, IsWritable: false},
		},
		append([]byte{9}, rentBytes...), // Initialize account instruction with rent
	)

	// Create and send transaction
	tx, err := solana.NewTransaction(
		[]solana.Instruction{instruction},
		solana.Hash{}, // Recent blockhash will be fetched
		solana.TransactionPayer(owner),
	)
	if err != nil {
		return solana.PublicKey{}, fmt.Errorf("failed to create transaction: %w", err)
	}

	// Sign and send transaction
	_, err = c.rpc.SendTransaction(ctx, tx)
	if err != nil {
		return solana.PublicKey{}, fmt.Errorf("failed to send transaction: %w", err)
	}

	return account.PublicKey(), nil
}

// TransferUSDC transfers USDC from one account to another
func (c *solanaClient) TransferUSDC(ctx context.Context, from, to solana.PublicKey, amount *USDCAmount) error {
	if from.IsZero() || to.IsZero() {
		return fmt.Errorf("from and to addresses are required")
	}
	if amount == nil || amount.Value == nil {
		return fmt.Errorf("amount is required")
	}
	if amount.Value.Sign() <= 0 {
		return fmt.Errorf("amount must be greater than 0")
	}

	// Convert amount to bytes (little-endian)
	amountBytes := make([]byte, 8)
	amount.Value.FillBytes(amountBytes)
	// Reverse bytes for little-endian
	for i, j := 0, len(amountBytes)-1; i < j; i, j = i+1, j-1 {
		amountBytes[i], amountBytes[j] = amountBytes[j], amountBytes[i]
	}

	// Create transfer instruction
	instruction := solana.NewInstruction(
		solana.TokenProgramID,
		solana.AccountMetaSlice{
			{PublicKey: from, IsSigner: true, IsWritable: true},
			{PublicKey: to, IsSigner: false, IsWritable: true},
			{PublicKey: solana.MustPublicKeyFromBase58(USDC_MINT), IsSigner: false, IsWritable: false},
		},
		append([]byte{3}, amountBytes...), // Transfer instruction
	)

	// Create and send transaction
	tx, err := solana.NewTransaction(
		[]solana.Instruction{instruction},
		solana.Hash{}, // Recent blockhash will be fetched
		solana.TransactionPayer(from),
	)
	if err != nil {
		return fmt.Errorf("failed to create transaction: %w", err)
	}

	// Sign and send transaction
	_, err = c.rpc.SendTransaction(ctx, tx)
	if err != nil {
		return fmt.Errorf("failed to send transaction: %w", err)
	}

	return nil
}

// GetUSDCBalance gets the USDC balance of a token account
func (c *solanaClient) GetUSDCBalance(ctx context.Context, account solana.PublicKey) (*USDCAmount, error) {
	if account.IsZero() {
		return nil, fmt.Errorf("account address is required")
	}

	// Get token account info
	info, err := c.rpc.GetAccountInfo(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("failed to get account info: %w", err)
	}

	// Parse balance from account data
	// Token account data layout: https://spl.solana.com/token#account
	if len(info.GetBinary()) < 64 {
		return nil, fmt.Errorf("invalid token account data length")
	}

	// Amount is stored as a 64-bit little-endian integer starting at offset 64
	amountBytes := info.GetBinary()[64:72]
	// Reverse bytes for big-endian
	for i, j := 0, len(amountBytes)-1; i < j; i, j = i+1, j-1 {
		amountBytes[i], amountBytes[j] = amountBytes[j], amountBytes[i]
	}

	value := new(big.Int)
	value.SetBytes(amountBytes)
	return &USDCAmount{Value: value}, nil
}

// EscrowUSDC escrows USDC from a user to the platform's escrow account
func (c *solanaClient) EscrowUSDC(ctx context.Context, from solana.PublicKey, amount *USDCAmount) error {
	if from.IsZero() {
		return fmt.Errorf("from address is required")
	}
	if amount == nil || amount.Value == nil {
		return fmt.Errorf("amount is required")
	}
	if amount.Value.Sign() <= 0 {
		return fmt.Errorf("amount must be greater than 0")
	}

	// Transfer USDC to escrow account
	return c.TransferUSDC(ctx, from, c.config.EscrowTokenAccount, amount)
}

// ReleaseEscrow releases USDC from the escrow account to a user
func (c *solanaClient) ReleaseEscrow(ctx context.Context, to solana.PublicKey, amount *USDCAmount) error {
	if to.IsZero() {
		return fmt.Errorf("to address is required")
	}
	if amount == nil || amount.Value == nil {
		return fmt.Errorf("amount is required")
	}
	if amount.Value.Sign() <= 0 {
		return fmt.Errorf("amount must be greater than 0")
	}

	// Transfer USDC from escrow account to user
	return c.TransferUSDC(ctx, c.config.EscrowTokenAccount, to, amount)
}

// ValidateWalletAddress validates a Solana wallet address
func ValidateWalletAddress(address string) error {
	if address == "" {
		return fmt.Errorf("address is required")
	}

	// Try to decode the address
	_, err := solana.PublicKeyFromBase58(address)
	if err != nil {
		return fmt.Errorf("invalid Solana address: %w", err)
	}

	return nil
}

// ValidateTokenAccount validates a USDC token account
func ValidateTokenAccount(ctx context.Context, account solana.PublicKey) error {
	if account.IsZero() {
		return fmt.Errorf("account address is required")
	}

	// TODO: Implement proper token account validation
	// This would involve checking:
	// 1. The account exists
	// 2. The account is owned by the Token Program
	// 3. The account is associated with the USDC mint
	// 4. The account has the correct number of decimals

	return nil
}
