package http

import (
	"os"
	"testing"
)

func TestDefaultPayoutCalculator(t *testing.T) {
	tests := []struct {
		name            string
		envValue        string
		inputAmount     float64
		expectedAmount  float64
		shouldSetEnvVar bool
	}{
		{
			name:           "default 50% split",
			inputAmount:    100.0,
			expectedAmount: 50.0,
		},
		{
			name:            "30% user share",
			envValue:        "30",
			inputAmount:     100.0,
			expectedAmount:  30.0,
			shouldSetEnvVar: true,
		},
		{
			name:            "70% user share",
			envValue:        "70",
			inputAmount:     100.0,
			expectedAmount:  70.0,
			shouldSetEnvVar: true,
		},
		{
			name:            "100% user share (no fee)",
			envValue:        "100",
			inputAmount:     100.0,
			expectedAmount:  100.0,
			shouldSetEnvVar: true,
		},
		{
			name:           "zero input amount",
			inputAmount:    0.0,
			expectedAmount: 0.0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Setup environment if needed
			if tc.shouldSetEnvVar {
				oldVal := os.Getenv("USER_REVENUE_SHARE_PCT")
				os.Setenv("USER_REVENUE_SHARE_PCT", tc.envValue)
				defer os.Setenv("USER_REVENUE_SHARE_PCT", oldVal)
			}

			// Create calculator and test
			calc := DefaultPayoutCalculator()
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

	// Test with default calculator (50%)
	calc := DefaultPayoutCalculator()
	userBountyPerPost := calc(originalBountyPerPost)
	userTotalBounty := calc(originalTotalBounty)

	if userBountyPerPost != 5.0 {
		t.Errorf("Expected bounty per post of 5.0 but got %.2f", userBountyPerPost)
	}
	if userTotalBounty != 50.0 {
		t.Errorf("Expected total bounty of 50.0 but got %.2f", userTotalBounty)
	}

	// Test with 100% share (no fee)
	os.Setenv("USER_REVENUE_SHARE_PCT", "100")
	defer os.Setenv("USER_REVENUE_SHARE_PCT", "")
	noFeeCalc := DefaultPayoutCalculator()

	userBountyPerPost = noFeeCalc(originalBountyPerPost)
	userTotalBounty = noFeeCalc(originalTotalBounty)

	if userBountyPerPost != 10.0 {
		t.Errorf("Expected no-fee bounty per post of 10.0 but got %.2f", userBountyPerPost)
	}
	if userTotalBounty != 100.0 {
		t.Errorf("Expected no-fee total bounty of 100.0 but got %.2f", userTotalBounty)
	}
}
