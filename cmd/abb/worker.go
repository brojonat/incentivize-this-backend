package main

import (
	"log/slog"
	"os"

	"github.com/brojonat/affiliate-bounty-board/worker"
	"github.com/urfave/cli/v2"
	"go.temporal.io/sdk/client"
)

func workerCommands() []*cli.Command {
	return []*cli.Command{
		{
			Name:  "worker",
			Usage: "Run the worker",
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
					Value:   "default",
				},
				&cli.BoolFlag{
					Name:    "local-mode",
					Aliases: []string{"l"},
					Usage:   "Run in local mode without Solana functionality",
					Value:   false,
				},
				&cli.BoolFlag{
					Name:  "check-connection",
					Usage: "Check Temporal connection and exit (for health checks)",
					Value: false,
				},
			},
			Action: run_worker,
		},
	}
}

func run_worker(c *cli.Context) error {
	logger := getDefaultLogger(slog.LevelInfo)
	temporalAddr := c.String("temporal-address")
	temporalNamespace := c.String("temporal-namespace")

	if c.Bool("check-connection") {
		logger.Info("Performing Temporal connection check...")
		tc, err := client.Dial(client.Options{
			HostPort:  temporalAddr,
			Namespace: temporalNamespace,
		})
		if err != nil {
			logger.Error("Temporal connection check failed (dial)", "error", err)
			os.Exit(1)
		}
		tc.Close()
		logger.Info("Temporal connection check successful")
		os.Exit(0)
	}

	localMode := c.Bool("local-mode")

	if localMode {
		logger.Info("Starting worker in local mode")
		return worker.RunWorkerLocal(
			c.Context,
			logger,
			temporalAddr,
			temporalNamespace,
		)
	}

	logger.Info("Starting worker")
	return worker.RunWorker(
		c.Context,
		logger,
		temporalAddr,
		temporalNamespace,
	)
}
