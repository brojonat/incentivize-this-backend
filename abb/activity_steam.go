package abb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// SteamDependencies holds dependencies for Steam activities.
type SteamDependencies struct {
	APIKey string `json:"api_key"`
}

// Type returns the platform type for Steam.
func (deps SteamDependencies) Type() PlatformKind {
	return PlatformSteam
}

// MarshalJSON implements json.Marshaler for SteamDependencies.
func (deps SteamDependencies) MarshalJSON() ([]byte, error) {
	type Aux struct {
		APIKey string `json:"api_key"`
	}
	aux := Aux{
		APIKey: deps.APIKey,
	}
	return json.Marshal(aux)
}

// UnmarshalJSON implements json.Unmarshaler for SteamDependencies.
func (deps *SteamDependencies) UnmarshalJSON(data []byte) error {
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

// Dota2ChatContent represents the chat content of a Dota 2 match.
type Dota2ChatContent struct {
	MatchID    int64       `json:"match_id"`
	ChatEvents []ChatEvent `json:"chat"`
}

// ChatEvent represents a single chat event in a Dota 2 match.
type ChatEvent struct {
	Time int    `json:"time"`
	Type string `json:"type"`
	Key  string `json:"key"`
	Slot int    `json:"slot"`
}

// OpenDotaPlayerInfo represents the player information from OpenDota.
type OpenDotaPlayerInfo struct {
	Profile struct {
		AccountID      int    `json:"account_id"`
		Personaname    string `json:"personaname"`
		Name           string `json:"name"`
		Plus           bool   `json:"plus"`
		Cheese         int    `json:"cheese"`
		Steamid        string `json:"steamid"`
		Avatar         string `json:"avatar"`
		Avatarmedium   string `json:"avatarmedium"`
		Avatarfull     string `json:"avatarfull"`
		Profileurl     string `json:"profileurl"`
		LastLogin      string `json:"last_login"`
		Loccountrycode string `json:"loccountrycode"`
		IsContributor  bool   `json:"is_contributor"`
		IsSubscriber   bool   `json:"is_subscriber"`
	} `json:"profile"`
	RankTier    int `json:"rank_tier"`
	Leaderboard int `json:"leaderboard_rank"`
}

// fetchDota2Chat fetches a specific review from the Steam API.
func (a *Activities) fetchDota2Chat(ctx context.Context, deps SteamDependencies, matchID string) (*Dota2ChatContent, error) {
	// Construct the URL for the GetMatchDetails endpoint
	baseURL := fmt.Sprintf("https://api.opendota.com/api/matches/%s", matchID)
	url := baseURL
	if deps.APIKey != "" {
		url = fmt.Sprintf("%s?api_key=%s", baseURL, deps.APIKey)
	}

	// Make the HTTP request
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create opendota api request: %w", err)
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make opendota api request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("opendota api returned status %d", resp.StatusCode)
	}

	// Decode the JSON response
	var response Dota2ChatContent
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to decode opendota api response: %w", err)
	}

	// Filter out "chatwheel" events
	var filteredChat []ChatEvent
	for _, event := range response.ChatEvents {
		if event.Type != "chatwheel" {
			filteredChat = append(filteredChat, event)
		}
	}
	response.ChatEvents = filteredChat

	// Truncate chat log if it's too long
	if len(response.ChatEvents) > 100 { // Limit to the last 100 messages
		response.ChatEvents = response.ChatEvents[len(response.ChatEvents)-100:]
	}

	return &response, nil
}

// GetSteamPlayerInfo fetches player information from OpenDota.
func (a *Activities) GetSteamPlayerInfo(ctx context.Context, accountID int) (*OpenDotaPlayerInfo, error) {
	cfg, err := getConfiguration(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get configuration: %w", err)
	}
	deps := cfg.SteamDeps

	baseURL := fmt.Sprintf("https://api.opendota.com/api/players/%d", accountID)
	url := baseURL
	if deps.APIKey != "" {
		url = fmt.Sprintf("%s?api_key=%s", baseURL, deps.APIKey)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create opendota api request: %w", err)
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make opendota api request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("opendota api returned status %d", resp.StatusCode)
	}

	var playerInfo OpenDotaPlayerInfo
	if err := json.NewDecoder(resp.Body).Decode(&playerInfo); err != nil {
		return nil, fmt.Errorf("failed to decode opendota api response: %w", err)
	}

	return &playerInfo, nil
}
