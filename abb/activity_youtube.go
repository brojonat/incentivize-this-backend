package abb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"go.temporal.io/sdk/activity"
)

// YouTubeDependencies holds the dependencies for YouTube-related activities
type YouTubeDependencies struct {
	Client          *http.Client
	APIKey          string
	ApplicationName string
	MaxResults      int64
}

// Type returns the platform type for YouTubeDependencies
func (deps YouTubeDependencies) Type() PlatformKind {
	return PlatformYouTube
}

// MarshalJSON implements json.Marshaler for YouTubeDependencies
func (deps YouTubeDependencies) MarshalJSON() ([]byte, error) {
	type Aux struct {
		APIKey          string `json:"api_key"`
		ApplicationName string `json:"application_name"`
		MaxResults      int64  `json:"max_results"`
	}

	aux := Aux{
		APIKey:          deps.APIKey,
		ApplicationName: deps.ApplicationName,
		MaxResults:      deps.MaxResults,
	}

	return json.Marshal(aux)
}

// UnmarshalJSON implements json.Unmarshaler for YouTubeDependencies
func (deps *YouTubeDependencies) UnmarshalJSON(data []byte) error {
	type Aux struct {
		APIKey          string `json:"api_key"`
		ApplicationName string `json:"application_name"`
		MaxResults      int64  `json:"max_results"`
	}

	var aux Aux
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	deps.APIKey = aux.APIKey
	deps.ApplicationName = aux.ApplicationName
	deps.MaxResults = aux.MaxResults

	return nil
}

// YouTubeContent represents the extracted content from YouTube
type YouTubeContent struct {
	ID                   string    `json:"id"`
	Title                string    `json:"title"`
	Description          string    `json:"description"`
	ChannelID            string    `json:"channel_id"`
	ChannelTitle         string    `json:"channel_title"`
	PublishedAt          time.Time `json:"published_at"`
	ViewCount            string    `json:"view_count"`
	LikeCount            string    `json:"like_count"`
	CommentCount         string    `json:"comment_count"`
	Duration             string    `json:"duration"`
	ThumbnailURL         string    `json:"thumbnail_url"`
	Tags                 []string  `json:"tags"`
	CategoryID           string    `json:"category_id"`
	LiveBroadcastContent string    `json:"live_broadcast_content"`
	Transcript           string    `json:"transcript,omitempty"`
}

// YouTubeChannelStats represents the stats for a given YouTube channel
type YouTubeChannelStats struct {
	Kind  string `json:"kind"`
	Items []struct {
		Kind       string `json:"kind"`
		ID         string `json:"id"`
		Statistics struct {
			ViewCount             string `json:"viewCount"`
			SubscriberCount       string `json:"subscriberCount"`
			HiddenSubscriberCount bool   `json:"hiddenSubscriberCount"`
			VideoCount            string `json:"videoCount"`
		} `json:"statistics"`
	} `json:"items"`
}

// YouTubeVideoData represents the response from the YouTube Data API
type YouTubeVideoData struct {
	ID      string `json:"id"`
	Snippet struct {
		PublishedAt time.Time `json:"publishedAt"`
		ChannelID   string    `json:"channelId"`
		Title       string    `json:"title"`
		Description string    `json:"description"`
		Thumbnails  struct {
			Default struct {
				URL string `json:"url"`
			} `json:"default"`
		} `json:"thumbnails"`
		ChannelTitle         string   `json:"channelTitle"`
		Tags                 []string `json:"tags"`
		CategoryID           string   `json:"categoryId"`
		LiveBroadcastContent string   `json:"liveBroadcastContent"`
	} `json:"snippet"`
	Statistics struct {
		ViewCount    string `json:"viewCount"`
		LikeCount    string `json:"likeCount"`
		CommentCount string `json:"commentCount"`
	} `json:"statistics"`
	ContentDetails struct {
		Duration string `json:"duration"`
	} `json:"contentDetails"`
}

// UnmarshalJSON implements custom unmarshaling for YouTubeVideoData
func (v *YouTubeVideoData) UnmarshalJSON(data []byte) error {
	var rawData map[string]interface{}
	if err := json.Unmarshal(data, &rawData); err != nil {
		return err
	}

	type Aux struct {
		ID      string `json:"id"`
		Snippet struct {
			PublishedAt time.Time `json:"publishedAt"`
			ChannelID   string    `json:"channelId"`
			Title       string    `json:"title"`
			Description string    `json:"description"`
			Thumbnails  struct {
				Default struct {
					URL string `json:"url"`
				} `json:"default"`
			} `json:"thumbnails"`
			ChannelTitle         string   `json:"channelTitle"`
			Tags                 []string `json:"tags"`
			CategoryID           string   `json:"categoryId"`
			LiveBroadcastContent string   `json:"liveBroadcastContent"`
		} `json:"snippet"`
		Statistics struct {
			ViewCount    string `json:"viewCount"`
			LikeCount    string `json:"likeCount"`
			CommentCount string `json:"commentCount"`
		} `json:"statistics"`
		ContentDetails struct {
			Duration string `json:"duration"`
		} `json:"contentDetails"`
	}

	var aux Aux
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	v.ID = aux.ID
	v.Snippet = aux.Snippet
	v.Statistics = aux.Statistics
	v.ContentDetails = aux.ContentDetails

	return nil
}

// fetchYouTubeVideoMetadata fetches video metadata from the YouTube Data API
func (a *Activities) fetchYouTubeVideoMetadata(ctx context.Context, ytDeps YouTubeDependencies, httpClient *http.Client, videoID string) (*YouTubeVideoData, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("Fetching YouTube video metadata", "video_id", videoID)

	url := fmt.Sprintf("https://www.googleapis.com/youtube/v3/videos?part=snippet,statistics,contentDetails&id=%s&key=%s",
		videoID, ytDeps.APIKey)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if ytDeps.ApplicationName != "" {
		req.Header.Set("X-Goog-Api-Key", ytDeps.APIKey)
		req.Header.Set("X-Goog-Api-Client", ytDeps.ApplicationName)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("YouTube API returned status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Items []YouTubeVideoData `json:"items"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w (body: %s)", err, string(body))
	}

	if len(result.Items) == 0 {
		return nil, fmt.Errorf("no video found for ID %s", videoID)
	}

	return &result.Items[0], nil
}

// GetYoutubeChannelStats fetches channel stats from the YouTube Data API
func (a *Activities) GetYoutubeChannelStats(ctx context.Context, channelID string) (*YouTubeChannelStats, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("Fetching YouTube channel stats", "channel_id", channelID)
	cfg, err := getConfiguration(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get configuration: %w", err)
	}
	ytDeps := cfg.YouTubeDeps

	url := fmt.Sprintf("https://www.googleapis.com/youtube/v3/channels?part=statistics&id=%s&key=%s",
		channelID, ytDeps.APIKey)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if ytDeps.ApplicationName != "" {
		req.Header.Set("X-Goog-Api-Key", ytDeps.APIKey)
		req.Header.Set("X-Goog-Api-Client", ytDeps.ApplicationName)
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("YouTube API returned status %d: %s", resp.StatusCode, string(body))
	}

	var result YouTubeChannelStats
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w (body: %s)", err, string(body))
	}

	if len(result.Items) == 0 {
		return nil, fmt.Errorf("no channel found for ID %s", channelID)
	}

	return &result, nil
}

// FetchYouTubeTranscriptDirectly attempts to fetch a YouTube transcript by scraping the watch page.
func (a *Activities) FetchYouTubeTranscriptDirectly(ctx context.Context, httpClient *http.Client, videoID string, preferredLanguage string) (string, error) {
	logger := activity.GetLogger(ctx)
	if preferredLanguage == "" {
		preferredLanguage = "en"
	}
	logger.Info("Fetching YouTube transcript directly (scraping)", "video_id", videoID, "language", preferredLanguage)

	watchURL := fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID)
	req, err := http.NewRequestWithContext(ctx, "GET", watchURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request for watch page: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch watch page %s: %w", watchURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to fetch watch page %s: status code %d", watchURL, resp.StatusCode)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read watch page body: %w", err)
	}
	bodyString := string(bodyBytes)

	startMarker := "ytInitialPlayerResponse = "
	startIndex := strings.Index(bodyString, startMarker)
	if startIndex == -1 {
		return "", fmt.Errorf("could not find '%s' marker in watch page HTML", startMarker)
	}
	startIndex += len(startMarker)

	braceLevel := 0
	endIndex := -1
	inString := false
	for i := startIndex; i < len(bodyString); i++ {
		switch bodyString[i] {
		case '{':
			if !inString {
				braceLevel++
			}
		case '}':
			if !inString {
				braceLevel--
				if braceLevel == 0 {
					endIndex = i + 1
					goto endLoop
				}
			}
		case '"':
			if !(i > 0 && bodyString[i-1] == '\\') {
				inString = !inString
			}
		}
	}
endLoop:

	if endIndex == -1 {
		return "", fmt.Errorf("could not find the end of the ytInitialPlayerResponse JSON object using brace counting")
	}

	jsonString := bodyString[startIndex:endIndex]

	var playerResponse ytInitialPlayerResponse
	if err := json.Unmarshal([]byte(jsonString), &playerResponse); err != nil {
		logSnippet := jsonString
		if len(logSnippet) > 1000 {
			logSnippet = logSnippet[:500] + "..." + logSnippet[len(logSnippet)-500:]
		}
		logger.Error("Failed to unmarshal ytInitialPlayerResponse JSON", "error", err, "extracted_json_snippet", logSnippet)
		return "", fmt.Errorf("failed to unmarshal player response JSON: %w", err)
	}

	if playerResponse.Captions == nil ||
		playerResponse.Captions.PlayerCaptionsTracklistRenderer == nil ||
		playerResponse.Captions.PlayerCaptionsTracklistRenderer.CaptionTracks == nil {
		logger.Warn("No caption tracks found in player response JSON", "video_id", videoID)
		return "", fmt.Errorf("no caption tracks found for video %s", videoID)
	}

	var captionURL string
	foundPreferred := false
	captionTracks := *playerResponse.Captions.PlayerCaptionsTracklistRenderer.CaptionTracks

	for _, track := range captionTracks {
		if track.LanguageCode == preferredLanguage {
			captionURL = track.BaseUrl
			foundPreferred = true
			logger.Info("Found preferred language caption track", "language", preferredLanguage, "url", captionURL)
			break
		}
	}

	if !foundPreferred && preferredLanguage != "en" {
		for _, track := range captionTracks {
			if track.LanguageCode == "en" {
				captionURL = track.BaseUrl
				logger.Info("Preferred language not found, using default English caption track", "url", captionURL)
				break
			}
		}
	}

	if captionURL == "" && len(captionTracks) > 0 {
		captionURL = captionTracks[0].BaseUrl
		logger.Warn("Preferred/default language not found, using first available caption track", "language", captionTracks[0].LanguageCode, "url", captionURL)
	}

	if captionURL == "" {
		return "", fmt.Errorf("no suitable caption track URL found for video %s", videoID)
	}

	captionReq, err := http.NewRequestWithContext(ctx, "GET", captionURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request for caption URL %s: %w", captionURL, err)
	}
	captionResp, err := httpClient.Do(captionReq)
	if err != nil {
		return "", fmt.Errorf("failed to fetch caption content from %s: %w", captionURL, err)
	}
	defer captionResp.Body.Close()

	if captionResp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to fetch caption content from %s: status code %d", captionURL, captionResp.StatusCode)
	}

	captionBytes, err := io.ReadAll(captionResp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read caption content body: %w", err)
	}

	logger.Info("Successfully fetched transcript directly", "video_id", videoID, "language", preferredLanguage, "bytes", len(captionBytes))
	return string(captionBytes), nil
}

// ytInitialPlayerResponse is used to parse the JSON data embedded in the YouTube watch page HTML
type ytInitialPlayerResponse struct {
	Captions *struct {
		PlayerCaptionsTracklistRenderer *struct {
			CaptionTracks *[]struct {
				BaseUrl        string `json:"baseUrl"`
				LanguageCode   string `json:"languageCode"`
				IsTranslatable bool   `json:"isTranslatable"`
			} `json:"captionTracks"`
		} `json:"playerCaptionsTracklistRenderer"`
	} `json:"captions"`
}
