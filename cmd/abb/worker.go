package main

import (
	"log"
	"log/slog"
	"os"

	"github.com/brojonat/affiliate-bounty-board/worker"
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
				&cli.BoolFlag{
					Name:  "check-connection",
					Usage: "Check Temporal connection and exit (for health checks)",
					Value: false,
				},
				&cli.StringFlag{
					Name:    "task-queue",
					Aliases: []string{"tq"},
					Usage:   "Temporal task queue name",
					EnvVars: []string{"TASK_QUEUE"},
					Value:   "affiliate_bounty_board",
				},
			},
			Action: runWorker,
		},
	}
}

func runWorker(c *cli.Context) error {
	temporalAddr := c.String("temporal-address")
	temporalNamespace := c.String("temporal-namespace")
	taskQueue := c.String("task-queue")

	// Initialize the logger
	lvl := new(slog.LevelVar)
	lvl.Set(slog.LevelInfo)
	l := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))

	// Handle the health check flag
	if c.Bool("check-connection") {
		if err := worker.CheckConnection(c.Context, l, temporalAddr, temporalNamespace); err != nil {
			log.Fatalf("Health check failed: %v", err)
		}
		// If the health check is successful, we exit cleanly.
		return nil
	}

	if err := worker.RunWorker(c.Context, l, temporalAddr, temporalNamespace, taskQueue); err != nil {
		log.Fatalln("Worker failed to run", "error", err)
	}

	return nil
}
