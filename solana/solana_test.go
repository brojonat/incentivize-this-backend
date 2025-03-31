package solana

import (
	"testing"
)

func TestNewUSDCAmount(t *testing.T) {
	tests := []struct {
		name    string
		amount  float64
		wantErr bool
	}{
		{
			name:    "valid amount",
			amount:  1.5,
			wantErr: false,
		},
		{
			name:    "zero amount",
			amount:  0,
			wantErr: false,
		},
		{
			name:    "negative amount",
			amount:  -1.5,
			wantErr: false,
		},
		{
			name:    "very small amount",
			amount:  0.000001,
			wantErr: false,
		},
		{
			name:    "very large amount",
			amount:  1000000.0,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewUSDCAmount(tt.amount)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewUSDCAmount() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got == nil {
				t.Error("NewUSDCAmount() returned nil")
				return
			}
		})
	}
}

func TestUSDCAmount_ToUSDC(t *testing.T) {
	tests := []struct {
		name     string
		amount   float64
		expected float64
	}{
		{
			name:     "whole number",
			amount:   1.0,
			expected: 1.0,
		},
		{
			name:     "decimal number",
			amount:   1.5,
			expected: 1.5,
		},
		{
			name:     "zero",
			amount:   0.0,
			expected: 0.0,
		},
		{
			name:     "small decimal",
			amount:   0.000001,
			expected: 0.000001,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usdc, err := NewUSDCAmount(tt.amount)
			if err != nil {
				t.Fatalf("NewUSDCAmount() error = %v", err)
			}
			got := usdc.ToUSDC()
			if got != tt.expected {
				t.Errorf("ToUSDC() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestUSDCAmount_Add(t *testing.T) {
	tests := []struct {
		name     string
		a        float64
		b        float64
		expected float64
	}{
		{
			name:     "add positive numbers",
			a:        1.5,
			b:        2.5,
			expected: 4.0,
		},
		{
			name:     "add with zero",
			a:        1.5,
			b:        0.0,
			expected: 1.5,
		},
		{
			name:     "add negative numbers",
			a:        -1.5,
			b:        -2.5,
			expected: -4.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, err := NewUSDCAmount(tt.a)
			if err != nil {
				t.Fatalf("NewUSDCAmount() error = %v", err)
			}
			b, err := NewUSDCAmount(tt.b)
			if err != nil {
				t.Fatalf("NewUSDCAmount() error = %v", err)
			}
			got := a.Add(b)
			if got == nil {
				t.Error("Add() returned nil")
				return
			}
			if got.ToUSDC() != tt.expected {
				t.Errorf("Add() = %v, want %v", got.ToUSDC(), tt.expected)
			}
		})
	}
}

func TestUSDCAmount_Sub(t *testing.T) {
	tests := []struct {
		name     string
		a        float64
		b        float64
		expected float64
	}{
		{
			name:     "subtract positive numbers",
			a:        2.5,
			b:        1.5,
			expected: 1.0,
		},
		{
			name:     "subtract with zero",
			a:        1.5,
			b:        0.0,
			expected: 1.5,
		},
		{
			name:     "subtract negative numbers",
			a:        -1.5,
			b:        -2.5,
			expected: 1.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, err := NewUSDCAmount(tt.a)
			if err != nil {
				t.Fatalf("NewUSDCAmount() error = %v", err)
			}
			b, err := NewUSDCAmount(tt.b)
			if err != nil {
				t.Fatalf("NewUSDCAmount() error = %v", err)
			}
			got := a.Sub(b)
			if got == nil {
				t.Error("Sub() returned nil")
				return
			}
			if got.ToUSDC() != tt.expected {
				t.Errorf("Sub() = %v, want %v", got.ToUSDC(), tt.expected)
			}
		})
	}
}

func TestUSDCAmount_Cmp(t *testing.T) {
	tests := []struct {
		name     string
		a        float64
		b        float64
		expected int
	}{
		{
			name:     "equal numbers",
			a:        1.5,
			b:        1.5,
			expected: 0,
		},
		{
			name:     "a greater than b",
			a:        2.5,
			b:        1.5,
			expected: 1,
		},
		{
			name:     "a less than b",
			a:        1.5,
			b:        2.5,
			expected: -1,
		},
		{
			name:     "compare with zero",
			a:        1.5,
			b:        0.0,
			expected: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, err := NewUSDCAmount(tt.a)
			if err != nil {
				t.Fatalf("NewUSDCAmount() error = %v", err)
			}
			b, err := NewUSDCAmount(tt.b)
			if err != nil {
				t.Fatalf("NewUSDCAmount() error = %v", err)
			}
			got := a.Cmp(b)
			if got != tt.expected {
				t.Errorf("Cmp() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestUSDCAmount_IsZero(t *testing.T) {
	tests := []struct {
		name     string
		amount   float64
		expected bool
	}{
		{
			name:     "zero amount",
			amount:   0.0,
			expected: true,
		},
		{
			name:     "non-zero amount",
			amount:   1.5,
			expected: false,
		},
		{
			name:     "small amount",
			amount:   0.000001,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usdc, err := NewUSDCAmount(tt.amount)
			if err != nil {
				t.Fatalf("NewUSDCAmount() error = %v", err)
			}
			got := usdc.IsZero()
			if got != tt.expected {
				t.Errorf("IsZero() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestUSDCAmount_IsNegative(t *testing.T) {
	tests := []struct {
		name     string
		amount   float64
		expected bool
	}{
		{
			name:     "negative amount",
			amount:   -1.5,
			expected: true,
		},
		{
			name:     "positive amount",
			amount:   1.5,
			expected: false,
		},
		{
			name:     "zero amount",
			amount:   0.0,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usdc, err := NewUSDCAmount(tt.amount)
			if err != nil {
				t.Fatalf("NewUSDCAmount() error = %v", err)
			}
			got := usdc.IsNegative()
			if got != tt.expected {
				t.Errorf("IsNegative() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestUSDCAmount_IsPositive(t *testing.T) {
	tests := []struct {
		name     string
		amount   float64
		expected bool
	}{
		{
			name:     "positive amount",
			amount:   1.5,
			expected: true,
		},
		{
			name:     "negative amount",
			amount:   -1.5,
			expected: false,
		},
		{
			name:     "zero amount",
			amount:   0.0,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usdc, err := NewUSDCAmount(tt.amount)
			if err != nil {
				t.Fatalf("NewUSDCAmount() error = %v", err)
			}
			got := usdc.IsPositive()
			if got != tt.expected {
				t.Errorf("IsPositive() = %v, want %v", got, tt.expected)
			}
		})
	}
}
