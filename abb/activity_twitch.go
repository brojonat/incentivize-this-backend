package abb

import (
	"encoding/json"
	"time"
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

// --- PullTwitchContent function and its helpers (getTwitchAppAccessToken, fetchTwitchVideo, fetchTwitchClip) are now removed from this file. ---
// --- Their logic has been migrated to PullContentActivity and helper methods in activity.go ---
