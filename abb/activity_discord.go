package abb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/brojonat/affiliate-bounty-board/http/api"
	"go.temporal.io/sdk/activity"
	temporal_log "go.temporal.io/sdk/log"
)

// PublishBountiesDiscord fetches bounties from the ABB server and posts them to Discord.
func (a *Activities) PublishBountiesDiscord(ctx context.Context) error {
	logger := activity.GetLogger(ctx)
	logger.Info("Running PublishBountiesDiscord activity...")

	cfg, err := getConfiguration(ctx)
	if err != nil {
		return fmt.Errorf("failed to get configuration: %w", err)
	}

	if cfg.DiscordConfig.BotToken == "" || cfg.DiscordConfig.ChannelID == "" {
		logger.Warn("Discord Bot Token or Channel ID is not configured. Skipping Discord post.")
		return nil // Not an error, just skipping if not configured
	}

	client := a.httpClient // Use the shared httpClient from Activities

	abbToken, err := a.getABBAuthToken(ctx, logger, cfg, client)
	if err != nil {
		logger.Error("Failed to get ABB auth token for Discord publisher", "error", err)
		return err
	}
	logger.Debug("Obtained ABB auth token for Discord publisher")

	bounties, err := a.fetchBounties(ctx, logger, cfg, client, abbToken)
	if err != nil {
		logger.Error("Failed to fetch bounties for Discord publisher", "error", err)
		return err
	}
	if len(bounties) == 0 {
		logger.Info("No active bounties found to post to Discord.")
		return nil
	}
	logger.Info("Fetched bounties for Discord", "count", len(bounties))

	discordMessage, err := a.formatBountiesForDiscord(bounties, cfg.ABBServerConfig.PublicBaseURL, cfg.Environment)
	if err != nil {
		logger.Error("Failed to format bounties for Discord", "error", err)
		return fmt.Errorf("internal error formatting bounties for Discord: %w", err)
	}
	logger.Debug("Formatted bounties for Discord message")

	// Discord messages have a character limit (typically 2000 or 4000 for embeds).
	// We'll split the message if it's too long.
	const discordMessageLimit = 1900 // Be conservative
	messagesToSend := splitMessage(discordMessage, discordMessageLimit)

	for i, msgChunk := range messagesToSend {
		err = a.postToDiscord(ctx, logger, cfg, client, msgChunk, cfg.Environment)
		if err != nil {
			logger.Error("Failed to post message chunk to Discord", "chunk_index", i, "error", err)
			// Decide if we should continue trying other chunks or return the error
			return fmt.Errorf("failed to post message chunk %d to Discord: %w", i, err)
		}
		logger.Info("Successfully posted message chunk to Discord", "chunk_index", i, "channel_id", cfg.DiscordConfig.ChannelID)
		// Discord has rate limits, a small delay might be prudent if sending many chunks
		if i < len(messagesToSend)-1 {
			activity.RecordHeartbeat(ctx, "Pausing between Discord message chunks")
			time.Sleep(1 * time.Second) // Small delay
		}
	}

	logger.Info("Successfully posted all bounty message chunks to Discord")
	return nil
}

// formatBountiesForDiscord formats the list of bounties into a Discord message (Markdown)
func (a *Activities) formatBountiesForDiscord(bounties []api.BountyListItem, publicBaseURL string, env string) (string, error) {
	var sb strings.Builder
	envPrefix := ""
	if env != "" {
		envPrefix = fmt.Sprintf("[%s] ", strings.ToUpper(env))
	}
	sb.WriteString(fmt.Sprintf("%s**📢 Active Bounties (%s)**\n\n", envPrefix, time.Now().Format("2006-01-02")))

	for _, b := range bounties {
		// Shorten requirements for Discord, as it's less suited for long texts than Reddit.
		reqSummary := strings.Join(b.Requirements, ", ")
		if len(reqSummary) > 100 { // Much shorter summary for Discord
			reqSummary = reqSummary[:97] + "..."
		}

		// Constructing a direct link to the bounty details page
		bountyURL := fmt.Sprintf("%s/bounties/%s", strings.TrimSuffix(publicBaseURL, "/"), b.WorkflowID)

		sb.WriteString(fmt.Sprintf("### [%s](%s)\n", b.WorkflowID, bountyURL)) // Title as link
		sb.WriteString(fmt.Sprintf("- **Platform**: %s (%s)\n", b.PlatformKind, b.ContentKind))
		sb.WriteString(fmt.Sprintf("- **Reward/Post**: $%.2f\n", b.BountyPerPost))
		sb.WriteString(fmt.Sprintf("- **Total Bounty Pool**: $%.2f\n", b.TotalBounty))
		sb.WriteString(fmt.Sprintf("- **Key Requirements**: %s\n", reqSummary))
		sb.WriteString("\n---\n\n") // Separator
	}
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
