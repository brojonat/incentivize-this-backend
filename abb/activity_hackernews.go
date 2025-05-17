package abb

import (
	"encoding/json"
	"fmt"
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
	ID          int    `json:"id"`          // The item's unique id.
	Deleted     bool   `json:"deleted"`     // true if the item is deleted.
	Type        string `json:"type"`        // "job", "story", "comment", "poll", or "pollopt".
	By          string `json:"by"`          // The username of the item's author.
	Time        int64  `json:"time"`        // Creation date of the item, as Unix timestamp.
	Text        string `json:"text"`        // The comment, story or poll text. HTML.
	Dead        bool   `json:"dead"`        // true if the item is dead.
	Parent      int    `json:"parent"`      // The comment's parent: either another comment or the story.
	Poll        int    `json:"poll"`        // The pollopt's associated poll.
	Kids        []int  `json:"kids"`        // The ids of the item's comments, in ranked display order.
	URL         string `json:"url"`         // The URL of the story.
	Score       int    `json:"score"`       // The story's score, or the votes for a pollopt.
	Title       string `json:"title"`       // The title of the story, poll or job.
	Parts       []int  `json:"parts"`       // A list of related pollopts, in display order.
	Descendants int    `json:"descendants"` // In the case of stories or polls, the total comment count.
}
