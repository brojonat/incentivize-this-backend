package abb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/brojonat/affiliate-bounty-board/http/api"
	"go.temporal.io/sdk/activity"
	temporal_log "go.temporal.io/sdk/log"
)

// PublishNewBountyDiscord fetches a specific bounty and posts it to Discord as a new bounty announcement.
func (a *Activities) PublishNewBountyDiscord(ctx context.Context, bountyID string) error {
	logger := activity.GetLogger(ctx)
	logger.Info("Running PublishNewBountyDiscord activity...", "bounty_id", bountyID)

	cfg, err := getConfiguration(ctx)
	if err != nil {
		return fmt.Errorf("failed to get configuration: %w", err)
	}

	if cfg.DiscordConfig.BotToken == "" || cfg.DiscordConfig.ChannelID == "" {
		logger.Warn("Discord Bot Token or Channel ID is not configured. Skipping Discord post.")
		return nil // Not an error, just skipping if not configured
	}

	client := a.httpClient

	abbToken, err := a.getABBAuthToken(ctx, logger, cfg, client)
	if err != nil {
		logger.Error("Failed to get ABB auth token for Discord publisher", "error", err)
		return err
	}
	logger.Debug("Obtained ABB auth token for Discord publisher")

	// Fetch the specific bounty by ID using the helper from Reddit activity
	bounty, err := a.fetchSingleBountyForDiscord(ctx, logger, cfg, client, abbToken, bountyID)
	if err != nil {
		logger.Error("Failed to fetch bounty for Discord publisher", "bounty_id", bountyID, "error", err)
		return err
	}
	logger.Info("Fetched bounty for Discord announcement", "bounty_id", bountyID)

	discordMessage, err := a.formatNewBountyForDiscord(bounty, cfg.ABBServerConfig.PublicBaseURL, cfg.Environment)
	if err != nil {
		logger.Error("Failed to format bounty for Discord", "bounty_id", bountyID, "error", err)
		return fmt.Errorf("internal error formatting bounty for Discord: %w", err)
	}
	logger.Debug("Formatted bounty for Discord message")

	err = a.postToDiscord(ctx, logger, cfg, client, discordMessage, cfg.Environment)
	if err != nil {
		logger.Error("Failed to post new bounty to Discord", "bounty_id", bountyID, "error", err)
		return err
	}

	logger.Info("Successfully posted new bounty to Discord", "channel_id", cfg.DiscordConfig.ChannelID, "bounty_id", bountyID)
	return nil
}

// formatNewBountyForDiscord formats a single bounty into a Discord message announcement
func (a *Activities) formatNewBountyForDiscord(bounty *api.BountyListItem, publicBaseURL string, env string) (string, error) {
	var sb strings.Builder
	envPrefix := ""
	if env != "" {
		envPrefix = fmt.Sprintf("[%s] ", strings.ToUpper(env))
	}

	// Use a different emoji and format for new bounty announcements
	sb.WriteString(fmt.Sprintf("%s🆕 **New Bounty Available!**\n\n", envPrefix))

	// Shorten requirements for Discord
	reqSummary := strings.Join(bounty.Requirements, ", ")
	if len(reqSummary) > 150 { // Slightly longer for new bounty announcements
		reqSummary = reqSummary[:147] + "..."
	}

	// Constructing a direct link to the bounty details page
	bountyURL := fmt.Sprintf("%s/bounties/%s", strings.TrimSuffix(publicBaseURL, "/"), bounty.BountyID)

	title := bounty.Title
	if title == "" {
		title = bounty.BountyID
	}

	sb.WriteString(fmt.Sprintf("### [%s](%s)\n", title, bountyURL))
	sb.WriteString(fmt.Sprintf("- **Platform**: %s (%s)\n", bounty.PlatformKind, bounty.ContentKind))
	sb.WriteString(fmt.Sprintf("- **Reward/Post**: $%.2f\n", bounty.BountyPerPost))
	sb.WriteString(fmt.Sprintf("- **Total Bounty Pool**: $%.2f\n", bounty.TotalBounty))
	if !bounty.EndAt.IsZero() {
		sb.WriteString(fmt.Sprintf("- **Expires**: %s\n", bounty.EndAt.Format("Jan 2, 2006 15:04 MST")))
	}
	sb.WriteString(fmt.Sprintf("- **Requirements**: %s\n", reqSummary))
	sb.WriteString("\n🎯 **Get started now by clicking the link above!**")

	return sb.String(), nil
}

// postToDiscord sends a message to the configured Discord channel
func (a *Activities) postToDiscord(ctx context.Context, logger temporal_log.Logger, cfg *Configuration, client *http.Client, message string, env string) error {
	discordAPIURL := fmt.Sprintf("https://discord.com/api/v10/channels/%s/messages", cfg.DiscordConfig.ChannelID)

	var payload map[string]interface{} // Use interface{} to handle different payload structures

	lowerEnv := strings.ToLower(env)

	if lowerEnv == "dev" {
		payload = map[string]interface{}{
			"embeds": []map[string]interface{}{
				{
					"description": message,
					"color":       15158332, // Red color in decimal (#E74C3C)
				},
			},
		}
	} else if lowerEnv == "prod" {
		payload = map[string]interface{}{
			"embeds": []map[string]interface{}{
				{
					"description": message,
					"color":       3066993, // Green color in decimal (#2ECC71)
				},
			},
		}
	} else {
		payload = map[string]interface{}{
			"content": message,
		}
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal discord payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", discordAPIURL, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return fmt.Errorf("failed to create discord message request: %w", err)
	}

	req.Header.Set("Authorization", "Bot "+cfg.DiscordConfig.BotToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "AffiliateBountyBoard-Worker/1.0 (DiscordPublisher)")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("discord message request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 { // Discord uses 200, 201, 204 for success usually
		logger.Error("Discord API request failed", "status_code", resp.StatusCode, "response_body", string(bodyBytes), "url", discordAPIURL)
		return fmt.Errorf("discord API request returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	logger.Debug("Discord API message post successful", "status_code", resp.StatusCode, "response_body", string(bodyBytes))
	return nil
}

// fetchSingleBountyForDiscord fetches a specific bounty by ID from the ABB server (Discord version)
func (a *Activities) fetchSingleBountyForDiscord(ctx context.Context, logger temporal_log.Logger, cfg *Configuration, client *http.Client, token, bountyID string) (*api.BountyListItem, error) {
	bountyURL := fmt.Sprintf("%s/bounties/%s", strings.TrimSuffix(cfg.ABBServerConfig.APIEndpoint, "/"), bountyID)
	req, err := http.NewRequestWithContext(ctx, "GET", bountyURL, nil)
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

// splitMessage splits a long message into chunks that respect a given limit.
func splitMessage(message string, limit int) []string {
	var chunks []string
	if len(message) <= limit {
		return []string{message}
	}

	currentChunk := ""
	lines := strings.Split(message, "\n") // Split by newline to try and keep logical breaks

	for _, line := range lines {
		// If adding the current line (plus a newline char) exceeds the limit,
		// and the current chunk is not empty, store the current chunk.
		if len(currentChunk)+len(line)+1 > limit && currentChunk != "" {
			chunks = append(chunks, strings.TrimSpace(currentChunk))
			currentChunk = ""
		}

		// If a single line itself is too long, it needs to be split.
		// This is a simplified split; a more robust one would look for word boundaries.
		for len(line) > limit {
			if currentChunk != "" { // If there's something in currentChunk, push it first
				chunks = append(chunks, strings.TrimSpace(currentChunk))
				currentChunk = ""
			}
			chunks = append(chunks, line[:limit-1]+"…") // Add ellipsis and push
			line = "…" + line[limit-1:]                 // Prepend ellipsis to remainder
		}

		// Add the line (or its remainder after splitting) to the current chunk.
		if currentChunk == "" {
			currentChunk = line
		} else {
			currentChunk += "\n" + line
		}
	}

	// Add the last chunk if it's not empty
	if currentChunk != "" {
		chunks = append(chunks, strings.TrimSpace(currentChunk))
	}

	return chunks
}
