package main

import (
	"log/slog"

	"github.com/brojonat/reddit-bounty-board/worker"
	"github.com/urfave/cli/v2"
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
			},
			Action: run_worker,
		},
	}
}

func run_worker(c *cli.Context) error {
	return worker.RunWorker(
		c.Context,
		getDefaultLogger(slog.LevelInfo),
		c.String("temporal-address"),
		c.String("temporal-namespace"),
	)
}
