package http

import (
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildSolanaPayURL(t *testing.T) {
	tests := []struct {
		name      string
		recipient string
		amount    float64
		usdcMint  string
		memo      string
		wantCheck func(t *testing.T, got string)
	}{
		{
			name:      "basic transfer with trailing zeros stripped",
			recipient: "ABC123def456",
			amount:    1.0,
			usdcMint:  "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v",
			memo:      "test-memo",
			wantCheck: func(t *testing.T, got string) {
				assert.True(t, strings.HasPrefix(got, "solana:ABC123def456?"))
				assert.Contains(t, got, "amount=1") // Not "1.000000"
				assert.Contains(t, got, "spl-token=EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v")
				assert.Contains(t, got, "memo=test-memo")
				assert.Contains(t, got, "label=IncentivizeThis")
				assert.NotContains(t, got, "reference=") // reference removed (requires valid pubkey)
			},
		},
		{
			name:      "decimal amount preserves precision",
			recipient: "XYZ789",
			amount:    1.5,
			usdcMint:  "4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU",
			memo:      "bounty-123",
			wantCheck: func(t *testing.T, got string) {
				assert.Contains(t, got, "amount=1.5")
				assert.NotContains(t, got, "1.500000")
			},
		},
		{
			name:      "small amount with precision",
			recipient: "TEST",
			amount:    0.000001,
			usdcMint:  "MINT",
			memo:      "small",
			wantCheck: func(t *testing.T, got string) {
				assert.Contains(t, got, "amount=0.000001")
			},
		},
		{
			name:      "empty memo omits memo parameter",
			recipient: "ADDR",
			amount:    10.0,
			usdcMint:  "MINT",
			memo:      "",
			wantCheck: func(t *testing.T, got string) {
				assert.Contains(t, got, "amount=10")
				assert.NotContains(t, got, "memo=")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildSolanaPayURL(tt.recipient, tt.amount, tt.usdcMint, tt.memo)

			// Verify URL structure
			assert.True(t, strings.HasPrefix(got, "solana:"))

			// Parse and verify it's a valid URL structure
			parts := strings.SplitN(got, "?", 2)
			require.Len(t, parts, 2, "URL should have query parameters")

			_, err := url.ParseQuery(parts[1])
			require.NoError(t, err, "Query parameters should be valid")

			// Run custom checks
			tt.wantCheck(t, got)
		})
	}
}

func TestFormatAmount(t *testing.T) {
	tests := []struct {
		name   string
		amount float64
		want   string
	}{
		{"whole number", 1.0, "1"},
		{"one decimal", 1.5, "1.5"},
		{"two decimals", 1.25, "1.25"},
		{"six decimals", 0.000001, "0.000001"},
		{"trailing zeros stripped", 1.500000, "1.5"},
		{"large amount", 1000000.0, "1000000"},
		{"small fraction", 0.1, "0.1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatAmount(tt.amount)
			assert.Equal(t, tt.want, got)
		})
	}
}
