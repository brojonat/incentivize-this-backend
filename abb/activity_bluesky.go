package abb

import (
	"encoding/json"
	"fmt"
	"time"
)

// BlueskyDependencies holds dependencies for Bluesky activities (currently none needed for public read)
type BlueskyDependencies struct {
	PDS string `json:"pds,omitempty"` // Added PDS field for the Personal Data Server URL
	// Add fields like Handle, AppPassword if authentication is needed later
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

// BlueskyHandleResponse is used to parse the JSON response from com.atproto.identity.resolveHandle
type BlueskyHandleResponse struct {
	DID string `json:"did"`
}
