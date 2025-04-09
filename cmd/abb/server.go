package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
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

	// Handle shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		cancel()
	}()

	fmt.Printf("temporal-address: %s\n", c.String("temporal-address"))
	fmt.Printf("temporal-namespace: %s\n", c.String("temporal-namespace"))

	// Create Temporal client
	tc, err := client.Dial(client.Options{
		Logger:    getDefaultLogger(slog.LevelInfo),
		HostPort:  c.String("temporal-address"),
		Namespace: c.String("temporal-namespace"),
	})
	if err != nil {
		return fmt.Errorf("couldn't initialize temporal client: %w", err)
	}
	defer tc.Close()

	// Create and run the server
	logger := getDefaultLogger(slog.LevelInfo)
	return http.RunServer(ctx, logger, tc, c.String("port"))
}
