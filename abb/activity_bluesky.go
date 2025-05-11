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

// --- Bluesky Structures ---

// BlueskyDependencies holds dependencies for Bluesky activities (currently none needed for public read)
type BlueskyDependencies struct {
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

// ResolveBlueskyURLToATURI takes a full Bluesky post URL, resolves the handle to a DID,
// and constructs the full AT URI for the post.
func (a *Activities) ResolveBlueskyURLToATURI(ctx context.Context, postURL string) (string, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("Resolving Bluesky URL to AT URI", "url", postURL)

	// 1. Parse the URL to get handle and rkey
	parsedURL, err := url.Parse(postURL)
	if err != nil {
		logger.Error("Failed to parse Bluesky post URL", "url", postURL, "error", err)
		return "", fmt.Errorf("failed to parse post URL '%s': %w", postURL, err)
	}

	pathParts := strings.Split(strings.Trim(parsedURL.Path, "/"), "/")
	// Expected path: /profile/{handle}/post/{rkey}
	if len(pathParts) != 4 || pathParts[0] != "profile" || pathParts[2] != "post" {
		logger.Error("Invalid Bluesky post URL path format", "url", postURL, "path_parts", pathParts)
		return "", fmt.Errorf("invalid Bluesky URL path format: %s. Expected /profile/{handle}/post/{rkey}", parsedURL.Path)
	}
	handle := pathParts[1]
	rkey := pathParts[3]

	if handle == "" || rkey == "" {
		logger.Error("Could not extract handle or rkey from Bluesky URL", "url", postURL, "handle", handle, "rkey", rkey)
		return "", fmt.Errorf("could not extract handle or rkey from URL '%s'", postURL)
	}
	logger.Debug("Extracted from URL", "handle", handle, "rkey", rkey)

	// 2. Resolve handle to DID
	resolveHandleURL := fmt.Sprintf("https://public.api.bsky.app/xrpc/com.atproto.identity.resolveHandle?handle=%s", url.QueryEscape(handle))
	req, err := http.NewRequestWithContext(ctx, "GET", resolveHandleURL, nil)
	if err != nil {
		logger.Error("Failed to create request for DID resolution", "url", resolveHandleURL, "error", err)
		return "", fmt.Errorf("failed to create DID resolution request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	// Use the shared httpClient
	resp, err := a.httpClient.Do(req)
	if err != nil {
		logger.Error("Failed to resolve Bluesky handle to DID", "handle", handle, "error", err)
		return "", fmt.Errorf("failed to resolve handle '%s': %w", handle, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error("Failed to read DID resolution response body", "handle", handle, "error", err)
		return "", fmt.Errorf("failed to read DID resolution response body for handle '%s': %w", handle, err)
	}

	if resp.StatusCode != http.StatusOK {
		logger.Error("Bluesky handle resolution API returned non-OK status", "handle", handle, "status_code", resp.StatusCode, "response_body", string(body))
		return "", fmt.Errorf("failed to resolve handle '%s', status %d: %s", handle, resp.StatusCode, string(body))
	}

	var handleResponse BlueskyHandleResponse
	if err := json.Unmarshal(body, &handleResponse); err != nil {
		logger.Error("Failed to unmarshal DID resolution response", "handle", handle, "error", err, "response_body", string(body))
		return "", fmt.Errorf("failed to unmarshal DID for handle '%s': %w", handle, err)
	}

	if handleResponse.DID == "" {
		logger.Error("Resolved DID is empty", "handle", handle, "response_body", string(body))
		return "", fmt.Errorf("resolved DID for handle '%s' is empty", handle)
	}
	logger.Debug("Resolved handle to DID", "handle", handle, "did", handleResponse.DID)

	// 3. Construct AT URI
	// Collection for posts is app.bsky.feed.post
	atURI := fmt.Sprintf("at://%s/app.bsky.feed.post/%s", handleResponse.DID, rkey)
	logger.Info("Successfully resolved Bluesky URL to AT URI", "url", postURL, "at_uri", atURI)

	return atURI, nil
}

// PullBlueskyContent pulls content from Bluesky using the public AppView API
func (a *Activities) PullBlueskyContent(ctx context.Context, contentID string, contentKind ContentKind) (*BlueskyContent, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("Pulling Bluesky content", "at_uri", contentID, "content_kind", contentKind)

	// Validate contentID (should be an AT URI)
	if !strings.HasPrefix(contentID, "at://") {
		return nil, fmt.Errorf("invalid Bluesky content ID (must be an AT URI): %s", contentID)
	}

	// Construct API URL for app.bsky.feed.getPosts
	// Use the public API endpoint which doesn't require auth for public posts
	apiURL := "https://public.api.bsky.app/xrpc/app.bsky.feed.getPosts"

	// Set query parameters
	params := url.Values{}
	params.Add("uris", contentID) // The endpoint takes a list of URIs, but we only provide one
	fullURL := apiURL + "?" + params.Encode()

	// Create request using shared httpClient
	req, err := http.NewRequestWithContext(ctx, "GET", fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Bluesky API request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	// Make request
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make Bluesky API request: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read Bluesky API response body (status %d): %w", resp.StatusCode, err)
	}

	// Log raw response for debugging
	logger.Debug("Bluesky API Response", "status_code", resp.StatusCode, "response", string(body))

	// Check status code
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Bluesky API returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse the response - Structure is {"posts": [postView]}
	var responseData struct {
		Posts []json.RawMessage `json:"posts"`
	}

	if err := json.Unmarshal(body, &responseData); err != nil {
		return nil, fmt.Errorf("failed to decode Bluesky API response structure: %w (body: %s)", err, string(body))
	}

	if len(responseData.Posts) == 0 {
		return nil, fmt.Errorf("no post found for URI %s", contentID)
	}

	// Unmarshal the actual post view content
	postItemJSON := responseData.Posts[0]
	var content BlueskyContent
	if err := json.Unmarshal(postItemJSON, &content); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Bluesky post view content: %w (json: %s)", err, string(postItemJSON))
	}

	// --- Extract Text from Record ---
	// The actual post text is within the 'record' field, which follows the app.bsky.feed.post schema.
	var postRecord struct {
		Text      string    `json:"text"`
		CreatedAt time.Time `json:"createdAt"`
		// We only need 'text' for now, but other fields exist (e.g., embeds, facets, langs)
	}
	if err := json.Unmarshal(content.Record, &postRecord); err != nil {
		logger.Warn("Failed to unmarshal Bluesky post record to extract text", "uri", content.Uri, "error", err)
		// Continue without extracted text if unmarshal fails
	} else {
		content.Text = postRecord.Text
	}
	// --- End Text Extraction ---

	logger.Info("Successfully pulled Bluesky content", "uri", content.Uri, "by", content.Author.Handle)
	return &content, nil
}
