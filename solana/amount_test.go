package solana

import (
	"math/big"
	"testing"
)

func TestNewUSDCAmount(t *testing.T) {
	tests := []struct {
		name    string
		amount  float64
		want    *big.Int
		wantErr bool
	}{
		{"zero", 0.0, big.NewInt(0), false},
		{"one_usdc", 1.0, big.NewInt(1_000_000), false},
		{"fractional_usdc", 0.5, big.NewInt(500_000), false},
		{"small_fractional_usdc", 0.000001, big.NewInt(1), false},
		{"large_amount", 123456.789012, big.NewInt(123456_789012), false},
		{"max_precision", 99.123456, big.NewInt(99_123456), false},
		// TODO: Add test case for amounts exceeding standard float64 precision if needed
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewUSDCAmount(tt.amount)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewUSDCAmount() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got.Value.Cmp(tt.want) != 0 {
				t.Errorf("NewUSDCAmount() got = %v, want %v", got.Value, tt.want)
			}
		})
	}
}

func TestUSDCAmount_ToUSDC(t *testing.T) {
	tests := []struct {
		name   string
		amount *USDCAmount
		want   float64
	}{
		{"zero", &USDCAmount{Value: big.NewInt(0)}, 0.0},
		{"one_usdc", &USDCAmount{Value: big.NewInt(1_000_000)}, 1.0},
		{"fractional_usdc", &USDCAmount{Value: big.NewInt(500_000)}, 0.5},
		{"small_fractional_usdc", &USDCAmount{Value: big.NewInt(1)}, 0.000001},
		{"large_amount", &USDCAmount{Value: big.NewInt(123456_789012)}, 123456.789012},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use a small tolerance for float comparison
			tolerance := 0.0000001
			got := tt.amount.ToUSDC()
			if diff := got - tt.want; diff > tolerance || diff < -tolerance {
				t.Errorf("ToUSDC() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestUSDCAmount_Add(t *testing.T) {
	a1, _ := NewUSDCAmount(1.23)
	a2, _ := NewUSDCAmount(4.56)
	want, _ := NewUSDCAmount(5.79)

	got := a1.Add(a2)
	if got.Cmp(want) != 0 {
		t.Errorf("Add() = %v, want %v", got.Value, want.Value)
	}
}

func TestUSDCAmount_Sub(t *testing.T) {
	a1, _ := NewUSDCAmount(5.79)
	a2, _ := NewUSDCAmount(1.23)
	want, _ := NewUSDCAmount(4.56)

	got := a1.Sub(a2)
	if got.Cmp(want) != 0 {
		t.Errorf("Sub() = %v, want %v", got.Value, want.Value)
	}
}

func TestUSDCAmount_Cmp(t *testing.T) {
	a1, _ := NewUSDCAmount(1.0)
	a2, _ := NewUSDCAmount(2.0)
	a3, _ := NewUSDCAmount(1.0)

	if a1.Cmp(a2) != -1 {
		t.Errorf("Cmp(a1, a2) want -1")
	}
	if a2.Cmp(a1) != 1 {
		t.Errorf("Cmp(a2, a1) want 1")
	}
	if a1.Cmp(a3) != 0 {
		t.Errorf("Cmp(a1, a3) want 0")
	}
}
