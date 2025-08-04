package abb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
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

func (a *Activities) fetchTwitchClip(ctx context.Context, deps TwitchDependencies, client *http.Client, token, clipID string) (*TwitchClipContent, error) {
	// --- Logic from activity_twitch.go/fetchTwitchClip ---
	logger := activity.GetLogger(ctx)
	apiURL := fmt.Sprintf("https://api.twitch.tv/helix/clips?id=%s", clipID) // Use clip ID

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Twitch clip request: %w", err)
	}
	req.Header.Add("Client-ID", deps.ClientID)
	req.Header.Add("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Twitch clip request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read Twitch clip response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		logger.Error("Twitch clip request failed", "status", resp.StatusCode, "body", string(body))
		return nil, fmt.Errorf("Twitch clip request returned status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Data []TwitchClipContent `json:"data"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to decode Twitch clip response: %w (body: %s)", err, string(body))
	}

	if len(result.Data) == 0 {
		return nil, fmt.Errorf("no Twitch clip found for ID %s", clipID)
	}

	logger.Info("Successfully fetched Twitch clip", "id", clipID)
	clip := &result.Data[0]
	// Replace placeholders in ThumbnailURL
	clip.ThumbnailURL = strings.ReplaceAll(clip.ThumbnailURL, "%{width}", "320")
	clip.ThumbnailURL = strings.ReplaceAll(clip.ThumbnailURL, "%{height}", "180")
	return clip, nil
}

func (a *Activities) getTwitchAppAccessToken(ctx context.Context, deps TwitchDependencies, client *http.Client) (string, error) {
	// --- Logic from activity_twitch.go/getTwitchAppAccessToken ---
	logger := activity.GetLogger(ctx)
	logger.Info("Requesting new Twitch App Access Token via internal helper")
	tokenURL := "https://id.twitch.tv/oauth2/token"
	data := url.Values{}
	data.Set("client_id", deps.ClientID)
	data.Set("client_secret", deps.ClientSecret)
	data.Set("grant_type", "client_credentials")

	req, err := http.NewRequestWithContext(ctx, "POST", tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", fmt.Errorf("failed to create Twitch token request: %w", err)
	}
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("Twitch token request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read Twitch token response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		logger.Error("Twitch token request failed", "status", resp.StatusCode, "body", string(body))
		return "", fmt.Errorf("Twitch token request returned status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		TokenType   string `json:"token_type"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("failed to decode Twitch token response: %w", err)
	}

	if result.AccessToken == "" {
		return "", fmt.Errorf("Twitch token response did not contain an access token")
	}

	logger.Debug("Successfully obtained Twitch App Access Token via internal helper")
	return result.AccessToken, nil
}

func (a *Activities) fetchTwitchVideo(ctx context.Context, deps TwitchDependencies, client *http.Client, token, videoID string) (*TwitchVideoContent, error) {
	// --- Logic from activity_twitch.go/fetchTwitchVideo ---
	logger := activity.GetLogger(ctx)
	apiURL := fmt.Sprintf("https://api.twitch.tv/helix/videos?id=%s", videoID)

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Twitch video request: %w", err)
	}
	req.Header.Add("Client-ID", deps.ClientID)
	req.Header.Add("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Twitch video request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read Twitch video response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		logger.Error("Twitch video request failed", "status", resp.StatusCode, "body", string(body))
		return nil, fmt.Errorf("Twitch video request returned status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Data []TwitchVideoContent `json:"data"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to decode Twitch video response: %w (body: %s)", err, string(body))
	}

	if len(result.Data) == 0 {
		return nil, fmt.Errorf("no Twitch video found for ID %s", videoID)
	}

	logger.Info("Successfully fetched Twitch video", "id", videoID)
	video := &result.Data[0]
	// Replace placeholders in ThumbnailURL
	video.ThumbnailURL = strings.ReplaceAll(video.ThumbnailURL, "%{width}", "320")
	video.ThumbnailURL = strings.ReplaceAll(video.ThumbnailURL, "%{height}", "180")
	return video, nil
}

func (a *Activities) GetWalletAddressFromTwitchProfile(ctx context.Context, username string) (string, error) {
	cfg, err := getConfiguration(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get configuration: %w", err)
	}
	deps := cfg.TwitchDeps

	token, err := a.getTwitchAppAccessToken(ctx, deps, a.httpClient)
	if err != nil {
		return "", fmt.Errorf("failed to get twitch app access token: %w", err)
	}

	apiURL := fmt.Sprintf("https://api.twitch.tv/helix/users?login=%s", username)

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create Twitch user request: %w", err)
	}
	req.Header.Add("Client-ID", deps.ClientID)
	req.Header.Add("Authorization", "Bearer "+token)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("Twitch user request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read Twitch user response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Twitch user request returned status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Data []struct {
			Description string `json:"description"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("failed to decode Twitch user response: %w (body: %s)", err, string(body))
	}

	if len(result.Data) == 0 {
		return "", fmt.Errorf("no Twitch user found for username %s", username)
	}

	description := result.Data[0].Description
	if description == "" {
		return "", fmt.Errorf("no description found for user %s", username)
	}

	re := regexp.MustCompile(`[1-9A-HJ-NP-Za-km-z]{32,44}`)
	walletAddress := re.FindString(description)

	if walletAddress == "" {
		return "", fmt.Errorf("no wallet address found in profile description")
	}

	return walletAddress, nil
}
