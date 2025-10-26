package abb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"go.temporal.io/sdk/activity"
)

const (
	// StackOverflowUserFilter is our custom Stack Exchange API filter for user profiles.
	// This filter includes the critical fields where wallet addresses may be stored:
	// - user.about_me (HTML bio - primary location)
	// - user.location (text field - some users put wallet here)
	// - user.website_url (URL field - could link to wallet)
	//
	// Created via: /filters/create?base=default&include=user.about_me;user.location;user.website_url&unsafe=true
	//
	// Stack Exchange filters are immutable and non-expiring (permanent).
	// See: https://api.stackexchange.com/docs/filters
	StackOverflowUserFilter = "Dh6gvXqpGDFcXaUgK"
)

// StackOverflowDependencies holds dependencies for Stack Overflow activities
type StackOverflowDependencies struct {
	APIKey string // Optional API key for higher rate limits
	Site   string // Stack Exchange site (e.g., "stackoverflow", "serverfault")
}

// Type returns the platform type for Stack Overflow activities
func (deps StackOverflowDependencies) Type() PlatformKind {
	return PlatformStackOverflow
}

// StackOverflowQuestion represents a Stack Overflow question
type StackOverflowQuestion struct {
	QuestionID       int      `json:"question_id"`
	Title            string   `json:"title"`
	Body             string   `json:"body"`
	BodyMarkdown     string   `json:"body_markdown"`
	Link             string   `json:"link"`
	Score            int      `json:"score"`
	ViewCount        int      `json:"view_count"`
	AnswerCount      int      `json:"answer_count"`
	IsAnswered       bool     `json:"is_answered"`
	AcceptedAnswerID int      `json:"accepted_answer_id,omitempty"`
	Tags             []string `json:"tags"`
	OwnerUserID      int      `json:"owner_user_id"`
	OwnerDisplayName string   `json:"owner_display_name"`
	CreationDate     int64    `json:"creation_date"`
	LastActivityDate int64    `json:"last_activity_date"`
}

// StackOverflowAnswer represents a Stack Overflow answer
type StackOverflowAnswer struct {
	AnswerID         int    `json:"answer_id"`
	QuestionID       int    `json:"question_id"`
	Body             string `json:"body"`
	BodyMarkdown     string `json:"body_markdown"`
	Link             string `json:"link"`
	Score            int    `json:"score"`
	IsAccepted       bool   `json:"is_accepted"`
	OwnerUserID      int    `json:"owner_user_id"`
	OwnerDisplayName string `json:"owner_display_name"`
	CreationDate     int64  `json:"creation_date"`
	LastActivityDate int64  `json:"last_activity_date"`
}

// StackOverflowUser represents a Stack Overflow user profile
type StackOverflowUser struct {
	UserID       int    `json:"user_id"`
	DisplayName  string `json:"display_name"`
	AboutMe      string `json:"about_me"`       // HTML bio where wallet address may be
	Location     string `json:"location"`       // Some users put wallet here
	WebsiteURL   string `json:"website_url"`    // Could link to wallet
	ProfileImage string `json:"profile_image"`
	Reputation   int    `json:"reputation"`
	Link         string `json:"link"`
}

// stackExchangeResponse is the generic response wrapper from Stack Exchange API
type stackExchangeResponse struct {
	Items          []json.RawMessage `json:"items"`
	HasMore        bool              `json:"has_more"`
	QuotaMax       int               `json:"quota_max"`
	QuotaRemaining int               `json:"quota_remaining"`
	Backoff        int               `json:"backoff,omitempty"` // Backoff in seconds if rate limited
}

// fetchStackOverflowQuestion fetches a question by ID from Stack Overflow
func (a *Activities) fetchStackOverflowQuestion(ctx context.Context, deps StackOverflowDependencies, questionID string) (*StackOverflowQuestion, error) {
	logger := activity.GetLogger(ctx)

	// Add timeout to prevent hanging
	timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	site := deps.Site
	if site == "" {
		site = "stackoverflow" // Default to main Stack Overflow site
	}

	// Build API URL
	// Using /questions/{ids} endpoint with filter to get body_markdown
	// Filter "withbody" includes the body in HTML and markdown
	apiURL := fmt.Sprintf("https://api.stackexchange.com/2.3/questions/%s", questionID)
	params := url.Values{}
	params.Set("site", site)
	params.Set("filter", "!9_bDDxJY5") // Filter that includes body, body_markdown, and most fields
	if deps.APIKey != "" {
		params.Set("key", deps.APIKey)
	}

	fullURL := fmt.Sprintf("%s?%s", apiURL, params.Encode())
	logger.Debug("Fetching Stack Overflow question", "url", fullURL, "question_id", questionID)

	req, err := http.NewRequestWithContext(timeoutCtx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Stack Overflow question request: %w", err)
	}

	// Set User-Agent (required by Stack Exchange API)
	req.Header.Set("User-Agent", "AffiliateBountyBoard/1.0 (StackOverflowIntegration)")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		if timeoutCtx.Err() == context.DeadlineExceeded {
			logger.Error("Stack Overflow question fetch timed out after 30 seconds", "question_id", questionID)
			return nil, fmt.Errorf("Stack Overflow question fetch timed out: %w", err)
		}
		return nil, fmt.Errorf("Stack Overflow question request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read Stack Overflow question response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		logger.Error("Stack Overflow API returned non-200 status", "status_code", resp.StatusCode, "response_body", string(bodyBytes))
		return nil, fmt.Errorf("Stack Overflow API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var apiResp stackExchangeResponse
	if err := json.Unmarshal(bodyBytes, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse Stack Overflow response: %w", err)
	}

	// Check for backoff header (rate limiting)
	if apiResp.Backoff > 0 {
		logger.Warn("Stack Overflow API requested backoff", "backoff_seconds", apiResp.Backoff, "quota_remaining", apiResp.QuotaRemaining)
	}

	if len(apiResp.Items) == 0 {
		return nil, fmt.Errorf("Stack Overflow question not found: %s", questionID)
	}

	// Parse the question from the first item
	var rawQuestion map[string]interface{}
	if err := json.Unmarshal(apiResp.Items[0], &rawQuestion); err != nil {
		return nil, fmt.Errorf("failed to parse question data: %w", err)
	}

	question := &StackOverflowQuestion{
		QuestionID:       int(rawQuestion["question_id"].(float64)),
		Title:            rawQuestion["title"].(string),
		Link:             rawQuestion["link"].(string),
		Score:            int(rawQuestion["score"].(float64)),
		ViewCount:        int(rawQuestion["view_count"].(float64)),
		AnswerCount:      int(rawQuestion["answer_count"].(float64)),
		IsAnswered:       rawQuestion["is_answered"].(bool),
		CreationDate:     int64(rawQuestion["creation_date"].(float64)),
		LastActivityDate: int64(rawQuestion["last_activity_date"].(float64)),
	}

	// Optional fields
	if body, ok := rawQuestion["body"].(string); ok {
		question.Body = body
	}
	if bodyMd, ok := rawQuestion["body_markdown"].(string); ok {
		question.BodyMarkdown = bodyMd
	}
	if tags, ok := rawQuestion["tags"].([]interface{}); ok {
		question.Tags = make([]string, len(tags))
		for i, tag := range tags {
			question.Tags[i] = tag.(string)
		}
	}
	if acceptedID, ok := rawQuestion["accepted_answer_id"].(float64); ok {
		question.AcceptedAnswerID = int(acceptedID)
	}
	if owner, ok := rawQuestion["owner"].(map[string]interface{}); ok {
		if userID, ok := owner["user_id"].(float64); ok {
			question.OwnerUserID = int(userID)
		}
		if displayName, ok := owner["display_name"].(string); ok {
			question.OwnerDisplayName = displayName
		}
	}

	logger.Info("Successfully fetched Stack Overflow question", "question_id", questionID, "title", question.Title, "score", question.Score)
	return question, nil
}

// fetchStackOverflowAnswer fetches an answer by ID from Stack Overflow
func (a *Activities) fetchStackOverflowAnswer(ctx context.Context, deps StackOverflowDependencies, answerID string) (*StackOverflowAnswer, error) {
	logger := activity.GetLogger(ctx)

	// Add timeout to prevent hanging
	timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	site := deps.Site
	if site == "" {
		site = "stackoverflow"
	}

	// Build API URL
	apiURL := fmt.Sprintf("https://api.stackexchange.com/2.3/answers/%s", answerID)
	params := url.Values{}
	params.Set("site", site)
	params.Set("filter", "!9_bDDxJY5") // Filter that includes body and body_markdown
	if deps.APIKey != "" {
		params.Set("key", deps.APIKey)
	}

	fullURL := fmt.Sprintf("%s?%s", apiURL, params.Encode())
	logger.Debug("Fetching Stack Overflow answer", "url", fullURL, "answer_id", answerID)

	req, err := http.NewRequestWithContext(timeoutCtx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Stack Overflow answer request: %w", err)
	}

	req.Header.Set("User-Agent", "AffiliateBountyBoard/1.0 (StackOverflowIntegration)")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		if timeoutCtx.Err() == context.DeadlineExceeded {
			logger.Error("Stack Overflow answer fetch timed out after 30 seconds", "answer_id", answerID)
			return nil, fmt.Errorf("Stack Overflow answer fetch timed out: %w", err)
		}
		return nil, fmt.Errorf("Stack Overflow answer request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read Stack Overflow answer response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		logger.Error("Stack Overflow API returned non-200 status", "status_code", resp.StatusCode, "response_body", string(bodyBytes))
		return nil, fmt.Errorf("Stack Overflow API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var apiResp stackExchangeResponse
	if err := json.Unmarshal(bodyBytes, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse Stack Overflow response: %w", err)
	}

	if apiResp.Backoff > 0 {
		logger.Warn("Stack Overflow API requested backoff", "backoff_seconds", apiResp.Backoff, "quota_remaining", apiResp.QuotaRemaining)
	}

	if len(apiResp.Items) == 0 {
		return nil, fmt.Errorf("Stack Overflow answer not found: %s", answerID)
	}

	// Parse the answer from the first item
	var rawAnswer map[string]interface{}
	if err := json.Unmarshal(apiResp.Items[0], &rawAnswer); err != nil {
		return nil, fmt.Errorf("failed to parse answer data: %w", err)
	}

	answer := &StackOverflowAnswer{
		AnswerID:         int(rawAnswer["answer_id"].(float64)),
		QuestionID:       int(rawAnswer["question_id"].(float64)),
		Link:             rawAnswer["link"].(string),
		Score:            int(rawAnswer["score"].(float64)),
		IsAccepted:       rawAnswer["is_accepted"].(bool),
		CreationDate:     int64(rawAnswer["creation_date"].(float64)),
		LastActivityDate: int64(rawAnswer["last_activity_date"].(float64)),
	}

	// Optional fields
	if body, ok := rawAnswer["body"].(string); ok {
		answer.Body = body
	}
	if bodyMd, ok := rawAnswer["body_markdown"].(string); ok {
		answer.BodyMarkdown = bodyMd
	}
	if owner, ok := rawAnswer["owner"].(map[string]interface{}); ok {
		if userID, ok := owner["user_id"].(float64); ok {
			answer.OwnerUserID = int(userID)
		}
		if displayName, ok := owner["display_name"].(string); ok {
			answer.OwnerDisplayName = displayName
		}
	}

	logger.Info("Successfully fetched Stack Overflow answer", "answer_id", answerID, "score", answer.Score, "is_accepted", answer.IsAccepted)
	return answer, nil
}

// fetchStackOverflowUser fetches a user profile by user ID from Stack Overflow
// IMPORTANT: Uses filter !--1nZv)deGu1 to include about_me field where wallet address may be
func (a *Activities) fetchStackOverflowUser(ctx context.Context, deps StackOverflowDependencies, userID string) (*StackOverflowUser, error) {
	logger := activity.GetLogger(ctx)

	// Add timeout to prevent hanging
	timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	site := deps.Site
	if site == "" {
		site = "stackoverflow"
	}

	// Build API URL
	// CRITICAL: Use our custom filter to get about_me field (not included by default!)
	// about_me is where users can put their wallet address
	apiURL := fmt.Sprintf("https://api.stackexchange.com/2.3/users/%s", userID)
	params := url.Values{}
	params.Set("site", site)
	params.Set("filter", StackOverflowUserFilter) // Our custom filter that includes about_me, location, website_url
	if deps.APIKey != "" {
		params.Set("key", deps.APIKey)
	}

	fullURL := fmt.Sprintf("%s?%s", apiURL, params.Encode())
	logger.Debug("Fetching Stack Overflow user profile", "url", fullURL, "user_id", userID)

	req, err := http.NewRequestWithContext(timeoutCtx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Stack Overflow user request: %w", err)
	}

	req.Header.Set("User-Agent", "AffiliateBountyBoard/1.0 (StackOverflowIntegration)")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		if timeoutCtx.Err() == context.DeadlineExceeded {
			logger.Error("Stack Overflow user fetch timed out after 30 seconds", "user_id", userID)
			return nil, fmt.Errorf("Stack Overflow user fetch timed out: %w", err)
		}
		return nil, fmt.Errorf("Stack Overflow user request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read Stack Overflow user response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		logger.Error("Stack Overflow API returned non-200 status", "status_code", resp.StatusCode, "response_body", string(bodyBytes))
		return nil, fmt.Errorf("Stack Overflow API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var apiResp stackExchangeResponse
	if err := json.Unmarshal(bodyBytes, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse Stack Overflow response: %w", err)
	}

	if apiResp.Backoff > 0 {
		logger.Warn("Stack Overflow API requested backoff", "backoff_seconds", apiResp.Backoff, "quota_remaining", apiResp.QuotaRemaining)
	}

	if len(apiResp.Items) == 0 {
		return nil, fmt.Errorf("Stack Overflow user not found: %s", userID)
	}

	// Parse the user from the first item
	var rawUser map[string]interface{}
	if err := json.Unmarshal(apiResp.Items[0], &rawUser); err != nil {
		return nil, fmt.Errorf("failed to parse user data: %w", err)
	}

	user := &StackOverflowUser{
		UserID:      int(rawUser["user_id"].(float64)),
		DisplayName: rawUser["display_name"].(string),
		Link:        rawUser["link"].(string),
	}

	// Optional fields (where wallet address might be!)
	if aboutMe, ok := rawUser["about_me"].(string); ok {
		user.AboutMe = aboutMe
	}
	if location, ok := rawUser["location"].(string); ok {
		user.Location = location
	}
	if websiteURL, ok := rawUser["website_url"].(string); ok {
		user.WebsiteURL = websiteURL
	}
	if profileImage, ok := rawUser["profile_image"].(string); ok {
		user.ProfileImage = profileImage
	}
	if reputation, ok := rawUser["reputation"].(float64); ok {
		user.Reputation = int(reputation)
	}

	logger.Info("Successfully fetched Stack Overflow user profile", "user_id", userID, "display_name", user.DisplayName, "reputation", user.Reputation)
	return user, nil
}
