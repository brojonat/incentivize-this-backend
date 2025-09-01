# Integration Tests

## 1. Overview

To facilitate automated integration testing of our platform's features that interact with external APIs, we provide debug CLI commands that execute activities and tool-backed flows inside a dedicated Temporal debug worker.

Quickstart:

- Start a dev session
- Ensure worker envs are loaded (e.g., source `.env.worker` or export needed vars)
- In one terminal:
  ```bash
  abb debug run-debug-worker | tee logs/integration-worker.log
  ```
- In a separate terminal:
  ```bash
  abb debug run-integration-tests | tee logs/integration-stdout.log
  ```

What these tests do:

- Launch `DebugWorkflow` runs that call activities like `pull_content`, `analyze_image_url`, `validate_payout_wallet`, etc.
- Cover multiple platforms/inputs for `pull_content` and other tools.
- Print failures inline and a final pass count summary.

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

## 3. Workflow: `DebugWorkflow`

A new workflow, `DebugWorkflow`, will be created to handle the execution of the specified activity. This keeps the execution logic within the Temporal environment, allowing for proper retries, timeouts, and visibility.

**Location:** `abb/workflow_debug.go`

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

## 4. CLI command: `debug run-debug-worker`

To avoid registering the `DebugWorkflow` in the production worker, we will create a separate worker for debugging purposes.

**Command:**

```bash
abb debug run-debug-worker
```

This command will start a new Temporal worker on the `affiliate_bounty_board_debug` task queue that registers the `DebugWorkflow` and all activities. This worker should be run in a separate terminal during testing.

## 5. Implementation Steps & Testing Workflow

1.  **Run the debug worker:** In a terminal, start the debug worker (binary or `go run`):
    ```bash
    abb debug run-debug-worker
    # or
    go run ./cmd/abb/main.go debug run-debug-worker
    ```
2.  **Run an activity:** In a separate terminal, use the `run-activity` command to execute an activity:
    ```bash
    go run ./cmd/abb/main.go debug run-activity \
      -a GetGitHubUser \
      -d '{"username": "temporalio"}'
    ```
    Or run the full integration suite (optionally filter with repeated `--tool` flags):
    ```bash
    abb debug run-integration-tests
    ```
3.  **Create `abb/workflow_debug.go`**: Implement the `DebugWorkflow` as described above. Initially, we can populate it with one or two activities to prove the concept.
4.  **Update `cmd/abb/debug.go`**: Add the new `run-activity` and `debug-worker` commands and their `Action` functions to the `debugCommands` slice.
5.  **Documentation**: Ensure the `README.md` or other relevant developer documentation is updated with instructions on how to use this new testing tool.
