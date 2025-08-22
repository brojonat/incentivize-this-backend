package abb

import (
	"encoding/json"
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

func DebugWorkflow(ctx workflow.Context, activityName string, input json.RawMessage) (interface{}, error) {
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		// these activities should only get one attempt
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 1,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	a := &Activities{}
	var result interface{}
	var err error

	switch activityName {
	case ToolNamePullContent:
		var params PullContentInput
		if err = json.Unmarshal(input, &params); err != nil {
			return nil, fmt.Errorf("invalid input for %s: %w", ToolNamePullContent, err)
		}
		var res json.RawMessage
		err = workflow.ExecuteActivity(ctx, a.PullContentActivity, params).Get(ctx, &res)
		result = res
	case ToolNameGetClosingPR:
		var params struct {
			Owner       string `json:"owner"`
			Repo        string `json:"repo"`
			IssueNumber int    `json:"issue_number"`
		}
		if err = json.Unmarshal(input, &params); err != nil {
			return nil, fmt.Errorf("invalid input for %s: %w", ToolNameGetClosingPR, err)
		}
		var res *GitHubPullRequestContent
		err = workflow.ExecuteActivity(ctx, a.GetClosingPullRequest, params.Owner, params.Repo, params.IssueNumber).Get(ctx, &res)
		result = res
	case ToolNameAnalyzeImageURL:
		var params struct {
			ImageURL string `json:"image_url"`
			Prompt   string `json:"prompt"`
		}
		if err = json.Unmarshal(input, &params); err != nil {
			return nil, fmt.Errorf("invalid input for %s: %w", ToolNameAnalyzeImageURL, err)
		}
		var res *CheckContentRequirementsResult
		err = workflow.ExecuteActivity(ctx, a.AnalyzeImageURL, params.ImageURL, params.Prompt).Get(ctx, &res)
		result = res
	case ToolNameDetectMaliciousContent:
		var params struct {
			Content string `json:"content"`
		}
		if err = json.Unmarshal(input, &params); err != nil {
			return nil, fmt.Errorf("invalid input for %s: %w", ToolNameDetectMaliciousContent, err)
		}
		var res *DetectMaliciousContentResult
		err = workflow.ExecuteActivity(ctx, a.DetectMaliciousContent, params.Content).Get(ctx, &res)
		result = res
	case ToolNameSubmitDecision:
		// This is the final decision from the LLM.
		var decisionArgs struct {
			IsApproved bool   `json:"is_approved"`
			Reason     string `json:"reason"`
		}
		if err := json.Unmarshal([]byte(input), &decisionArgs); err != nil {
			return nil, fmt.Errorf("failed to unmarshal decision tool arguments: %w", err)
		}
		return &decisionArgs, nil
	default:
		return nil, fmt.Errorf("unknown activity: %s", activityName)
	}

	return result, err
}
