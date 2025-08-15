package abb

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/brojonat/affiliate-bounty-board/http/api"
	"go.temporal.io/sdk/activity"
	temporal_log "go.temporal.io/sdk/log"
)

// RedditDependencies holds the dependencies for Reddit-related activities
type RedditDependencies struct {
	UserAgent          string    // Reddit requires a user agent string
	Username           string    // Reddit username for authentication
	Password           string    // Reddit password for authentication
	ClientID           string    // Reddit client ID for authentication
	ClientSecret       string    // Reddit client secret for authentication
	RedditAuthToken    string    // Cached Reddit auth token
	RedditAuthTokenExp time.Time // When the auth token expires
}

// ensureValidRedditToken ensures the Reddit auth token is valid for at least the specified duration
func (deps *RedditDependencies) ensureValidRedditToken(minRemaining time.Duration) error {
	if deps.RedditAuthToken == "" || time.Now().Add(minRemaining).After(deps.RedditAuthTokenExp) {
		client := &http.Client{
			Timeout: 10 * time.Second,
		}
		token, err := getRedditAuthTokenForPull(client, *deps)
		if err != nil {
			return fmt.Errorf("failed to get Reddit token: %w", err)
		}
		deps.RedditAuthToken = token
		deps.RedditAuthTokenExp = time.Now().Add(1 * time.Hour)
	}
	return nil
}

// MarshalJSON implements json.Marshaler for RedditDependencies
func (deps RedditDependencies) MarshalJSON() ([]byte, error) {
	type Aux struct {
		UserAgent          string    `json:"user_agent"`
		Username           string    `json:"username"`
		Password           string    `json:"password"`
		ClientID           string    `json:"client_id"`
		ClientSecret       string    `json:"client_secret"`
		RedditAuthToken    string    `json:"reddit_auth_token"`
		RedditAuthTokenExp time.Time `json:"reddit_auth_token_exp"`
	}

	aux := Aux{
		UserAgent:          deps.UserAgent,
		Username:           deps.Username,
		Password:           deps.Password,
		ClientID:           deps.ClientID,
		ClientSecret:       deps.ClientSecret,
		RedditAuthToken:    deps.RedditAuthToken,
		RedditAuthTokenExp: deps.RedditAuthTokenExp,
	}

	return json.Marshal(aux)
}

// UnmarshalJSON implements json.Unmarshaler for RedditDependencies
func (deps *RedditDependencies) UnmarshalJSON(data []byte) error {
	type Aux struct {
		UserAgent          string    `json:"user_agent"`
		Username           string    `json:"username"`
		Password           string    `json:"password"`
		ClientID           string    `json:"client_id"`
		ClientSecret       string    `json:"client_secret"`
		RedditAuthToken    string    `json:"reddit_auth_token"`
		RedditAuthTokenExp time.Time `json:"reddit_auth_token_exp"`
	}

	var aux Aux
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	deps.UserAgent = aux.UserAgent
	deps.Username = aux.Username
	deps.Password = aux.Password
	deps.ClientID = aux.ClientID
	deps.ClientSecret = aux.ClientSecret
	deps.RedditAuthToken = aux.RedditAuthToken
	deps.RedditAuthTokenExp = aux.RedditAuthTokenExp

	return nil
}

// RedditContent represents the extracted content from Reddit
type RedditContent struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Selftext    string    `json:"selftext"`
	URL         string    `json:"url"`
	Body        string    `json:"body"`
	Author      string    `json:"author"`
	Subreddit   string    `json:"subreddit"`
	Score       int       `json:"score"`
	Created     time.Time `json:"created_utc"`
	Edited      bool      `json:"edited"`
	IsComment   bool      `json:"is_comment"`
	Permalink   string    `json:"permalink"`
	NumComments int       `json:"num_comments"`
	IsStickied  bool      `json:"is_stickied"`
	IsLocked    bool      `json:"is_locked"`
	IsNSFW      bool      `json:"is_nsfw"`
	IsSpoiler   bool      `json:"is_spoiler"`
	Flair       string    `json:"flair"`
	Thumbnail   string    `json:"thumbnail"` // Added Thumbnail field
}

// RedditUserStats represents the stats for a given reddit user
type RedditUserStats struct {
	Kind string `json:"kind"`
	Data struct {
		IsEmployee           bool        `json:"is_employee"`
		IsFriend             bool        `json:"is_friend"`
		Subreddit            interface{} `json:"subreddit"`
		SnoovatarSize        []int       `json:"snoovatar_size"`
		AwardeeKarma         int         `json:"awardee_karma"`
		ID                   string      `json:"id"`
		Verified             bool        `json:"verified"`
		IsMod                bool        `json:"is_mod"`
		AwarderKarma         int         `json:"awarder_karma"`
		HasVerifiedEmail     bool        `json:"has_verified_email"`
		IconImg              string      `json:"icon_img"`
		Name                 string      `json:"name"`
		IsSuspended          bool        `json:"is_suspended"`
		TotalKarma           int         `json:"total_karma"`
		CommentKarma         int         `json:"comment_karma"`
		LinkKarma            int         `json:"link_karma"`
		Created              float64     `json:"created"`
		CreatedUTC           float64     `json:"created_utc"`
		SnoovatarImg         string      `json:"snoovatar_img"`
		AcceptFollowers      bool        `json:"accept_followers"`
		HasSubscribed        bool        `json:"has_subscribed"`
		IsBlocked            bool        `json:"is_blocked"`
		PrefShowSnoovatar    bool        `json:"pref_show_snoovatar"`
		IsGold               bool        `json:"is_gold"`
		CanBeFollowed        bool        `json:"can_be_followed"`
		IsSuspendedForViolat bool        `json:"is_suspended_for_violating_the_rules"`
		HideFromRobots       bool        `json:"hide_from_robots"`
		PrefVideoAutoplay    bool        `json:"pref_video_autoplay"`
		HasModMail           bool        `json:"has_mod_mail"`
		PrefNoProfanity      bool        `json:"pref_no_profanity"`
		PrefTopKarmaSubreddi bool        `json:"pref_top_karma_subreddits"`
		CanCreateSubreddit   bool        `json:"can_create_subreddit"`
		Over18               bool        `json:"over_18"`
	} `json:"data"`
}

// Subreddit represents the stats for a given subreddit
type Subreddit struct {
	Kind string `json:"kind"`
	Data struct {
		UserFlairBackgroundColor        interface{}   `json:"user_flair_background_color"`
		SubmitTextHTML                  string        `json:"submit_text_html"`
		RestrictPosting                 bool          `json:"restrict_posting"`
		UserIsBanned                    bool          `json:"user_is_banned"`
		FreeFormReports                 bool          `json:"free_form_reports"`
		WikiEnabled                     bool          `json:"wiki_enabled"`
		UserIsMuted                     bool          `json:"user_is_muted"`
		UserCanFlairInSr                bool          `json:"user_can_flair_in_sr"`
		DisplayName                     string        `json:"display_name"`
		HeaderImg                       string        `json:"header_img"`
		Title                           string        `json:"title"`
		IconSize                        []int         `json:"icon_size"`
		PrimaryColor                    string        `json:"primary_color"`
		ActiveUserCount                 int           `json:"active_user_count"`
		IconImg                         string        `json:"icon_img"`
		DisplayNamePrefixed             string        `json:"display_name_prefixed"`
		AccountsActive                  int           `json:"accounts_active"`
		PublicTraffic                   bool          `json:"public_traffic"`
		Subscribers                     int           `json:"subscribers"`
		UserFlairRichtext               []interface{} `json:"user_flair_richtext"`
		VideostreamLinksCount           int           `json:"videostream_links_count"`
		Name                            string        `json:"name"`
		Quarantine                      bool          `json:"quarantine"`
		HideAds                         bool          `json:"hide_ads"`
		PredictionLeaderboardEntryType  interface{}   `json:"prediction_leaderboard_entry_type"`
		EmojisEnabled                   bool          `json:"emojis_enabled"`
		AdvertiserCategory              string        `json:"advertiser_category"`
		PublicDescription               string        `json:"public_description"`
		CommentScoreHideMins            int           `json:"comment_score_hide_mins"`
		AllowTalks                      bool          `json:"allow_talks"`
		UserHasFavorited                bool          `json:"user_has_favorited"`
		UserFlairTemplateID             interface{}   `json:"user_flair_template_id"`
		CommunityIcon                   string        `json:"community_icon"`
		BannerBackgroundImage           string        `json:"banner_background_image"`
		OriginalContentTagEnabled       bool          `json:"original_content_tag_enabled"`
		SubmitText                      string        `json:"submit_text"`
		DescriptionHTML                 string        `json:"description_html"`
		SuggestCommentSort              interface{}   `json:"suggested_comment_sort"`
		UserFlairPosition               string        `json:"user_flair_position"`
		AllOriginalContent              bool          `json:"all_original_content"`
		HasMenuWidget                   bool          `json:"has_menu_widget"`
		IsEnrolledInNewModmail          interface{}   `json:"is_enrolled_in_new_modmail"`
		KeyColor                        string        `json:"key_color"`
		CanAssignUserFlair              bool          `json:"can_assign_user_flair"`
		Created                         float64       `json:"created"`
		WLS                             int           `json:"wls"`
		ShowMediaPreview                bool          `json:"show_media_preview"`
		SubmissionType                  string        `json:"submission_type"`
		UserIsSubscriber                bool          `json:"user_is_subscriber"`
		DisableContributorRequests      bool          `json:"disable_contributor_requests"`
		AllowVideogifs                  bool          `json:"allow_videogifs"`
		UserFlairType                   string        `json:"user_flair_type"`
		CollapseDeletedComments         bool          `json:"collapse_deleted_comments"`
		EmojisCustomSize                interface{}   `json:"emojis_custom_size"`
		PublicDescriptionHTML           string        `json:"public_description_html"`
		AllowGalleries                  bool          `json:"allow_galleries"`
		UserFlairTextColor              interface{}   `json:"user_flair_text_color"`
		BannerSize                      []int         `json:"banner_size"`
		UserSrFlairEnabled              bool          `json:"user_sr_flair_enabled"`
		ShowMedia                       bool          `json:"show_media"`
		IsCrosspostableSubreddit        bool          `json:"is_crosspostable_subreddit"`
		NotificationLevel               string        `json:"notification_level"`
		CanAssignLinkFlair              bool          `json:"can_assign_link_flair"`
		UserFlairEnabledInSr            bool          `json:"user_flair_enabled_in_sr"`
		AllowDiscovery                  bool          `json:"allow_discovery"`
		UserSrThemeEnabled              bool          `json:"user_sr_theme_enabled"`
		LinkFlairPosition               string        `json:"link_flair_position"`
		UserIsContributor               bool          `json:"user_is_contributor"`
		RestrictCommenting              bool          `json:"restrict_commenting"`
		AllowImages                     bool          `json:"allow_images"`
		Lang                            string        `json:"lang"`
		WhitelistStatus                 string        `json:"whitelist_status"`
		URL                             string        `json:"url"`
		CreatedUTC                      float64       `json:"created_utc"`
		BannerImg                       string        `json:"banner_img"`
		UserIsModerator                 bool          `json:"user_is_moderator"`
		AllowPredictions                bool          `json:"allow_predictions"`
		Over18                          bool          `json:"over18"`
		HeaderSize                      []int         `json:"header_size"`
		UserFlairCSSClass               interface{}   `json:"user_flair_css_class"`
		SubmitLinkLabel                 string        `json:"submit_link_label"`
		UserFlairText                   interface{}   `json:"user_flair_text"`
		AllowPredictionsTournament      bool          `json:"allow_predictions_tournament"`
		LinkFlairEnabled                bool          `json:"link_flair_enabled"`
		SubredditType                   string        `json:"subreddit_type"`
		ID                              string        `json:"id"`
		AccountsActiveIsFuzzed          bool          `json:"accounts_active_is_fuzzed"`
		AllowPolls                      bool          `json:"allow_polls"`
		AllowVideos                     bool          `json:"allow_videos"`
		SpoilersEnabled                 bool          `json:"spoilers_enabled"`
		MobileBannerImage               string        `json:"mobile_banner_image"`
		Description                     string        `json:"description"`
		CommunityReviewed               bool          `json:"community_reviewed"`
		HeaderTitle                     string        `json:"header_title"`
		UserIsMutedInSr                 bool          `json:"user_is_muted_in_sr"`
		IsChatPostFeatureEnabled        bool          `json:"is_chat_post_feature_enabled"`
		AllowChatPostCreation           bool          `json:"allow_chat_post_creation"`
		AllowPredictionContributors     bool          `json:"allow_prediction_contributors"`
		IsAcceptingNewSentimentFeatures bool          `json:"is_accepting_new_sentiment_features"`
		AcceptFollowers                 bool          `json:"accept_followers"`
		UserFlairBackgroundColorInSr    interface{}   `json:"user_flair_background_color_in_sr"`
		EventPostsEnabled               bool          `json:"event_posts_enabled"`
		CommunityChatChannel            string        `json:"community_chat_channel"`
		IsFollowing                     bool          `json:"is_following"`
	} `json:"data"`
}

// UnmarshalJSON implements custom unmarshaling for RedditContent
func (r *RedditContent) UnmarshalJSON(data []byte) error {
	// First unmarshal into a map to handle the created_utc field flexibly
	var rawData map[string]interface{}
	if err := json.Unmarshal(data, &rawData); err != nil {
		return err
	}

	// Handle the created_utc field separately
	var createdTime time.Time
	switch v := rawData["created_utc"].(type) {
	case float64:
		createdTime = time.Unix(int64(v), 0)
	case string:
		// Try parsing as ISO 8601 first
		var err error
		createdTime, err = time.Parse(time.RFC3339, v)
		if err != nil {
			// If that fails, try parsing as a Unix timestamp string
			timestamp, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return fmt.Errorf("failed to parse created_utc timestamp: %w", err)
			}
			createdTime = time.Unix(int64(timestamp), 0)
		}
	case json.Number:
		timestamp, err := v.Float64()
		if err != nil {
			return fmt.Errorf("failed to parse created_utc timestamp: %w", err)
		}
		createdTime = time.Unix(int64(timestamp), 0)
	default:
		// Allow for created_utc to be missing or null, default to zero time
		createdTime = time.Time{}
	}

	// Handle the edited field
	r.Edited = false // Default to false (not edited)
	if editedVal, ok := rawData["edited"]; ok {
		switch v := editedVal.(type) {
		case bool:
			// If Reddit sends a boolean, it's 'false' for not edited.
			// If 'v' is somehow true here (unexpected), r.Edited would become true.
			r.Edited = v
		case float64:
			// If Reddit sends a number, it's a Unix timestamp (potentially 0.0 if not edited but field is present)
			if v > 0 { // Any positive timestamp indicates an edit
				r.Edited = true
			}
			// If v is 0.0 or negative, r.Edited remains false (due to default or if bool case set it)
		case string:
			// Less common for 'edited', but if it's a string representation of the timestamp or 'false'
			if tsFloat, err := strconv.ParseFloat(v, 64); err == nil && tsFloat > 0 {
				r.Edited = true
			} else if strings.ToLower(v) == "false" {
				r.Edited = false
			}
		}
	}

	type Aux struct {
		ID          string `json:"id"`
		Title       string `json:"title"`
		Selftext    string `json:"selftext"`
		URL         string `json:"url"`
		Body        string `json:"body"`
		Author      string `json:"author"`
		Subreddit   string `json:"subreddit"`
		Score       int    `json:"score"`
		IsComment   bool   `json:"is_comment"`
		Permalink   string `json:"permalink"`
		NumComments int    `json:"num_comments"`
		IsStickied  bool   `json:"stickied"`
		IsLocked    bool   `json:"locked"`
		IsNSFW      bool   `json:"over_18"`
		IsSpoiler   bool   `json:"spoiler"`
		Flair       string `json:"link_flair_text"`
		Thumbnail   string `json:"thumbnail"`
	}

	var aux Aux
	if err := json.Unmarshal(data, &aux); err != nil {
		return fmt.Errorf("failed to unmarshal reddit aux struct: %w (data: %s)", err, string(data))
	}

	r.ID = aux.ID
	r.Title = aux.Title
	r.Selftext = aux.Selftext
	r.URL = aux.URL
	r.Body = aux.Body
	r.Author = aux.Author
	r.Subreddit = aux.Subreddit
	r.Score = aux.Score
	r.Created = createdTime
	r.IsComment = aux.IsComment
	r.Permalink = aux.Permalink
	r.NumComments = aux.NumComments
	r.IsStickied = aux.IsStickied
	r.IsLocked = aux.IsLocked
	r.IsNSFW = aux.IsNSFW
	r.IsSpoiler = aux.IsSpoiler
	r.Flair = aux.Flair
	r.Thumbnail = aux.Thumbnail

	return nil
}

// GetRedditUserStats fetches user stats from the Reddit API
func (a *Activities) GetRedditUserStats(ctx context.Context, username string) (*RedditUserStats, error) {
	logger := activity.GetLogger(ctx)
	cfg, err := getConfiguration(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get configuration: %w", err)
	}

	deps := &cfg.RedditDeps
	if err := deps.ensureValidRedditToken(1 * time.Minute); err != nil {
		return nil, fmt.Errorf("failed to ensure valid reddit token: %w", err)
	}

	url := fmt.Sprintf("https://oauth.reddit.com/user/%s/about", username)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+deps.RedditAuthToken)
	req.Header.Set("User-Agent", deps.UserAgent)

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
		logger.Error("failed to get user stats", "status", resp.StatusCode, "body", string(body))
		return nil, fmt.Errorf("failed to get user stats: %s", string(body))
	}

	var userStats RedditUserStats
	if err := json.Unmarshal(body, &userStats); err != nil {
		return nil, fmt.Errorf("failed to unmarshal user stats: %w", err)
	}

	return &userStats, nil
}

// GetSubreddit fetches subreddit stats from the Reddit API
func (a *Activities) GetSubreddit(ctx context.Context, subredditName string) (*Subreddit, error) {
	logger := activity.GetLogger(ctx)
	cfg, err := getConfiguration(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get configuration: %w", err)
	}

	deps := &cfg.RedditDeps
	if err := deps.ensureValidRedditToken(1 * time.Minute); err != nil {
		return nil, fmt.Errorf("failed to ensure valid reddit token: %w", err)
	}

	url := fmt.Sprintf("https://oauth.reddit.com/r/%s/about", subredditName)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+deps.RedditAuthToken)
	req.Header.Set("User-Agent", deps.UserAgent)

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
		logger.Error("failed to get subreddit stats", "status", resp.StatusCode, "body", string(body))
		return nil, fmt.Errorf("failed to get subreddit stats: %s", string(body))
	}

	var subredditStats Subreddit
	if err := json.Unmarshal(body, &subredditStats); err != nil {
		return nil, fmt.Errorf("failed to unmarshal subreddit stats: %w", err)
	}

	return &subredditStats, nil
}

// getRedditAuthTokenForPull obtains an authentication token from Reddit (used by PullRedditContent)
func getRedditAuthTokenForPull(client *http.Client, deps RedditDependencies) (string, error) {
	form := url.Values{}
	form.Add("grant_type", "password")
	form.Add("username", deps.Username)
	form.Add("password", deps.Password)
	encodedForm := form.Encode()

	req, err := http.NewRequest(http.MethodPost, "https://www.reddit.com/api/v1/access_token", strings.NewReader(encodedForm))
	if err != nil {
		return "", fmt.Errorf("failed to create token request: %w", err)
	}

	req.Header.Set("User-Agent", deps.UserAgent)
	auth := base64.StdEncoding.EncodeToString([]byte(deps.ClientID + ":" + deps.ClientSecret))
	req.Header.Set("Authorization", "Basic "+auth)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Content-Length", strconv.Itoa(len(encodedForm)))

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to make token request: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read token response body (status %d): %w", resp.StatusCode, err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Reddit token API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error,omitempty"`
	}

	if err := json.Unmarshal(bodyBytes, &result); err != nil {
		return "", fmt.Errorf("failed to decode token response: %w (body: %s)", err, string(bodyBytes))
	}

	if result.AccessToken == "" {
		errMsg := "Reddit token response was successful but contained an empty access token."
		if result.Error != "" {
			errMsg = fmt.Sprintf("%s Reddit API error field: '%s'.", errMsg, result.Error)
		}
		return "", fmt.Errorf("%s Response body: %s", errMsg, string(bodyBytes))
	}
	return result.AccessToken, nil
}

// --- Bounty Publisher Activities (Reddit Specific) ---

// PublishNewBountyReddit fetches a specific bounty and posts it to Reddit as a new bounty announcement.
func (a *Activities) PublishNewBountyReddit(ctx context.Context, bountyID string) error {
	logger := activity.GetLogger(ctx)
	logger.Info("Running PublishNewBountyReddit activity...", "bounty_id", bountyID)

	cfg, err := getConfiguration(ctx)
	if err != nil {
		return fmt.Errorf("failed to get configuration: %w", err)
	}

	client := a.httpClient

	abbToken, err := a.getABBAuthToken(ctx, logger, cfg, client)
	if err != nil {
		logger.Error("Failed to get ABB auth token", "error", err)
		return err
	}
	logger.Debug("Obtained ABB auth token")

	// Fetch the specific bounty by ID
	bounty, err := a.fetchSingleBounty(ctx, logger, cfg, client, abbToken, bountyID)
	if err != nil {
		logger.Error("Failed to fetch bounty", "bounty_id", bountyID, "error", err)
		return err
	}
	logger.Info("Fetched bounty for Reddit announcement", "bounty_id", bountyID)

	postTitle, postBody, err := a.formatNewBountyForReddit(bounty, cfg.ABBServerConfig.PublicBaseURL)
	if err != nil {
		logger.Error("Failed to format bounty", "bounty_id", bountyID, "error", err)
		return fmt.Errorf("internal error formatting bounty: %w", err)
	}
	logger.Debug("Formatted bounty for Reddit post")

	redditToken, err := a.getRedditToken(ctx, logger, cfg, client)
	if err != nil {
		logger.Error("Failed to get Reddit auth token", "error", err)
		return err
	}
	logger.Debug("Obtained Reddit auth token")

	err = a.postToReddit(ctx, logger, cfg, client, redditToken, postTitle, postBody, cfg.RedditFlairID)
	if err != nil {
		logger.Error("Failed to post to Reddit", "bounty_id", bountyID, "error", err)
		return err
	}

	logger.Info("Successfully posted new bounty to Reddit", "subreddit", cfg.PublishTargetSubreddit, "bounty_id", bountyID)
	return nil
}

// getRedditToken (method for publisher) gets an OAuth2 token from Reddit
func (a *Activities) getRedditToken(ctx context.Context, logger temporal_log.Logger, cfg *Configuration, client *http.Client) (string, error) {
	tokenURL := "https://www.reddit.com/api/v1/access_token"
	form := url.Values{}
	form.Add("grant_type", "password")
	form.Add("username", cfg.RedditDeps.Username)
	form.Add("password", cfg.RedditDeps.Password)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("failed to create reddit token request: %w", err)
	}

	req.SetBasicAuth(cfg.RedditDeps.ClientID, cfg.RedditDeps.ClientSecret)
	req.Header.Set("User-Agent", cfg.RedditDeps.UserAgent)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("reddit token request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		logger.Error("Reddit token request failed", "status", resp.StatusCode, "body", string(bodyBytes))
		return "", fmt.Errorf("reddit token request returned status %d", resp.StatusCode)
	}

	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Scope       string `json:"scope"`
		TokenType   string `json:"token_type"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode reddit token response: %w", err)
	}

	if result.AccessToken == "" {
		return "", fmt.Errorf("reddit token response did not contain an access token")
	}

	logger.Debug("Successfully obtained Reddit access token", "scope", result.Scope, "expires_in", result.ExpiresIn)
	return result.AccessToken, nil
}

// postToReddit posts the formatted content to the specified subreddit
func (a *Activities) postToReddit(ctx context.Context, logger temporal_log.Logger, cfg *Configuration, client *http.Client, token, title, body, flairID string) error {
	submitURL := "https://oauth.reddit.com/api/submit"

	form := url.Values{}
	form.Add("api_type", "json")
	form.Add("kind", "self")
	form.Add("sr", cfg.PublishTargetSubreddit)
	form.Add("title", title)
	form.Add("text", body)
	if flairID != "" { // Only add flair_id if it's provided
		form.Add("flair_id", flairID)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, submitURL, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("failed to create reddit submit request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", cfg.RedditDeps.UserAgent)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("reddit submit request failed: %w", err)
	}
	defer resp.Body.Close()

	respBodyBytes, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		logger.Error("Reddit submit request failed", "status", resp.StatusCode, "body", string(respBodyBytes))
		return fmt.Errorf("reddit submit request returned status %d: %s", resp.StatusCode, string(respBodyBytes))
	}

	var result struct {
		JSON struct {
			Errors [][]interface{} `json:"errors"` // Changed to interface{} for flexibility
			Data   struct {
				URL string `json:"url"`
			} `json:"data"`
		} `json:"json"`
	}

	if err := json.Unmarshal(respBodyBytes, &result); err != nil {
		logger.Warn("Failed to decode reddit submit response JSON, but status was OK", "error", err, "response_body", string(respBodyBytes))
	} else if len(result.JSON.Errors) > 0 {
		logger.Error("Reddit API reported errors after submission", "errors", result.JSON.Errors, "response_body", string(respBodyBytes))
		// Format errors for better display
		var errorMessages []string
		for _, errGroup := range result.JSON.Errors {
			var currentError []string
			for _, e := range errGroup {
				currentError = append(currentError, fmt.Sprintf("%v", e))
			}
			errorMessages = append(errorMessages, strings.Join(currentError, ", "))
		}
		return fmt.Errorf("reddit API returned errors: [%s]", strings.Join(errorMessages, "; "))
	} else {
		logger.Info("Reddit post submitted successfully", "post_url", result.JSON.Data.URL)
	}

	return nil
}

// fetchSingleBounty fetches a specific bounty by ID from the ABB server
func (a *Activities) fetchSingleBounty(ctx context.Context, logger temporal_log.Logger, cfg *Configuration, client *http.Client, token, bountyID string) (*api.BountyListItem, error) {
	bountyURL := fmt.Sprintf("%s/bounties/%s", strings.TrimSuffix(cfg.ABBServerConfig.APIEndpoint, "/"), bountyID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, bountyURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create bounty request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bounty request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("bounty request returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var bounty api.BountyListItem
	if err := json.NewDecoder(resp.Body).Decode(&bounty); err != nil {
		return nil, fmt.Errorf("failed to decode bounty response: %w", err)
	}

	return &bounty, nil
}

// formatNewBountyForReddit formats a single bounty into a Reddit post announcement
func (a *Activities) formatNewBountyForReddit(bounty *api.BountyListItem, publicBaseURL string) (string, string, error) {
	title := fmt.Sprintf("🆕 New Bounty Available: %s", bounty.Title)
	if title == "" || bounty.Title == "" {
		title = fmt.Sprintf("🆕 New %s Bounty Available on %s", bounty.ContentKind, bounty.PlatformKind)
	}

	var body strings.Builder
	body.WriteString(fmt.Sprintf("A new bounty has just been posted and is now available for claims:\n\n"))

	bountyURL := fmt.Sprintf("%s/bounties/%s", strings.TrimSuffix(publicBaseURL, "/"), bounty.BountyID)
	body.WriteString(fmt.Sprintf("**🎯 [%s](%s)**\n\n", bounty.BountyID, bountyURL))

	body.WriteString(fmt.Sprintf("- **Platform**: %s (%s)\n", bounty.PlatformKind, bounty.ContentKind))
	body.WriteString(fmt.Sprintf("- **Reward per Post**: $%.2f\n", bounty.BountyPerPost))
	body.WriteString(fmt.Sprintf("- **Total Bounty Pool**: $%.2f\n", bounty.TotalBounty))
	if !bounty.EndAt.IsZero() {
		body.WriteString(fmt.Sprintf("- **Expires**: %s\n", bounty.EndAt.Format("January 2, 2006 at 15:04 MST")))
	}
	body.WriteString("\n**Requirements:**\n")
	for i, req := range bounty.Requirements {
		body.WriteString(fmt.Sprintf("%d. %s\n", i+1, req))
	}

	body.WriteString("\n---\n\n")
	body.WriteString("Get started by visiting the bounty page above!")

	return title, body.String(), nil
}
