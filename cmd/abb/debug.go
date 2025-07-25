package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/urfave/cli/v2"
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
