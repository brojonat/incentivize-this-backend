package api

import "time"

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
	PlatformType      string    `json:"platform_type"`
	CreatedAt         time.Time `json:"created_at"`
}
