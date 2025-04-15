package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/brojonat/affiliate-bounty-board/http"
	"github.com/urfave/cli/v2"
	"go.temporal.io/sdk/client"
)

func serverCommands() []*cli.Command {
	return []*cli.Command{
		{
			Name:  "http-server",
			Usage: "Run the HTTP server",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:    "port",
					Aliases: []string{"p"},
					Usage:   "Port to listen on",
					EnvVars: []string{"SERVER_PORT"},
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
			Action: run_server,
		},
	}
}

func run_server(c *cli.Context) error {
	// Create a context that can be cancelled
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Set up signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		cancel()
	}()

	// Set up logger
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// Set up Temporal client
	tc, err := client.Dial(client.Options{
		HostPort:  c.String("temporal-address"),
		Namespace: c.String("temporal-namespace"),
	})
	if err != nil {
		return fmt.Errorf("failed to create temporal client: %w", err)
	}
	defer tc.Close()

	// Parse CORS configuration from environment variables
	normalizeCORSParams := func(e string) []string {
		params := strings.Split(e, ",")
		for i, p := range params {
			params[i] = strings.TrimSpace(p)
		}
		return params
	}

	headers := normalizeCORSParams(os.Getenv("CORS_HEADERS"))
	methods := normalizeCORSParams(os.Getenv("CORS_METHODS"))
	origins := normalizeCORSParams(os.Getenv("CORS_ORIGINS"))

	// Add CORS config to context
	ctx = http.WithCORSConfig(ctx, headers, methods, origins)

	// Run the server
	port := c.String("port")
	if port == "" {
		port = "8080"
	}
	return http.RunServer(ctx, logger, tc, port)
}
