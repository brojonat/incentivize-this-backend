# Integration Testing Plan

## 1. Overview

To facilitate automated integration testing of our platform's features, especially those interacting with external APIs, we need a way to invoke specific business logic components in isolation. Our activities are the ideal candidates for this, as they encapsulate interactions with third-party services.

This document outlines a plan to create a testing utility that allows us to trigger any registered activity with custom inputs directly from the command line. This will be accomplished by introducing a new debug CLI command and a corresponding Temporal workflow, which will be run by a separate debug worker.

## 2. New CLI command: `debug run-activity`

We will add a new subcommand to our existing `debug` command set, located in `cmd/abb/debug.go`.

**Command:**

```bash
abb debug run-activity
```

**Flags:**

- `--activity-name` (`-a`): (Required) The name of the activity to execute (e.g., `GetGithubRepo`). This name should match one of the cases in the `DebugWorkflow`.
- `--data` (`-d`): (Required) A JSON string that provides the arguments for the activity. The structure of the JSON object must match what the target activity expects.
- `--wait-result`: (Optional, default: `true`) A boolean flag indicating whether the command should wait for the workflow to complete and print the result. If false, it will just start the workflow and print the workflow ID.

**Example Usage:**

```bash
go run ./cmd/abb/main.go debug run-activity \
  -a GetGithubRepo \
  -d '{"owner": "uber", "repo": "temporal"}'
```

The CLI command will be responsible for:

1.  Parsing the command-line arguments.
2.  Initializing a Temporal client.
3.  Executing the `DebugWorkflow` with the provided activity name and input JSON.
4.  Waiting for the workflow to complete (if `--wait-result` is true) and printing the returned result or error to standard output.

## 3. New Workflow: `DebugWorkflow`

A new workflow, `DebugWorkflow`, will be created to handle the execution of the specified activity. This keeps the execution logic within the Temporal environment, allowing for proper retries, timeouts, and visibility.

**Location:** `abb/workflow_debug.go` (new file)

**Parameters:**

- `activityName string`
- `input json.RawMessage`

**Logic:**
The `DebugWorkflow` will contain a `switch` statement that routes the request to the correct activity based on `activityName`. For each activity, it will:

1.  Define a struct that matches the JSON input shape for that activity.
2.  Unmarshal the `input` (`json.RawMessage`) into this struct.
3.  Call the corresponding activity function from the `Activities` struct, passing the unmarshalled parameters.
4.  Return the result and error from the activity call.

**Example `DebugWorkflow` structure:**

```go
package abb

import (
    "encoding/json"
    "fmt"
    "time"

    "go.temporal.io/sdk/workflow"
)

func DebugWorkflow(ctx workflow.Context, activityName string, input json.RawMessage) (interface{}, error) {
    ao := workflow.ActivityOptions{
        StartToCloseTimeout: 30 * time.Second,
    }
    ctx = workflow.WithActivityOptions(ctx, ao)

    a := &Activities{}
    var result interface{}
    var err error

    switch activityName {
    case "GetGithubRepo":
        var params struct {
            Owner string `json:"owner"`
            Repo  string `json:"repo"`
        }
        if err := json.Unmarshal(input, &params); err != nil {
            return nil, fmt.Errorf("invalid input for GetGithubRepo: %w", err)
        }
        var res *GithubRepo // Use the correct return type of the activity
        err = workflow.ExecuteActivity(ctx, a.GetGithubRepo, params.Owner, params.Repo).Get(ctx, &res)
        result = res

    // ... other cases for other activities

    default:
        return nil, fmt.Errorf("unknown activity: %s", activityName)
    }

    return result, err
}
```

## 4. New CLI command: `debug debug-worker`

To avoid registering the `DebugWorkflow` in the production worker, we will create a separate worker for debugging purposes.

**Command:**

```bash
abb debug debug-worker
```

This command will start a new Temporal worker on the `affiliate_bounty_board_debug` task queue that registers the `DebugWorkflow` and all activities. This worker should be run in a separate terminal during testing.

## 5. Implementation Steps & Testing Workflow

1.  **Run the debug worker:** In a terminal, run the following command to start the debug worker:
    ```bash
    go run ./cmd/abb/main.go debug debug-worker
    ```
2.  **Run an activity:** In a separate terminal, use the `run-activity` command to execute an activity:
    ```bash
    go run ./cmd/abb/main.go debug run-activity \
      -a GetGitHubUser \
      -d '{"username": "temporalio"}'
    ```
3.  **Create `abb/workflow_debug.go`**: Implement the `DebugWorkflow` as described above. Initially, we can populate it with one or two activities to prove the concept.
4.  **Update `cmd/abb/debug.go`**: Add the new `run-activity` and `debug-worker` commands and their `Action` functions to the `debugCommands` slice.
5.  **Documentation**: Ensure the `README.md` or other relevant developer documentation is updated with instructions on how to use this new testing tool.
