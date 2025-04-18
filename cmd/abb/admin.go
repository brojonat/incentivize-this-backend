package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	abbhttp "github.com/brojonat/affiliate-bounty-board/http"
	"github.com/brojonat/affiliate-bounty-board/http/api"
	"github.com/urfave/cli/v2"
)

var (
	EnvServerSecretKey = "SERVER_SECRET_KEY"
	EnvServerEndpoint  = "SERVER_ENDPOINT"
	EnvAuthToken       = "AUTH_TOKEN"
)

func getAuthToken(ctx *cli.Context) error {
	r, err := http.NewRequest(
		http.MethodPost,
		ctx.String("endpoint")+"/token",
		nil,
	)
	if err != nil {
		return err
	}
	r.SetBasicAuth(ctx.String("email"), ctx.String("secret-key"))
	res, err := http.DefaultClient.Do(r)
	if err != nil {
		return fmt.Errorf("could not do server request: %w", err)
	}
	defer res.Body.Close()

	// Read the body once
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return fmt.Errorf("could not read response body: %w", err)
	}

	var resp api.DefaultJSONResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Errorf("could not decode response: %w", err)
	}

	if resp.Error != "" {
		return fmt.Errorf("server error: %s", resp.Error)
	}

	// Handle env file update if specified
	envFile := ctx.String("env-file")
	if envFile != "" {
		content, err := os.ReadFile(envFile)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to read .env file: %w", err)
		}

		lines := strings.Split(string(content), "\n")
		found := false
		for i, line := range lines {
			if strings.HasPrefix(line, "AUTH_TOKEN=") {
				lines[i] = fmt.Sprintf("AUTH_TOKEN=%s", resp.Message)
				found = true
				break
			}
		}
		if !found {
			lines = append(lines, fmt.Sprintf("AUTH_TOKEN=%s", resp.Message))
		}

		if err := os.WriteFile(envFile, []byte(strings.Join(lines, "\n")), 0644); err != nil {
			return fmt.Errorf("failed to write .env file: %w", err)
		}
		fmt.Printf("Bearer token written to %s\n", envFile)
	}

	// Replace the body and call printServerResponse
	res.Body = io.NopCloser(bytes.NewReader(body))
	return printServerResponse(res)
}

func createBounty(ctx *cli.Context) error {
	// Create a map for the request to avoid type conversion issues
	req := map[string]interface{}{
		"requirements":    ctx.StringSlice("requirements"),
		"bounty_per_post": ctx.Float64("per-post"),
		"total_bounty":    ctx.Float64("total"),
		"owner_id":        ctx.String("owner-id"),
		"solana_wallet":   ctx.String("solana-wallet"),
		"usdc_account":    ctx.String("usdc-account"),
		"platform_type":   ctx.String("platform"),
	}

	// Marshal to JSON
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create and send the HTTP request
	httpReq, err := http.NewRequest(
		http.MethodPost,
		ctx.String("endpoint")+"/bounties",
		bytes.NewBuffer(body),
	)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", ctx.String("token")))

	// Execute the request
	res, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}

	return printServerResponse(res)
}

func payBounty(ctx *cli.Context) error {
	// Create the request
	req := abbhttp.PayBountyRequest{
		UserID:      ctx.String("user-id"),
		Amount:      ctx.Float64("amount"),
		ToAccount:   ctx.String("to-account"),
		FromAccount: ctx.String("from-account"),
	}

	// Marshal to JSON
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create and send the HTTP request
	httpReq, err := http.NewRequest(
		http.MethodPost,
		ctx.String("endpoint")+"/bounties/pay",
		bytes.NewBuffer(body),
	)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+ctx.String("token"))

	// Execute the request
	res, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}

	return printServerResponse(res)
}

func assessContent(ctx *cli.Context) error {
	// Create a map for the request to avoid type conversion issues
	req := map[string]interface{}{
		"bounty_id":  ctx.String("bounty-id"),
		"content_id": ctx.String("content-id"),
		"user_id":    ctx.String("user-id"),
		"platform":   ctx.String("platform"),
	}

	// Marshal to JSON
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create and send the HTTP request
	httpReq, err := http.NewRequest(
		http.MethodPost,
		ctx.String("endpoint")+"/assess",
		bytes.NewBuffer(body),
	)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+ctx.String("token"))

	// Execute the request
	res, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}

	return printServerResponse(res)
}

func listBounties(ctx *cli.Context) error {
	// Create and send the HTTP request
	httpReq, err := http.NewRequest(
		http.MethodGet,
		ctx.String("endpoint")+"/bounties",
		nil,
	)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	httpReq.Header.Set("Authorization", "Bearer "+ctx.String("token"))

	// Execute the request
	res, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}

	return printServerResponse(res)
}

func adminCommands() []*cli.Command {
	return []*cli.Command{
		{
			Name:  "auth",
			Usage: "Authentication related commands",
			Subcommands: []*cli.Command{
				{
					Name:        "get-token",
					Usage:       "Gets a new auth token with the user's email",
					Description: "This is a sudo operation and requires the server secret key for basic auth.",
					Flags: []cli.Flag{
						&cli.StringFlag{
							Name:    "endpoint",
							Aliases: []string{"end", "e"},
							Value:   "http://localhost:8080",
							Usage:   "Server endpoint",
							EnvVars: []string{EnvServerEndpoint},
						},
						&cli.StringFlag{
							Name:    "secret-key",
							Aliases: []string{"sk", "s"},
							Usage:   "Server secret key",
							EnvVars: []string{EnvServerSecretKey},
						},
						&cli.StringFlag{
							Name:     "email",
							Required: true,
							Usage:    "User's email",
						},
						&cli.StringFlag{
							Name:  "env-file",
							Usage: "Path to .env file to update with the new token",
							Value: ".env",
						},
					},
					Action: getAuthToken,
				},
			},
		},
		{
			Name:  "bounty",
			Usage: "Bounty management commands",
			Subcommands: []*cli.Command{
				{
					Name:        "create",
					Usage:       "Create a new bounty",
					Description: "Creates a new bounty with specified parameters",
					Flags: []cli.Flag{
						&cli.StringFlag{
							Name:    "endpoint",
							Aliases: []string{"end", "e"},
							Value:   "http://localhost:8080",
							Usage:   "Server endpoint",
							EnvVars: []string{EnvServerEndpoint},
						},
						&cli.StringFlag{
							Name:     "token",
							Required: true,
							Usage:    "Authorization token",
							EnvVars:  []string{EnvAuthToken},
						},
						&cli.StringSliceFlag{
							Name:     "requirements",
							Aliases:  []string{"req", "r"},
							Required: true,
							Usage:    "Description of the bounty requirements",
						},
						&cli.Float64Flag{
							Name:     "per-post",
							Required: true,
							Usage:    "Amount to pay per post (in USDC)",
						},
						&cli.Float64Flag{
							Name:     "total",
							Required: true,
							Usage:    "Total bounty amount (in USDC)",
						},
						&cli.StringFlag{
							Name:     "owner-id",
							Required: true,
							Usage:    "ID of the bounty owner",
						},
						&cli.StringFlag{
							Name:     "solana-wallet",
							Required: true,
							Usage:    "Solana wallet address of the bounty owner",
						},
						&cli.StringFlag{
							Name:     "usdc-account",
							Required: true,
							Usage:    "USDC account address of the bounty owner",
						},
						&cli.StringFlag{
							Name:     "platform",
							Required: true,
							Usage:    "Platform type (reddit, youtube, yelp, google)",
							Value:    "reddit",
						},
					},
					Action: createBounty,
				},
				{
					Name:        "pay",
					Usage:       "Pay a bounty",
					Description: "Pays a bounty to a user",
					Flags: []cli.Flag{
						&cli.StringFlag{
							Name:    "endpoint",
							Aliases: []string{"end", "e"},
							Value:   "http://localhost:8080",
							Usage:   "Server endpoint",
							EnvVars: []string{EnvServerEndpoint},
						},
						&cli.StringFlag{
							Name:     "token",
							Required: true,
							Usage:    "Authorization token",
							EnvVars:  []string{EnvAuthToken},
						},
						&cli.StringFlag{
							Name:  "user-id",
							Usage: "ID of the user receiving the bounty (optional)",
						},
						&cli.Float64Flag{
							Name:     "amount",
							Required: true,
							Usage:    "Amount to pay (in USDC)",
						},
						&cli.StringFlag{
							Name:     "to-account",
							Required: true,
							Usage:    "Destination wallet address",
						},
						&cli.StringFlag{
							Name:  "from-account",
							Usage: "Source account (defaults to escrow if empty)",
						},
					},
					Action: payBounty,
				},
				{
					Name:        "assess",
					Usage:       "Signal to assess content against bounty requirements",
					Description: "Sends a signal to assess content against bounty requirements",
					Flags: []cli.Flag{
						&cli.StringFlag{
							Name:    "endpoint",
							Aliases: []string{"end", "e"},
							Value:   "http://localhost:8080",
							Usage:   "Server endpoint",
							EnvVars: []string{EnvServerEndpoint},
						},
						&cli.StringFlag{
							Name:     "token",
							Required: true,
							Usage:    "Authorization token",
							EnvVars:  []string{EnvAuthToken},
						},
						&cli.StringFlag{
							Name:     "bounty-id",
							Required: true,
							Usage:    "ID of the bounty",
						},
						&cli.StringFlag{
							Name:     "content-id",
							Required: true,
							Usage:    "ID of the content to assess",
						},
						&cli.StringFlag{
							Name:     "user-id",
							Required: true,
							Usage:    "ID of the user who created the content",
						},
						&cli.StringFlag{
							Name:     "platform",
							Required: true,
							Usage:    "Platform type (reddit, youtube, yelp, google)",
							Value:    "reddit",
						},
					},
					Action: assessContent,
				},
				{
					Name:        "list",
					Usage:       "List available bounties",
					Description: "Lists all available bounties",
					Flags: []cli.Flag{
						&cli.StringFlag{
							Name:    "endpoint",
							Aliases: []string{"end", "e"},
							Value:   "http://localhost:8080",
							Usage:   "Server endpoint",
							EnvVars: []string{EnvServerEndpoint},
						},
						&cli.StringFlag{
							Name:     "token",
							Required: true,
							Usage:    "Authorization token",
							EnvVars:  []string{EnvAuthToken},
						},
					},
					Action: listBounties,
				},
			},
		},
	}
}
