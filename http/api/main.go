package api

import "time"

type TokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
}

type DefaultJSONResponse struct {
	Message string `json:"message"`
	Error   string `json:"error"`
}

type BountyListItem struct {
	WorkflowID        string    `json:"workflow_id"`
	Status            string    `json:"status"`
	Requirements      []string  `json:"requirements"`
	BountyPerPost     float64   `json:"bounty_per_post"`
	TotalBounty       float64   `json:"total_bounty"`
	BountyOwnerWallet string    `json:"bounty_owner_wallet"`
	PlatformKind      string    `json:"platform_kind"`
	ContentKind       string    `json:"content_kind"`
	CreatedAt         time.Time `json:"created_at"`
	EndAt             time.Time `json:"end_at"`
}

type CreateBountySuccessResponse struct {
	Message  string `json:"message"`
	BountyID string `json:"bounty_id"`
}
