package abb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"go.temporal.io/sdk/activity"
)

// HackerNewsDependencies holds dependencies for Hacker News activities (currently none needed)
type HackerNewsDependencies struct{}

// Type returns the platform type for Hacker News activities
func (deps HackerNewsDependencies) Type() PlatformKind {
	return PlatformHackerNews
}

// MarshalJSON implements json.Marshaler for Hacker News activities
func (deps HackerNewsDependencies) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct{}{}) // Empty object
}

// UnmarshalJSON implements json.Unmarshaler for Hacker News activities
func (deps *HackerNewsDependencies) UnmarshalJSON(data []byte) error {
	// No fields to unmarshal, just ensure it's a valid empty object if needed
	var aux struct{}
	if err := json.Unmarshal(data, &aux); err != nil {
		return fmt.Errorf("failed to unmarshal HackerNewsDependencies: %w", err)
	}
	return nil
}

// HackerNewsContent represents the extracted content from Hacker News
type HackerNewsContent struct {
	ID          int       `json:"id"`          // The item's unique id.
	Deleted     bool      `json:"deleted"`     // true if the item is deleted.
	Type        string    `json:"type"`        // "job", "story", "comment", "poll", or "pollopt".
	By          string    `json:"by"`          // The username of the item's author.
	Time        time.Time `json:"time"`        // Creation date of the item, as time.Time.
	Text        string    `json:"text"`        // The comment, story or poll text. HTML.
	Dead        bool      `json:"dead"`        // true if the item is dead.
	Parent      int       `json:"parent"`      // The comment's parent: either another comment or the story.
	Poll        int       `json:"poll"`        // The pollopt's associated poll.
	Kids        []int     `json:"kids"`        // The ids of the item's comments, in ranked display order.
	URL         string    `json:"url"`         // The URL of the story.
	Score       int       `json:"score"`       // The story's score, or the votes for a pollopt.
	Title       string    `json:"title"`       // The title of the story, poll or job.
	Parts       []int     `json:"parts"`       // A list of related pollopts, in display order.
	Descendants int       `json:"descendants"` // In the case of stories or polls, the total comment count.
}

// PullHackerNewsContent pulls content from Hacker News Firebase API
func (a *Activities) PullHackerNewsContent(ctx context.Context, contentID string, contentKind ContentKind) (*HackerNewsContent, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("Pulling Hacker News content", "content_id", contentID, "content_kind", contentKind)

	// Validate contentID (should be an integer for HN)
	_, err := strconv.Atoi(contentID) // Declare err here
	if err != nil {
		return nil, fmt.Errorf("invalid Hacker News content ID (must be integer): %s", contentID)
	}

	// Construct API URL
	apiURL := fmt.Sprintf("https://hacker-news.firebaseio.com/v0/item/%s.json", contentID)

	// Create request using shared httpClient
	var req *http.Request                                          // Declare req here
	req, err = http.NewRequestWithContext(ctx, "GET", apiURL, nil) // Use = for assignment
	if err != nil {
		return nil, fmt.Errorf("failed to create Hacker News API request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	// Make request
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make Hacker News API request: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read Hacker News API response body (status %d): %w", resp.StatusCode, err)
	}

	// Log raw response for debugging
	logger.Debug("Hacker News API Response", "status_code", resp.StatusCode, "response", string(body))

	// Check status code
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Hacker News API returned status %d: %s", resp.StatusCode, string(body))
	}

	// Check for null response (item doesn't exist)
	if string(body) == "null" {
		logger.Warn("Hacker News item not found or null response", "content_id", contentID)
		return nil, fmt.Errorf("Hacker News item %s not found", contentID)
	}

	// --- Unmarshal into intermediate struct to handle int time ---
	var rawItem struct {
		ID          int    `json:"id"`
		Deleted     bool   `json:"deleted"`
		Type        string `json:"type"`
		By          string `json:"by"`
		Time        int64  `json:"time"` // Expect int64 here
		Text        string `json:"text"`
		Dead        bool   `json:"dead"`
		Parent      int    `json:"parent"`
		Poll        int    `json:"poll"`
		Kids        []int  `json:"kids"`
		URL         string `json:"url"`
		Score       int    `json:"score"`
		Title       string `json:"title"`
		Parts       []int  `json:"parts"`
		Descendants int    `json:"descendants"`
	}
	if err := json.Unmarshal(body, &rawItem); err != nil { // CORRECTED: was if err =
		return nil, fmt.Errorf("failed to decode Hacker News intermediate response: %w (body: %s)", err, string(body))
	}
	// --- End Intermediate Unmarshal ---

	// --- Create final struct, converting time ---
	content := &HackerNewsContent{
		ID:          rawItem.ID,
		Deleted:     rawItem.Deleted,
		Type:        rawItem.Type,
		By:          rawItem.By,
		Time:        time.Unix(rawItem.Time, 0), // Convert int64 to time.Time
		Text:        rawItem.Text,
		Dead:        rawItem.Dead,
		Parent:      rawItem.Parent,
		Poll:        rawItem.Poll,
		Kids:        rawItem.Kids,
		URL:         rawItem.URL,
		Score:       rawItem.Score,
		Title:       rawItem.Title,
		Parts:       rawItem.Parts,
		Descendants: rawItem.Descendants,
	}
	// --- End Final Struct Creation ---

	// Basic validation based on expected type
	// Note: HN API uses 'story' for posts, 'comment' for comments.
	expectedType := ""
	switch contentKind {
	case ContentKindPost:
		expectedType = "story" // Also potentially "job" or "poll"
	case ContentKindComment:
		expectedType = "comment"
	}

	if expectedType != "" && content.Type != expectedType {
		// Allow flexibility for now (e.g., treating job/poll as post)
		logger.Warn("Hacker News item type mismatch", "content_id", contentID, "expected_kind", contentKind, "found_type", content.Type, "expected_api_type", expectedType)
		// Decide if this should be an error or just a warning
		// return nil, fmt.Errorf("Hacker News item %s type mismatch: expected kind %s (api type %s), got %s", contentID, contentKind, expectedType, content.Type)
	}

	// Check if item is deleted or dead
	if content.Deleted {
		logger.Warn("Hacker News item is marked as deleted", "content_id", contentID)
		// Maybe return an error or a specific status?
		// For now, return the content but log.
	}
	if content.Dead {
		logger.Warn("Hacker News item is marked as dead", "content_id", contentID)
	}

	logger.Info("Successfully pulled Hacker News content", "id", content.ID, "type", content.Type, "by", content.By)
	return content, nil
}
