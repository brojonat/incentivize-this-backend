package solana

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

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
