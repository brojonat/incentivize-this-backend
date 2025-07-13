package http

import (
	"testing"
)

func TestDefaultPayoutCalculator(t *testing.T) {
	tests := []struct {
		name           string
		userSharePct   float64
		inputAmount    float64
		expectedAmount float64
	}{
		{
			name:           "50% split",
			userSharePct:   50.0,
			inputAmount:    100.0,
			expectedAmount: 50.0,
		},
		{
			name:           "30% user share",
			userSharePct:   30.0,
			inputAmount:    100.0,
			expectedAmount: 30.0,
		},
		{
			name:           "70% user share",
			userSharePct:   70.0,
			inputAmount:    100.0,
			expectedAmount: 70.0,
		},
		{
			name:           "100% user share (no fee)",
			userSharePct:   100.0,
			inputAmount:    100.0,
			expectedAmount: 100.0,
		},
		{
			name:           "zero input amount",
			userSharePct:   50.0,
			inputAmount:    0.0,
			expectedAmount: 0.0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Create calculator and test
			calc := DefaultPayoutCalculator(tc.userSharePct)
			result := calc(tc.inputAmount)

			if result != tc.expectedAmount {
				t.Errorf("Expected payout of %.2f but got %.2f", tc.expectedAmount, result)
			}
		})
	}
}

// TestPayoutCalculatorIntegration simulates how the payout calculator would be used in the handler
func TestPayoutCalculatorIntegration(t *testing.T) {
	// Test values
	originalBountyPerPost := 10.0
	originalTotalBounty := 100.0

	// Test with 50%
	calc := DefaultPayoutCalculator(50.0)
	userBountyPerPost := calc(originalBountyPerPost)
	userTotalBounty := calc(originalTotalBounty)

	if userBountyPerPost != 5.0 {
		t.Errorf("Expected bounty per post of 5.0 but got %.2f", userBountyPerPost)
	}
	if userTotalBounty != 50.0 {
		t.Errorf("Expected total bounty of 50.0 but got %.2f", userTotalBounty)
	}

	// Test with 100% share (no fee)
	noFeeCalc := DefaultPayoutCalculator(100.0)

	userBountyPerPost = noFeeCalc(originalBountyPerPost)
	userTotalBounty = noFeeCalc(originalTotalBounty)

	if userBountyPerPost != 10.0 {
		t.Errorf("Expected no-fee bounty per post of 10.0 but got %.2f", userBountyPerPost)
	}
	if userTotalBounty != 100.0 {
		t.Errorf("Expected no-fee total bounty of 100.0 but got %.2f", userTotalBounty)
	}
}
