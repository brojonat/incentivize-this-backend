package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/brojonat/affiliate-bounty-board/abb"
	"github.com/google/uuid"
	"github.com/urfave/cli/v2"
	"go.temporal.io/sdk/client"
)

func debugCommands() []*cli.Command {
	return []*cli.Command{
		{
			Name:  "rate-limit",
			Usage: "Test rate limiting",
			Flags: []cli.Flag{
				&cli.IntFlag{
					Name:    "count",
					Aliases: []string{"c"},
					Value:   10,
					Usage:   "Number of requests to make",
				},
				&cli.StringFlag{
					Name:    "endpoint",
					Aliases: []string{"e"},
					Value:   "http://localhost:8080",
					Usage:   "Server endpoint",
					EnvVars: []string{"SERVER_ENDPOINT"},
				},
			},
			Action: testRateLimit,
		},
		{
			Name:  "token-info",
			Usage: "Decode and display JWT token information",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:     "token",
					Required: true,
					Usage:    "JWT token to decode",
				},
			},
			Action: decodeToken,
		},
		{
			Name:  "health",
			Usage: "Check server health and connectivity",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:    "endpoint",
					Aliases: []string{"e"},
					Value:   "http://localhost:8080",
					Usage:   "Server endpoint",
					EnvVars: []string{"SERVER_ENDPOINT"},
				},
				&cli.StringFlag{
					Name:    "token",
					Usage:   "Bearer token for authenticated endpoints",
					EnvVars: []string{"AUTH_TOKEN"},
				},
			},
			Action: checkHealth,
		},
		{
			Name:  "pull-content",
			Usage: "Test PullContent workflow for various platforms",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:     "platform",
					Required: true,
					Usage:    "Platform type (reddit, youtube, yelp, google)",
					Value:    "reddit",
				},
				&cli.StringFlag{
					Name:     "content-id",
					Required: true,
					Usage:    "Content ID for the specified platform",
				},
				&cli.StringFlag{
					Name:    "temporal-address",
					Aliases: []string{"ta"},
					Usage:   "Temporal server address",
					EnvVars: []string{"TEMPORAL_ADDRESS"},
					Value:   "localhost:7233",
				},
				&cli.StringFlag{
					Name:    "temporal-namespace",
					Aliases: []string{"tn"},
					Usage:   "Temporal namespace",
					EnvVars: []string{"TEMPORAL_NAMESPACE"},
					Value:   "default",
				},
				// Reddit-specific flags
				&cli.StringFlag{
					Name:    "reddit-user-agent",
					Aliases: []string{"ua"},
					Usage:   "User agent string for Reddit API",
					Value:   "RedditBountyBoard/1.0",
					EnvVars: []string{"REDDIT_USER_AGENT"},
				},
				&cli.StringFlag{
					Name:    "reddit-username",
					Usage:   "Reddit username for authentication",
					EnvVars: []string{"REDDIT_USERNAME"},
				},
				&cli.StringFlag{
					Name:    "reddit-password",
					Usage:   "Reddit password for authentication",
					EnvVars: []string{"REDDIT_PASSWORD"},
				},
				&cli.StringFlag{
					Name:    "reddit-client-id",
					Usage:   "Reddit client ID for authentication",
					EnvVars: []string{"REDDIT_CLIENT_ID"},
				},
				&cli.StringFlag{
					Name:    "reddit-client-secret",
					Usage:   "Reddit client secret for authentication",
					EnvVars: []string{"REDDIT_CLIENT_SECRET"},
				},
				// YouTube-specific flags
				&cli.StringFlag{
					Name:    "youtube-api-key",
					Usage:   "YouTube API key",
					EnvVars: []string{"YOUTUBE_API_KEY"},
				},
				&cli.StringFlag{
					Name:    "youtube-app-name",
					Usage:   "YouTube application name",
					EnvVars: []string{"YOUTUBE_APP_NAME"},
				},
				// Yelp-specific flags
				&cli.StringFlag{
					Name:    "yelp-api-key",
					Usage:   "Yelp API key",
					EnvVars: []string{"YELP_API_KEY"},
				},
				&cli.StringFlag{
					Name:    "yelp-client-id",
					Usage:   "Yelp client ID",
					EnvVars: []string{"YELP_CLIENT_ID"},
				},
				// Google-specific flags
				&cli.StringFlag{
					Name:    "google-api-key",
					Usage:   "Google API key",
					EnvVars: []string{"GOOGLE_API_KEY"},
				},
				&cli.StringFlag{
					Name:    "google-search-engine-id",
					Usage:   "Google custom search engine ID",
					EnvVars: []string{"GOOGLE_SEARCH_ENGINE_ID"},
				},
			},
			Action: testPullContent,
		},
		{
			Name:  "check-requirements",
			Usage: "Test content requirements checking",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:     "content",
					Usage:    "Content to check",
					Required: true,
				},
				&cli.StringSliceFlag{
					Name:    "requirement",
					Aliases: []string{"r"},
					Usage:   "Requirement to check (can be specified multiple times)",
				},
				&cli.StringFlag{
					Name:    "openai-api-key",
					Usage:   "OpenAI API key",
					EnvVars: []string{"OPENAI_API_KEY"},
				},
				&cli.StringFlag{
					Name:    "openai-model",
					Usage:   "OpenAI model to use",
					Value:   "gpt-4o",
					EnvVars: []string{"OPENAI_MODEL"},
				},
				&cli.IntFlag{
					Name:  "max-tokens",
					Usage: "Maximum tokens to generate",
					Value: 1000,
				},
				&cli.Float64Flag{
					Name:  "temperature",
					Usage: "Temperature for text generation",
					Value: 0.7,
				},
				&cli.StringFlag{
					Name:    "temporal-address",
					Aliases: []string{"ta"},
					Usage:   "Temporal server address",
					EnvVars: []string{"TEMPORAL_ADDRESS"},
					Value:   "localhost:7233",
				},
				&cli.StringFlag{
					Name:    "temporal-namespace",
					Aliases: []string{"tn"},
					Usage:   "Temporal namespace",
					EnvVars: []string{"TEMPORAL_NAMESPACE"},
					Value:   "default",
				},
			},
			Action: testCheckContentRequirements,
		},
	}
}

func testRateLimit(c *cli.Context) error {
	endpoint := c.String("endpoint")
	count := c.Int("count")

	fmt.Printf("Testing rate limiting with %d requests to %s\n", count, endpoint)

	client := &http.Client{}
	for i := 0; i < count; i++ {
		resp, err := client.Get(endpoint + "/ping")
		if err != nil {
			fmt.Printf("Request %d failed: %v\n", i+1, err)
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		fmt.Printf("Request %d: Status=%d, Body=%s\n", i+1, resp.StatusCode, string(body))
		if resp.StatusCode == http.StatusTooManyRequests {
			retryAfter := resp.Header.Get("Retry-After")
			fmt.Printf("Rate limit hit! Retry after %s seconds\n", retryAfter)
			break
		}

		time.Sleep(100 * time.Millisecond) // Small delay between requests
	}

	return nil
}

func decodeToken(c *cli.Context) error {
	token := c.String("token")
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return fmt.Errorf("invalid token format")
	}

	// Decode header
	header, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return fmt.Errorf("failed to decode header: %w", err)
	}

	// Decode payload
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return fmt.Errorf("failed to decode payload: %w", err)
	}

	// Pretty print the JSON
	var prettyHeader, prettyPayload interface{}
	json.Unmarshal(header, &prettyHeader)
	json.Unmarshal(payload, &prettyPayload)

	fmt.Println("Token Information:")
	fmt.Println("-----------------")
	fmt.Println("Header:")
	json.NewEncoder(os.Stdout).SetIndent("", "  ")
	json.NewEncoder(os.Stdout).Encode(prettyHeader)
	fmt.Println("\nPayload:")
	json.NewEncoder(os.Stdout).Encode(prettyPayload)

	return nil
}

func checkHealth(c *cli.Context) error {
	endpoint := c.String("endpoint")
	token := c.String("token")

	fmt.Printf("Checking health of %s\n", endpoint)

	// Check basic connectivity
	resp, err := http.Get(endpoint + "/ping")
	if err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}
	fmt.Printf("Basic connectivity: %s\n", resp.Status)
	resp.Body.Close()

	// Check authenticated endpoint if token is provided
	if token != "" {
		req, err := http.NewRequest("GET", endpoint+"/ping", nil)
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return fmt.Errorf("failed to make authenticated request: %w", err)
		}
		fmt.Printf("Authenticated endpoint: %s\n", resp.Status)
		resp.Body.Close()
	}

	return nil
}

func testPullContent(c *cli.Context) error {
	// Get content ID and platform from flags
	contentID := c.String("content-id")
	if contentID == "" {
		return fmt.Errorf("--content-id flag is required")
	}

	platformStr := c.String("platform")
	if platformStr == "" {
		return fmt.Errorf("--platform flag is required")
	}

	var platformType abb.PlatformType
	switch strings.ToLower(platformStr) {
	case "reddit":
		platformType = abb.PlatformReddit
	case "youtube":
		platformType = abb.PlatformYouTube
	case "yelp":
		platformType = abb.PlatformYelp
	case "google":
		platformType = abb.PlatformGoogle
	default:
		return fmt.Errorf("unsupported platform: %s. Supported platforms: reddit, youtube, yelp, google", platformStr)
	}

	// Create Temporal client with debug logging
	tc, err := client.Dial(client.Options{
		Logger:    getDefaultLogger(slog.LevelDebug),
		HostPort:  c.String("temporal-address"),
		Namespace: c.String("temporal-namespace"),
	})
	if err != nil {
		return fmt.Errorf("couldn't initialize temporal client: %w", err)
	}
	defer tc.Close()

	// Create platform-specific dependencies
	var input abb.PullContentWorkflowInput
	input.PlatformType = platformType
	input.ContentID = contentID

	switch platformType {
	case abb.PlatformReddit:
		input.Dependencies = abb.RedditDependencies{
			UserAgent:    c.String("reddit-user-agent"),
			Username:     c.String("reddit-username"),
			Password:     c.String("reddit-password"),
			ClientID:     c.String("reddit-client-id"),
			ClientSecret: c.String("reddit-client-secret"),
		}
	case abb.PlatformYouTube:
		input.Dependencies = abb.YouTubeDependencies{
			APIKey:          c.String("youtube-api-key"),
			ApplicationName: c.String("youtube-app-name"),
			MaxResults:      100, // Default value
		}
	case abb.PlatformYelp:
		input.Dependencies = abb.YelpDependencies{
			APIKey:   c.String("yelp-api-key"),
			ClientID: c.String("yelp-client-id"),
		}
	case abb.PlatformGoogle:
		input.Dependencies = abb.GoogleDependencies{
			APIKey:         c.String("google-api-key"),
			SearchEngineID: c.String("google-search-engine-id"),
		}
	default:
		return fmt.Errorf("unsupported platform type: %s", platformType)
	}

	// Skip validating Solana configuration for content testing
	workflowID := fmt.Sprintf("test-pull-content-%s-%s", platformStr, contentID)
	workflowOptions := client.StartWorkflowOptions{
		ID:        workflowID,
		TaskQueue: abb.TaskQueueName,
		// Add workflow task timeout to avoid waiting indefinitely
		WorkflowTaskTimeout: 30 * time.Second,
	}

	// Execute the workflow using the registered workflow function
	run, err := tc.ExecuteWorkflow(c.Context, workflowOptions, abb.PullContentWorkflow, input)
	if err != nil {
		return fmt.Errorf("failed to start workflow: %w", err)
	}

	// Wait for workflow completion
	var content string
	if err := run.Get(c.Context, &content); err != nil {
		return fmt.Errorf("workflow execution failed: %w", err)
	}

	// Output clean JSON that can be piped to other commands
	output := struct {
		Platform  abb.PlatformType `json:"platform"`
		ContentID string           `json:"content_id"`
		Content   string           `json:"content"`
	}{
		Platform:  platformType,
		ContentID: contentID,
		Content:   content,
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(output)
}

func testCheckContentRequirements(c *cli.Context) error {
	// Get content from flag or stdin
	content := c.String("content")
	if content == "" {
		return fmt.Errorf("--content flag is required")
	}

	// If content is "-", read from stdin
	if content == "-" {
		contentBytes, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("failed to read content from stdin: %w", err)
		}
		content = string(contentBytes)
	}

	// Get requirements from flag
	requirements := c.StringSlice("requirement")
	if len(requirements) == 0 {
		return fmt.Errorf("at least one --requirement flag is required")
	}

	// Join requirements with newlines
	requirementsStr := strings.Join(requirements, "\n")

	// Create Temporal client with debug logging
	tc, err := client.Dial(client.Options{
		Logger:    getDefaultLogger(slog.LevelDebug),
		HostPort:  c.String("temporal-address"),
		Namespace: c.String("temporal-namespace"),
	})
	if err != nil {
		return fmt.Errorf("couldn't initialize temporal client: %w", err)
	}
	defer tc.Close()

	// Execute workflow
	workflowID := fmt.Sprintf("check-requirements-%s", uuid.New().String())
	workflowOptions := client.StartWorkflowOptions{
		ID:        workflowID,
		TaskQueue: abb.TaskQueueName,
	}

	// Execute the workflow using the registered workflow function
	we, err := tc.ExecuteWorkflow(c.Context, workflowOptions, abb.CheckContentRequirementsWorkflow, content, requirementsStr)
	if err != nil {
		return fmt.Errorf("failed to start workflow: %w", err)
	}

	// Wait for workflow completion
	var result abb.CheckContentRequirementsResult
	err = we.Get(c.Context, &result)
	if err != nil {
		return fmt.Errorf("workflow failed: %w", err)
	}

	// Output result as JSON
	output := struct {
		Satisfies bool   `json:"satisfies"`
		Reason    string `json:"reason"`
	}{
		Satisfies: result.Satisfies,
		Reason:    result.Reason,
	}
	json.NewEncoder(os.Stdout).Encode(output)
	return nil
}
