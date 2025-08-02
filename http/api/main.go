package api

import (
	"time"
)

type TokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Error       string `json:"error"`
}

type DefaultJSONResponse struct {
	Message string `json:"message"`
	Error   string `json:"error"`
}

type BountyListItem struct {
	BountyID                string     `json:"bounty_id"`
	Title                   string     `json:"title"`
	Status                  string     `json:"status"`
	Requirements            []string   `json:"requirements"`
	BountyPerPost           float64    `json:"bounty_per_post"`
	TotalBounty             float64    `json:"total_bounty"`
	TotalCharged            float64    `json:"total_charged"`
	RemainingBountyValue    float64    `json:"remaining_bounty_value"`
	BountyFunderWallet      string     `json:"bounty_funder_wallet"`
	PlatformKind            string     `json:"platform_kind"`
	ContentKind             string     `json:"content_kind"`
	Tier                    int        `json:"tier"`
	CreatedAt               time.Time  `json:"created_at"`
	EndAt                   time.Time  `json:"end_at"`
	PaymentTimeoutExpiresAt *time.Time `json:"payment_timeout_expires_at,omitempty"`
}

type CreateBountySuccessResponse struct {
	Message                 string    `json:"message"`
	BountyID                string    `json:"bounty_id"`
	TotalCharged            float64   `json:"total_charged"`
	PaymentTimeoutExpiresAt time.Time `json:"payment_timeout_expires_at"`
}

type ConfigResponse struct {
	USDCMintAddress string `json:"usdc_mint_address"`
	EscrowWallet    string `json:"escrow_wallet"`
}

type SolanaTransaction struct {
	Signature          string    `json:"signature"`
	Slot               int64     `json:"slot"`
	BlockTime          time.Time `json:"block_time"`
	BountyID           *string   `json:"bounty_id,omitempty"`
	FunderWallet       string    `json:"funder_wallet"`
	RecipientWallet    string    `json:"recipient_wallet"`
	AmountSmallestUnit int64     `json:"amount_smallest_unit"`
	Memo               *string   `json:"memo,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
}
