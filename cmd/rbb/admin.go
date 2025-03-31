package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/brojonat/reddit-bounty-board/http/api"
	"github.com/urfave/cli/v2"
)

var (
	EnvServerSecretKey = "SERVER_SECRET_KEY"
	EnvServerEndpoint  = "SERVER_ENDPOINT"
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
	}
}
