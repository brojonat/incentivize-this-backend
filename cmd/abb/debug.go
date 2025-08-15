package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/brojonat/affiliate-bounty-board/abb"
	"github.com/google/uuid"
	"github.com/urfave/cli/v2"
	"go.temporal.io/sdk/client"
	sdklog "go.temporal.io/sdk/log"
	"go.temporal.io/sdk/worker"
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
			Name:  "run-activity",
			Usage: "Run a single activity for debugging",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:     "activity-name",
					Aliases:  []string{"a"},
					Required: true,
					Usage:    "Name of the activity to run",
				},
				&cli.StringFlag{
					Name:     "data",
					Aliases:  []string{"d"},
					Required: true,
					Usage:    "JSON data to pass to the activity",
				},
				&cli.BoolFlag{
					Name:  "wait-result",
					Value: true,
					Usage: "Wait for the workflow to complete and print the result",
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
					Value:   "affiliate-bounty-board",
				},
				&cli.StringFlag{
					Name:    "task-queue",
					Aliases: []string{"tq"},
					Usage:   "Temporal task queue name",
					Value:   "affiliate_bounty_board_debug",
				},
			},
			Action: runActivity,
		},
		{
			Name:  "run-debug-worker",
			Usage: "Run a worker that only registers the debug workflow",
			Flags: []cli.Flag{
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
					Value:   "affiliate-bounty-board",
				},
				&cli.StringFlag{
					Name:    "task-queue",
					Aliases: []string{"tq"},
					Usage:   "Temporal task queue name",
					Value:   "affiliate_bounty_board_debug",
				},
			},
			Action: runDebugWorker,
		},
		{
			Name:  "run-integration-tests",
			Usage: "Run a suite of integration tests for all tool calls",
			Flags: []cli.Flag{
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
					Value:   "affiliate-bounty-board",
				},
				&cli.StringFlag{
					Name:    "task-queue",
					Aliases: []string{"tq"},
					Usage:   "Temporal task queue name",
					Value:   "affiliate_bounty_board_debug",
				},
				&cli.StringSliceFlag{
					Name:    "tool",
					Aliases: []string{"t", "test"},
					Usage:   "Only run the specified tool(s). Repeat flag to include multiple. Example: --tool detect_malicious_content --tool pull_content",
				},
			},
			Action: runIntegrationTests,
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

func runActivity(c *cli.Context) error {
	activityName := c.String("activity-name")
	data := c.String("data")
	waitResult := c.Bool("wait-result")
	temporalAddr := c.String("temporal-address")
	temporalNamespace := c.String("temporal-namespace")
	taskQueue := c.String("task-queue")

	// --- Create a new slog.Logger for Temporal, configured to LevelWarn ---
	temporalSlogHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelWarn,
	})
	temporalLogger := sdklog.NewStructuredLogger(slog.New(temporalSlogHandler))
	// --- End Temporal logger setup ---

	// Create Temporal client
	cl, err := client.Dial(client.Options{
		Logger:    temporalLogger,
		HostPort:  temporalAddr,
		Namespace: temporalNamespace,
	})
	if err != nil {
		return fmt.Errorf("unable to create Temporal client: %w", err)
	}
	defer cl.Close()

	// Prepare workflow options
	workflowID := "debug-activity-" + uuid.New().String()
	workflowOptions := client.StartWorkflowOptions{
		ID:        workflowID,
		TaskQueue: taskQueue,
	}

	// Start workflow
	we, err := cl.ExecuteWorkflow(
		context.Background(),
		workflowOptions,
		abb.DebugWorkflow,
		activityName,
		json.RawMessage(data),
	)
	if err != nil {
		return fmt.Errorf("unable to execute workflow: %w", err)
	}

	fmt.Printf("Started workflow; WorkflowID: %s, RunID: %s\n", we.GetID(), we.GetRunID())

	// Wait for result if requested
	if waitResult {
		var result interface{}
		err := we.Get(context.Background(), &result)
		if err != nil {
			return fmt.Errorf("workflow failed: %w", err)
		}

		// Pretty print the result
		resultJSON, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal result to JSON: %w", err)
		}
		fmt.Println("Workflow result:")
		fmt.Println(string(resultJSON))
	}

	return nil
}

func runDebugWorker(c *cli.Context) error {
	temporalAddr := c.String("temporal-address")
	temporalNamespace := c.String("temporal-namespace")
	taskQueue := c.String("task-queue")

	// --- Create a new slog.Logger for Temporal, configured to LevelWarn ---
	temporalSlogHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelWarn,
	})
	temporalLogger := sdklog.NewStructuredLogger(slog.New(temporalSlogHandler))
	l := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: new(slog.LevelVar)}))
	// --- End Temporal logger setup ---

	// connect to temporal
	cl, err := client.Dial(client.Options{
		Logger:    temporalLogger,
		HostPort:  temporalAddr,
		Namespace: temporalNamespace,
	})
	if err != nil {
		return fmt.Errorf("unable to create Temporal client: %w", err)
	}
	defer cl.Close()

	// Create activities struct
	activities, err := abb.NewActivities()
	if err != nil {
		return fmt.Errorf("failed to create activities: %w", err)
	}

	// Create the worker
	w := worker.New(cl, taskQueue, worker.Options{})

	// Register the debug workflow and all activities
	w.RegisterWorkflow(abb.DebugWorkflow)
	w.RegisterActivity(activities)

	// Run the worker
	l.Info("Starting debug worker", "TaskQueue", taskQueue)
	err = w.Run(worker.InterruptCh())
	l.Info("Debug worker stopped")
	return err
}

type toolTestResult struct {
	Name    string
	Success bool
	Input   string
	Output  string
	Error   error
}

func runIntegrationTests(c *cli.Context) error {
	temporalAddr := c.String("temporal-address")
	temporalNamespace := c.String("temporal-namespace")
	taskQueue := c.String("task-queue")

	temporalSlogHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelWarn,
	})
	temporalLogger := sdklog.NewStructuredLogger(slog.New(temporalSlogHandler))

	cl, err := client.Dial(client.Options{
		Logger:    temporalLogger,
		HostPort:  temporalAddr,
		Namespace: temporalNamespace,
	})
	if err != nil {
		return fmt.Errorf("unable to create Temporal client: %w", err)
	}
	defer cl.Close()

	type toolTest struct {
		Name  string
		Input string
	}

	tests := []toolTest{
		// PullContent coverage across platforms/kinds
		{abb.ToolNamePullContent, `{"platform": "github", "content_kind": "issue", "content_id": "astral-sh/uv/6637"}`},
		{abb.ToolNamePullContent, `{"platform": "github", "content_kind": "user", "content_id": "brojonat"}`},
		{abb.ToolNamePullContent, `{"platform": "youtube", "content_kind": "video", "content_id": "dQw4w9WgXcQ"}`},
		{abb.ToolNamePullContent, `{"platform": "youtube", "content_kind": "channel", "content_id": "UCfQgsKhHjSyRLOp9mnffqVg"}`},
		{abb.ToolNamePullContent, `{"platform": "reddit", "content_kind": "post", "content_id": "t3_j6ed00"}`},
		{abb.ToolNamePullContent, `{"platform": "reddit", "content_kind": "comment", "content_id": "t1_mhyb41q"}`},
		{abb.ToolNamePullContent, `{"platform": "reddit", "content_kind": "subreddit", "content_id": "golang"}`},
		{abb.ToolNamePullContent, `{"platform": "hackernews", "content_kind": "post", "content_id": "44434011"}`},
		{abb.ToolNamePullContent, `{"platform": "hackernews", "content_kind": "comment", "content_id": "44434103"}`},
		{abb.ToolNamePullContent, `{"platform": "twitch", "content_kind": "video", "content_id": "2251568150"}`},
		{abb.ToolNamePullContent, `{"platform": "twitch", "content_kind": "clip", "content_id": "CheerfulCheerfulSaladStinkyCheese-5X0KAkC-NfJJia1y"}`},
		// Bluesky post can be provided as bsky URL or at:// URI; sample may fail depending on availability
		{abb.ToolNamePullContent, `{"platform": "bluesky", "content_kind": "post", "content_id": "https://bsky.app/profile/incentivizethis.bsky.social/post/3ltvkbhevvc2c"}`},
		{abb.ToolNamePullContent, `{"platform": "steam", "content_kind": "dota2chat", "content_id": "8416080116"}`},
		{abb.ToolNamePullContent, `{"platform": "steam", "content_kind": "user", "content_id": "75412262"}`},
		// Replaced dedicated tools with pull_content coverage; keeping GetClosingPR for now
		{abb.ToolNameGetClosingPR, `{"owner": "astral-sh", "repo": "uv", "issue_number": 6637}`},
		{abb.ToolNameAnalyzeImageURL, `{"image_url": "https://upload.wikimedia.org/wikipedia/en/7/7d/Lenna_%28test_image%29.png", "prompt": "Does it contain a lady?"}`},
		{abb.ToolNameValidatePayoutWallet, `{"payout_wallet": "stub_wallet_address", "validation_prompt": "Is this a valid Solana address?"}`},
		{abb.ToolNameDetectMaliciousContent, `{"content": "This is benign content."}`},
		{abb.ToolNameSubmitDecision, `{"is_approved": true, "reason": "Content meets all requirements."}`},
		{abb.ToolNameSubmitDecision, `{"is_approved": false, "reason": "Content does not meet requirements."}`},
	}

	// Filter tests if specific tools were requested
	selectedTools := c.StringSlice("tool")
	if len(selectedTools) > 0 {
		allowed := make(map[string]struct{}, len(selectedTools))
		for _, name := range selectedTools {
			allowed[name] = struct{}{}
		}
		filtered := make([]toolTest, 0, len(tests))
		for _, t := range tests {
			if _, ok := allowed[t.Name]; ok {
				filtered = append(filtered, t)
			}
		}
		if len(filtered) == 0 {
			fmt.Println("No matching tools found for provided --tool flags; nothing to run.")
			return nil
		}
		tests = filtered
	}

	fmt.Println("Starting integration test suite...")

	var wg sync.WaitGroup
	resultsChan := make(chan toolTestResult)

	for _, test := range tests {
		wg.Add(1)
		go func(test toolTest) {
			defer wg.Done()

			workflowID := fmt.Sprintf("integration-test-%s-%s", test.Name, uuid.New().String())
			workflowOptions := client.StartWorkflowOptions{
				ID:        workflowID,
				TaskQueue: taskQueue,
			}

			testRes := toolTestResult{
				Name:    test.Name,
				Input:   test.Input,
				Success: false,
			}

			we, err := cl.ExecuteWorkflow(
				context.Background(),
				workflowOptions,
				abb.DebugWorkflow,
				test.Name,
				json.RawMessage(test.Input),
			)
			if err != nil {
				testRes.Success = false
				testRes.Error = fmt.Errorf("error starting workflow: %w", err)
				resultsChan <- testRes
				return
			}

			var result interface{}
			err = we.Get(context.Background(), &result)
			if err != nil {
				testRes.Success = false
				testRes.Error = fmt.Errorf("workflow failed: %w", err)
				resultsChan <- testRes
				return
			}

			resultJSON, err := json.MarshalIndent(result, "", "  ")
			if err != nil {
				testRes.Success = false
				testRes.Error = fmt.Errorf("failed to marshal result to JSON: %w", err)
				resultsChan <- testRes
				return
			}

			testRes.Success = true
			testRes.Output = string(resultJSON)
			resultsChan <- testRes

		}(test)
	}

	// Wait for all goroutines to finish and then close the channel
	go func() {
		wg.Wait()
		close(resultsChan)
	}()

	// Collect and print results
	successCount := 0
	for res := range resultsChan {
		// fmt.Printf("\n--- Test for: %s ---\n", res.Name)
		// if res.Success {
		// 	// we'll remove this after we've verified the tests are working
		// 	fmt.Printf("Status: SUCCESS\nResult:\n%s\n", res.Output)
		// 	continue
		// }
		if !res.Success {
			fmt.Printf("\n--- Test for: %s ---\nInput: %s\nStatus: FAILED\nError: %v\n", res.Name, res.Input, res.Error)
		} else {
			successCount++
		}
	}

	fmt.Printf("\nIntegration test suite completed: %d/%d passed.\n", successCount, len(tests))
	return nil
}
