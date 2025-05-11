package abb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.temporal.io/sdk/activity"
)

// TwitchDependencies holds the dependencies for Twitch-related activities
type TwitchDependencies struct {
	ClientID     string
	ClientSecret string
}

// Type returns the platform type for TwitchDependencies
func (deps TwitchDependencies) Type() PlatformKind {
	return PlatformTwitch
}

// MarshalJSON implements json.Marshaler for TwitchDependencies
func (deps TwitchDependencies) MarshalJSON() ([]byte, error) {
	type Aux struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	aux := Aux{
		ClientID:     deps.ClientID,
		ClientSecret: deps.ClientSecret,
	}
	return json.Marshal(aux)
}

// UnmarshalJSON implements json.Unmarshaler for TwitchDependencies
func (deps *TwitchDependencies) UnmarshalJSON(data []byte) error {
	type Aux struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	var aux Aux
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	deps.ClientID = aux.ClientID
	deps.ClientSecret = aux.ClientSecret
	return nil
}

// TwitchVideoContent represents the extracted content for a Twitch Video (VOD/Archive)
type TwitchVideoContent struct {
	ID            string    `json:"id"`
	StreamID      string    `json:"stream_id,omitempty"`
	UserID        string    `json:"user_id"`
	UserLogin     string    `json:"user_login"`
	UserName      string    `json:"user_name"`
	Title         string    `json:"title"`
	Description   string    `json:"description"`
	CreatedAt     time.Time `json:"created_at"`
	PublishedAt   time.Time `json:"published_at"`
	URL           string    `json:"url"`
	ThumbnailURL  string    `json:"thumbnail_url"`
	Viewable      string    `json:"viewable"`
	ViewCount     int64     `json:"view_count"`
	Language      string    `json:"language"`
	Type          string    `json:"type"`
	Duration      string    `json:"duration"`
	MutedSegments *[]struct {
		Duration int `json:"duration"`
		Offset   int `json:"offset"`
	} `json:"muted_segments"`
}

// TwitchClipContent represents the extracted content for a Twitch Clip
type TwitchClipContent struct {
	ID              string    `json:"id"`
	URL             string    `json:"url"`
	EmbedURL        string    `json:"embed_url"`
	BroadcasterID   string    `json:"broadcaster_id"`
	BroadcasterName string    `json:"broadcaster_name"`
	CreatorID       string    `json:"creator_id"`
	CreatorName     string    `json:"creator_name"`
	VideoID         string    `json:"video_id,omitempty"`
	GameID          string    `json:"game_id"`
	Language        string    `json:"language"`
	Title           string    `json:"title"`
	ViewCount       int64     `json:"view_count"`
	CreatedAt       time.Time `json:"created_at"`
	ThumbnailURL    string    `json:"thumbnail_url"`
	Duration        float64   `json:"duration"`
	VODOffset       *int      `json:"vod_offset"`
}

// PullTwitchContent pulls content from Twitch (Video or Clip)
func (a *Activities) PullTwitchContent(ctx context.Context, contentID string, contentKind ContentKind) (interface{}, error) {
	logger := activity.GetLogger(ctx)

	cfg, err := getConfiguration(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get configuration in PullTwitchContent: %w", err)
	}
	twitchDeps := cfg.TwitchDeps
	if twitchDeps.ClientID == "" || twitchDeps.ClientSecret == "" {
		return nil, fmt.Errorf("twitch ClientID or ClientSecret not configured")
	}

	logger.Info("Pulling Twitch content", "content_id", contentID, "content_kind", contentKind)

	accessToken, err := getTwitchAppAccessToken(a.httpClient, twitchDeps)
	if err != nil {
		return nil, fmt.Errorf("failed to get Twitch App Access Token: %w", err)
	}

	var apiURL string
	params := url.Values{}
	params.Add("id", contentID)

	switch contentKind {
	case ContentKindVideo:
		apiURL = "https://api.twitch.tv/helix/videos"
	case ContentKindClip:
		apiURL = "https://api.twitch.tv/helix/clips"
	default:
		return nil, fmt.Errorf("unsupported Twitch content kind: %s", contentKind)
	}

	fullURL := apiURL + "?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, "GET", fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Twitch API request: %w", err)
	}

	req.Header.Set("Client-Id", twitchDeps.ClientID)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make Twitch API request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read Twitch API response body: %w", err)
	}

	logger.Debug("Twitch API Response", "status_code", resp.StatusCode, "response", string(body))

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Twitch API returned status %d: %s", resp.StatusCode, string(body))
	}

	var responseData struct {
		Data       []json.RawMessage `json:"data"`
		Pagination struct{}          `json:"pagination"`
	}

	if err := json.Unmarshal(body, &responseData); err != nil {
		return nil, fmt.Errorf("failed to decode Twitch API response structure: %w (body: %s)", err, string(body))
	}

	if len(responseData.Data) == 0 {
		return nil, fmt.Errorf("no content found for ID %s (Kind: %s)", contentID, contentKind)
	}

	contentItemJSON := responseData.Data[0]
	switch contentKind {
	case ContentKindVideo:
		var videoContent TwitchVideoContent
		if err := json.Unmarshal(contentItemJSON, &videoContent); err != nil {
			return nil, fmt.Errorf("failed to unmarshal Twitch video content: %w (json: %s)", err, string(contentItemJSON))
		}
		return &videoContent, nil
	case ContentKindClip:
		var clipContent TwitchClipContent
		if err := json.Unmarshal(contentItemJSON, &clipContent); err != nil {
			return nil, fmt.Errorf("failed to unmarshal Twitch clip content: %w (json: %s)", err, string(contentItemJSON))
		}
		return &clipContent, nil
	default:
		return nil, fmt.Errorf("internal error: unsupported Twitch content kind reached parsing: %s", contentKind)
	}
}

// getTwitchAppAccessToken obtains an App Access Token from Twitch using Client Credentials Flow
func getTwitchAppAccessToken(client *http.Client, deps TwitchDependencies) (string, error) {
	formData := url.Values{
		"client_id":     {deps.ClientID},
		"client_secret": {deps.ClientSecret},
		"grant_type":    {"client_credentials"},
	}

	tokenURL := "https://id.twitch.tv/oauth2/token"
	req, err := http.NewRequest("POST", tokenURL, strings.NewReader(formData.Encode()))
	if err != nil {
		return "", fmt.Errorf("failed to create Twitch token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("Twitch token request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read Twitch token response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Twitch token request returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		TokenType   string `json:"token_type"`
	}

	if err := json.Unmarshal(bodyBytes, &tokenResp); err != nil {
		return "", fmt.Errorf("failed to decode Twitch token response: %w (body: %s)", err, string(bodyBytes))
	}

	if tokenResp.AccessToken == "" {
		return "", fmt.Errorf("Twitch token response did not contain an access token")
	}

	return tokenResp.AccessToken, nil
}
