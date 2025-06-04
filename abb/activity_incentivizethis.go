package abb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"go.temporal.io/sdk/activity"
)

// IncentivizeThisDependencies holds the dependencies for IncentivizeThis-related activities.
type IncentivizeThisDependencies struct {
	APIEndpoint   string `json:"api_endpoint"`    // e.g., http://localhost:8080/api/v1
	AuthToken     string `json:"auth_token"`      // Optional, for authenticated internal endpoints
	PublicBaseURL string `json:"public_base_url"` // e.g., https://yourapp.com (may not be used directly by this activity anymore)
}

// Type returns the platform type for IncentivizeThisDependencies
func (deps IncentivizeThisDependencies) Type() PlatformKind {
	return PlatformIncentivizeThis
}

// MarshalJSON implements json.Marshaler for IncentivizeThisDependencies
func (deps IncentivizeThisDependencies) MarshalJSON() ([]byte, error) {
	type Aux struct {
		APIEndpoint   string `json:"api_endpoint"`
		AuthToken     string `json:"auth_token"`
		PublicBaseURL string `json:"public_base_url"`
	}
	aux := Aux{
		APIEndpoint:   deps.APIEndpoint,
		AuthToken:     deps.AuthToken,
		PublicBaseURL: deps.PublicBaseURL,
	}
	return json.Marshal(aux)
}

// UnmarshalJSON implements json.Unmarshaler for IncentivizeThisDependencies
func (deps *IncentivizeThisDependencies) UnmarshalJSON(data []byte) error {
	type Aux struct {
		APIEndpoint   string `json:"api_endpoint"`
		AuthToken     string `json:"auth_token"`
		PublicBaseURL string `json:"public_base_url"`
	}
	var aux Aux
	if err := json.Unmarshal(data, &aux); err != nil {
		return fmt.Errorf("failed to unmarshal IncentivizeThisDependencies: %w", err)
	}
	deps.APIEndpoint = aux.APIEndpoint
	deps.AuthToken = aux.AuthToken
	deps.PublicBaseURL = aux.PublicBaseURL
	return nil
}

// TargetBountyDetails represents the detailed structure of a bounty fetched for assessment
// by a meta-bounty on the IncentivizeThis platform.
// Its fields should mirror FetchedBountyData.
type TargetBountyDetails struct {
	BountyID             string       `json:"bounty_id"`
	Requirements         []string     `json:"requirements"`
	BountyPerPost        float64      `json:"bounty_per_post"`
	TotalBounty          float64      `json:"total_bounty"`
	PlatformType         PlatformKind `json:"platform_kind"`
	ContentKind          ContentKind  `json:"content_kind"`
	EndTime              time.Time    `json:"end_time,omitempty"`
	RemainingBountyValue float64      `json:"remaining_bounty_value"`
	BountyOwnerWallet    string       `json:"bounty_owner_wallet"` // Added from typical bounty details
	Status               string       `json:"status"`              // Added from typical bounty details
}

// FetchedBountyData is a simplified struct to unmarshal the response from GET /bounties/{id}
// It should match the relevant fields from http.BountyListItem for this activity's purpose.
type FetchedBountyData struct {
	BountyID             string       `json:"bounty_id"`
	Requirements         []string     `json:"requirements"`
	BountyPerPost        float64      `json:"bounty_per_post"`
	TotalBounty          float64      `json:"total_bounty"`
	PlatformType         PlatformKind `json:"platform_kind"`
	ContentKind          ContentKind  `json:"content_kind"`
	EndTime              time.Time    `json:"end_time,omitempty"`
	RemainingBountyValue float64      `json:"remaining_bounty_value"`
	BountyOwnerWallet    string       `json:"bounty_owner_wallet"`
	Status               string       `json:"status"`
}

// PullIncentivizeThisContentActivity fetches details of an existing bounty (the target bounty)
// from the GET /bounties/{id} endpoint. This data is then used by CheckContentRequirements
// to assess if the target bounty meets the criteria of the current (meta) bounty.
func (a *Activities) PullIncentivizeThisContentActivity(ctx context.Context, deps IncentivizeThisDependencies, bountyID string) (*TargetBountyDetails, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("PullIncentivizeThisContentActivity started", "targetBountyID", bountyID)

	if deps.APIEndpoint == "" {
		return nil, fmt.Errorf("APIEndpoint is not configured for IncentivizeThisDependencies")
	}
	// PublicBaseURL might not be strictly needed by this activity anymore if not constructing a display link here.
	if bountyID == "" {
		return nil, fmt.Errorf("bountyID for target bounty cannot be empty")
	}

	endpointURL := fmt.Sprintf("%s/bounties/%s", strings.TrimSuffix(deps.APIEndpoint, "/"), bountyID)

	req, err := http.NewRequestWithContext(ctx, "GET", endpointURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request to %s: %w", endpointURL, err)
	}

	req.Header.Set("Accept", "application/json")
	if deps.AuthToken != "" { // AuthToken is optional, GET /bounties/{id} might be public
		req.Header.Set("Authorization", "Bearer "+deps.AuthToken)
	}

	logger.Info("Fetching target bounty details for IncentivizeThis platform", "url", endpointURL)

	httpClient := a.httpClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request to %s: %w", endpointURL, err)
	}
	defer resp.Body.Close()

	bodyBytes, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		statusErrText := ""
		if resp.StatusCode != http.StatusOK {
			statusErrText = fmt.Sprintf(" (status %d)", resp.StatusCode)
		}
		return nil, fmt.Errorf("failed to read response body from %s%s: %w", endpointURL, statusErrText, readErr)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET /bounties request to %s failed with status %d: %s", endpointURL, resp.StatusCode, string(bodyBytes))
	}

	var fetchedBounty TargetBountyDetails
	if err := json.Unmarshal(bodyBytes, &fetchedBounty); err != nil {
		logger.Error("Failed to unmarshal target bounty data", "error", err, "responseBody", string(bodyBytes))
		return nil, fmt.Errorf("failed to unmarshal response from %s: %w. Body: %s", endpointURL, err, string(bodyBytes))
	}

	logger.Info("Successfully fetched target bounty details for IncentivizeThis platform", "targetBountyID", fetchedBounty.BountyID)
	return &fetchedBounty, nil
}
