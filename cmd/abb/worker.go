package main

import (
	"log"

	"github.com/brojonat/affiliate-bounty-board/abb"
	"github.com/urfave/cli/v2"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
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

	// Create the client object just once per process
	client, err := client.Dial(client.Options{
		HostPort:  temporalAddr,
		Namespace: temporalNamespace,
	})
	if err != nil {
		log.Fatalln("Unable to create Temporal client", err)
	}
	defer client.Close()

	// Initialize the Temporal worker.
	w := worker.New(client, taskQueue, worker.Options{})

	// Register the workflows.
	w.RegisterWorkflow(abb.BountyAssessmentWorkflow)
	w.RegisterWorkflow(abb.OrchestratorWorkflow)
	w.RegisterWorkflow(abb.ContactUsNotifyWorkflow)
	w.RegisterWorkflow(abb.EmailTokenWorkflow)

	// Register the activities.
	activities, err := abb.NewActivities()
	if err != nil {
		log.Fatalln("Unable to create activities", err)
	}
	w.RegisterActivity(activities)

	// Start the worker.
	err = w.Run(worker.InterruptCh())
	if err != nil {
		log.Fatalln("Unable to start worker", err)
	}
	return err
}
