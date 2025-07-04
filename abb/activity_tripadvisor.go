package abb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// TripadvisorDependencies holds the dependencies for Tripadvisor-related activities
type TripadvisorDependencies struct {
	APIKey string `json:"api_key"`
}

// Type returns the platform type
func (deps TripadvisorDependencies) Type() PlatformKind {
	return PlatformTripadvisor
}

// MarshalJSON implements json.Marshaler for TripadvisorDependencies
func (deps TripadvisorDependencies) MarshalJSON() ([]byte, error) {
	type Aux struct {
		APIKey string `json:"api_key"`
	}

	aux := Aux{
		APIKey: deps.APIKey,
	}

	return json.Marshal(aux)
}

// UnmarshalJSON implements json.Unmarshaler for TripadvisorDependencies
func (deps *TripadvisorDependencies) UnmarshalJSON(data []byte) error {
	type Aux struct {
		APIKey string `json:"api_key"`
	}

	var aux Aux
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	deps.APIKey = aux.APIKey
	return nil
}

// TripadvisorContent represents the extracted content from Tripadvisor
type TripadvisorContent struct {
	ID            json.Number `json:"id"`
	Text          string      `json:"text"`
	Rating        int         `json:"rating"`
	HelpfulVotes  int         `json:"helpful_votes"`
	URL           string      `json:"url"`
	PublishedDate string      `json:"published_date"` // e.g., "2025-07-02T23:14:23Z"
	Language      string      `json:"lang"`
	IsEdited      bool        `json:"is_edited"` // Note: Not present in Tripadvisor API, will default to false.
	Title         string      `json:"title"`
	TripType      string      `json:"trip_type"`
	User          struct {
		Username string `json:"username"`
		Avatar   struct {
			Thumbnail string `json:"thumbnail"`
		} `json:"avatar"`
	} `json:"user"`
}

// TripadvisorAPIResponse is the expected structure from the Tripadvisor API.
type TripadvisorAPIResponse struct {
	Data []TripadvisorContent `json:"data"`
}

// PullTripadvisorContentActivity pulls content from Tripadvisor.
// The contentID is expected to be a composite key in the format "locationId:reviewId".
func (a *Activities) PullTripadvisorContentActivity(ctx context.Context, deps TripadvisorDependencies, contentID string) (*TripadvisorContent, error) {
	parts := strings.Split(contentID, ":")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid contentID format for Tripadvisor, expected 'locationId:reviewId', got '%s'", contentID)
	}
	locationID := parts[0]
	reviewID := parts[1]

	apiURL := fmt.Sprintf("https://api.content.tripadvisor.com/api/v1/location/%s/reviews", locationID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request to tripadvisor: %w", err)
	}

	req.Header.Add("X-Tripadvisor-API-Key", deps.APIKey)
	req.Header.Add("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request to tripadvisor: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body from tripadvisor: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tripadvisor api returned non-200 status: %d, body: %s", resp.StatusCode, string(body))
	}

	var apiResponse TripadvisorAPIResponse
	if err := json.Unmarshal(body, &apiResponse); err != nil {
		return nil, fmt.Errorf("failed to unmarshal tripadvisor response: %w. body: %s", err, string(body))
	}

	for _, review := range apiResponse.Data {
		if review.ID.String() == reviewID {
			return &review, nil
		}
	}

	return nil, fmt.Errorf("review with id '%s' not found for location '%s' in tripadvisor api response", reviewID, locationID)
}
