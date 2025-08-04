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
	case "GetGitHubUser":
		var params struct {
			Username string `json:"username"`
		}
		if err = json.Unmarshal(input, &params); err != nil {
			return nil, fmt.Errorf("invalid input for GetGitHubUser: %w", err)
		}
		var res *GitHubUserContent
		err = workflow.ExecuteActivity(ctx, a.GetGitHubUser, params.Username).Get(ctx, &res)
		result = res
	// todo: add more activities here
	default:
		return nil, fmt.Errorf("unknown activity: %s", activityName)
	}

	return result, err
}
