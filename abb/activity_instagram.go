package abb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"

	"go.temporal.io/sdk/activity"
)

const (
	instagramAPIHost   = "instagram-scraper-stable-api.p.rapidapi.com"
	instagramAPIScheme = "https"
	instagramAPIPath   = "/get_reel_title.php"
)

// InstagramDependencies holds the dependencies for Instagram-related activities
type InstagramDependencies struct {
	RapidAPIKey string `json:"rapid_api_key"`
}

// Type returns the platform type for InstagramDependencies
func (deps InstagramDependencies) Type() PlatformKind {
	return PlatformInstagram
}

// MarshalJSON implements json.Marshaler for InstagramDependencies
func (deps InstagramDependencies) MarshalJSON() ([]byte, error) {
	type Aux struct {
		RapidAPIKey string `json:"rapid_api_key"`
	}
	aux := Aux{
		RapidAPIKey: deps.RapidAPIKey,
	}
	return json.Marshal(aux)
}

// UnmarshalJSON implements json.Unmarshaler for InstagramDependencies
func (deps *InstagramDependencies) UnmarshalJSON(data []byte) error {
	type Aux struct {
		RapidAPIKey string `json:"rapid_api_key"`
	}
	var aux Aux
	if err := json.Unmarshal(data, &aux); err != nil {
		return fmt.Errorf("failed to unmarshal InstagramDependencies: %w", err)
	}
	deps.RapidAPIKey = aux.RapidAPIKey
	return nil
}

// InstagramContent represents the extracted content from an Instagram reel/post
type InstagramContent struct {
	PostID                string `json:"post_id"`
	PostShortCode         string `json:"post_short_code"`
	Title                 string `json:"title"`
	Description           string `json:"description"`
	PostCaption           string `json:"post_caption"`
	PostLikes             int64  `json:"post_likes"`
	PostComments          int64  `json:"post_comments"`
	URL                   string `json:"url"`
	CreationDate          string `json:"creation_date"` // e.g., "August 3, 2024"
	CreationDateTimestamp int64  `json:"creation_date_timestamp"`
}

// PullInstagramContentActivity is a Temporal activity that pulls content from Instagram.
func (a *Activities) PullInstagramContentActivity(ctx context.Context, deps InstagramDependencies, contentID string) (*InstagramContent, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("PullInstagramContentActivity started", "contentID", contentID)

	if deps.RapidAPIKey == "" {
		return nil, fmt.Errorf("RapidAPI key for Instagram is not configured")
	}

	apiURL := url.URL{
		Scheme: instagramAPIScheme,
		Host:   instagramAPIHost,
		Path:   instagramAPIPath,
	}
	q := apiURL.Query()
	q.Set("reel_post_code_or_url", contentID)
	q.Set("type", "post") // Assuming 'post' type covers reels as per example
	apiURL.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Instagram API request: %w", err)
	}

	req.Header.Add("x-rapidapi-key", deps.RapidAPIKey)
	req.Header.Add("x-rapidapi-host", instagramAPIHost)
	req.Header.Set("Accept", "application/json")

	httpClient := a.httpClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute Instagram API request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read Instagram API response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Instagram API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	// The API sometimes returns an empty JSON array `[]` for errors or not found,
	// instead of a proper error JSON or non-200 status.
	if string(body) == "[]" {
		return nil, fmt.Errorf("Instagram content not found or API returned empty array for ID %s", contentID)
	}

	var content InstagramContent
	if err := json.Unmarshal(body, &content); err != nil {
		// Attempt to unmarshal into a potential error structure from RapidAPI
		var apiError struct {
			Message string `json:"message"`
		}
		if errParse := json.Unmarshal(body, &apiError); errParse == nil && apiError.Message != "" {
			logger.Error("Instagram API returned an error message", "error_message", apiError.Message, "contentID", contentID)
			return nil, fmt.Errorf("Instagram API error: %s", apiError.Message)
		}
		// If it's not the known error structure, return the original unmarshal error
		logger.Error("Failed to unmarshal Instagram API response", "error", err, "responseBody", string(body))
		return nil, fmt.Errorf("failed to unmarshal Instagram API response: %w. Body: %s", err, string(body))
	}
	return &content, nil
}

func (a *Activities) GetWalletAddressFromInstagramProfile(ctx context.Context, username string) (string, error) {
	cfg, err := getConfiguration(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get configuration: %w", err)
	}

	deps := cfg.InstagramDeps
	if deps.RapidAPIKey == "" {
		return "", fmt.Errorf("RapidAPI key for Instagram is not configured")
	}

	apiURL := url.URL{
		Scheme: instagramAPIScheme,
		Host:   instagramAPIHost,
		Path:   "/ig_get_fb_profile_v3.php",
	}
	q := apiURL.Query()
	q.Set("username", username)
	apiURL.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL.String(), nil)
	if err != nil {
		return "", fmt.Errorf("failed to create Instagram API request: %w", err)
	}

	req.Header.Add("x-rapidapi-key", deps.RapidAPIKey)
	req.Header.Add("x-rapidapi-host", instagramAPIHost)
	req.Header.Set("Accept", "application/json")

	httpClient := a.httpClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to execute Instagram API request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read Instagram API response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Instagram API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var userInfo struct {
		Bio string `json:"bio"`
	}
	if err := json.Unmarshal(body, &userInfo); err != nil {
		return "", fmt.Errorf("failed to unmarshal Instagram user info response: %w. Body: %s", err, string(body))
	}

	if userInfo.Bio == "" {
		return "", fmt.Errorf("no bio found on user profile")
	}

	re := regexp.MustCompile(`[1-9A-HJ-NP-Za-km-z]{32,44}`)
	walletAddress := re.FindString(userInfo.Bio)

	if walletAddress == "" {
		return "", ErrWalletNotFound
	}

	return walletAddress, nil
}
