package abb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"time"
)

// BlueskyDependencies holds dependencies for Bluesky activities (currently none needed for public read)
type BlueskyDependencies struct {
	PDS        string `json:"pds,omitempty"` // Added PDS field for the Personal Data Server URL
	Identifier string `json:"identifier,omitempty"`
	Password   string `json:"password,omitempty"`
}

// Type returns the platform type for BlueskyDependencies
func (deps BlueskyDependencies) Type() PlatformKind {
	return PlatformBluesky
}

// MarshalJSON implements json.Marshaler for BlueskyDependencies
func (deps BlueskyDependencies) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct{}{}) // Empty object for now
}

// UnmarshalJSON implements json.Unmarshaler for BlueskyDependencies
func (deps *BlueskyDependencies) UnmarshalJSON(data []byte) error {
	var aux struct{}
	if err := json.Unmarshal(data, &aux); err != nil {
		return fmt.Errorf("failed to unmarshal BlueskyDependencies: %w", err)
	}
	return nil
}

// BlueskyContent represents the extracted content from a Bluesky post view
// Based on app.bsky.feed.defs#postView and related schemas
type BlueskyContent struct {
	Uri         string                  `json:"uri"`             // AT URI of the post
	Cid         string                  `json:"cid"`             // CID of the post record
	Author      BlueskyProfileViewBasic `json:"author"`          // Basic profile view of the author
	Record      json.RawMessage         `json:"record"`          // The raw post record (app.bsky.feed.post)
	Embed       *BlueskyEmbedView       `json:"embed,omitempty"` // Embedded content (images, external links, etc.)
	ReplyCount  *int64                  `json:"replyCount,omitempty"`
	RepostCount *int64                  `json:"repostCount,omitempty"`
	LikeCount   *int64                  `json:"likeCount,omitempty"`
	IndexedAt   time.Time               `json:"indexedAt"`
	Labels      []string                `json:"labels,omitempty"` // Assuming labels are strings for simplicity
	// Extracted Text from Record for convenience
	Text string `json:"text,omitempty"`
}

// BlueskyProfileViewBasic represents a subset of app.bsky.actor.defs#profileViewBasic
type BlueskyProfileViewBasic struct {
	Did         string   `json:"did"`
	Handle      string   `json:"handle"`
	DisplayName *string  `json:"displayName,omitempty"`
	Avatar      *string  `json:"avatar,omitempty"`
	Labels      []string `json:"labels,omitempty"` // Assuming labels are strings
}

// BlueskyEmbedView represents possible embed types (simplified)
type BlueskyEmbedView struct {
	Type     string                    `json:"$type"` // e.g., "app.bsky.embed.images#view", "app.bsky.embed.external#view"
	Images   []BlueskyEmbedImageView   `json:"images,omitempty"`
	External *BlueskyEmbedExternalView `json:"external,omitempty"`
	Record   *BlueskyEmbedRecordView   `json:"record,omitempty"` // For quote posts/record embeds
	// Add other embed types as needed (e.g., RecordWithMedia)
}

// BlueskyEmbedImageView represents app.bsky.embed.images#viewImage
type BlueskyEmbedImageView struct {
	Thumb    string `json:"thumb"`    // URL of thumbnail
	Fullsize string `json:"fullsize"` // URL of full-size image
	Alt      string `json:"alt"`      // Alt text
}

// BlueskyEmbedExternalView represents app.bsky.embed.external#viewExternal
type BlueskyEmbedExternalView struct {
	Uri         string `json:"uri"`
	Thumb       string `json:"thumb,omitempty"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

// BlueskyEmbedRecordView represents app.bsky.embed.record#viewRecord (for quote posts)
type BlueskyEmbedRecordView struct {
	Uri    string                  `json:"uri"`
	Cid    string                  `json:"cid"`
	Author BlueskyProfileViewBasic `json:"author"`
	// Value json.RawMessage `json:"value"` // The actual record content - maybe omit for simplicity?
	Embeds *[]BlueskyEmbedView `json:"embeds,omitempty"` // Nested embeds within the quoted post
}

// BlueskyUserStats represents the stats for a given bluesky user
type BlueskyUserStats struct {
	Did            string        `json:"did"`
	Handle         string        `json:"handle"`
	DisplayName    string        `json:"displayName"`
	Description    string        `json:"description"`
	Avatar         string        `json:"avatar"`
	Banner         string        `json:"banner"`
	FollowsCount   int           `json:"followsCount"`
	FollowersCount int           `json:"followersCount"`
	PostsCount     int           `json:"postsCount"`
	IndexedAt      time.Time     `json:"indexedAt"`
	Labels         []interface{} `json:"labels"`
}

// BlueskyHandleResponse is used to parse the JSON response from com.atproto.identity.resolveHandle
type BlueskyHandleResponse struct {
	DID string `json:"did"`
}

// GetBlueSkyUserStats fetches user stats from the Bluesky API
func (a *Activities) GetBlueSkyUserStats(ctx context.Context, userHandle string) (*BlueskyUserStats, error) {
	// First, resolve the handle to a DID
	handleURL := fmt.Sprintf("https://public.api.bsky.app/xrpc/com.atproto.identity.resolveHandle?handle=%s", userHandle)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, handleURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create handle request: %w", err)
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make handle request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to resolve handle: %s", string(body))
	}

	var handleResp BlueskyHandleResponse
	if err := json.NewDecoder(resp.Body).Decode(&handleResp); err != nil {
		return nil, fmt.Errorf("failed to decode handle response: %w", err)
	}

	// Now get the profile
	profileURL := fmt.Sprintf("https://public.api.bsky.app/xrpc/app.bsky.actor.getProfile?actor=%s", handleResp.DID)
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, profileURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create profile request: %w", err)
	}

	resp, err = a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make profile request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to get profile: %s", string(body))
	}

	var userStats BlueskyUserStats
	if err := json.NewDecoder(resp.Body).Decode(&userStats); err != nil {
		return nil, fmt.Errorf("failed to decode profile response: %w", err)
	}

	return &userStats, nil
}

func (a *Activities) GetWalletAddressFromBlueskyProfile(ctx context.Context, userHandle string) (string, error) {
	userStats, err := a.GetBlueSkyUserStats(ctx, userHandle)
	if err != nil {
		return "", fmt.Errorf("failed to get user stats: %w", err)
	}

	if userStats.Description == "" {
		return "", fmt.Errorf("no description found on user profile")
	}

	// Use a regex to find a Solana wallet address
	re := regexp.MustCompile(`[1-9A-HJ-NP-Za-km-z]{32,44}`)
	walletAddress := re.FindString(userStats.Description)

	if walletAddress == "" {
		return "", ErrWalletNotFound
	}

	return walletAddress, nil
}
