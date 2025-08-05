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
	case ToolNamePullContent:
		var params PullContentInput
		if err = json.Unmarshal(input, &params); err != nil {
			return nil, fmt.Errorf("invalid input for %s: %w", ToolNamePullContent, err)
		}
		var res json.RawMessage
		err = workflow.ExecuteActivity(ctx, a.PullContentActivity, params).Get(ctx, &res)
		result = res
	case ToolNameGetGitHubIssue:
		var params struct {
			Owner       string `json:"owner"`
			Repo        string `json:"repo"`
			IssueNumber int    `json:"issue_number"`
		}
		if err = json.Unmarshal(input, &params); err != nil {
			return nil, fmt.Errorf("invalid input for %s: %w", ToolNameGetGitHubIssue, err)
		}
		var res *GitHubIssueContent
		err = workflow.ExecuteActivity(ctx, a.GetGitHubIssue, params.Owner, params.Repo, params.IssueNumber).Get(ctx, &res)
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
	case ToolNameGetGitHubUser:
		var params struct {
			Username string `json:"username"`
		}
		if err = json.Unmarshal(input, &params); err != nil {
			return nil, fmt.Errorf("invalid input for %s: %w", ToolNameGetGitHubUser, err)
		}
		var res *GitHubUserContent
		err = workflow.ExecuteActivity(ctx, a.GetGitHubUser, params.Username).Get(ctx, &res)
		result = res
	case ToolNameGetRedditUserStats:
		var params struct {
			Username string `json:"username"`
		}
		if err = json.Unmarshal(input, &params); err != nil {
			return nil, fmt.Errorf("invalid input for %s: %w", ToolNameGetRedditUserStats, err)
		}
		var res *RedditUserStats
		err = workflow.ExecuteActivity(ctx, a.GetRedditUserStats, params.Username).Get(ctx, &res)
		result = res
	case ToolNameGetSubredditStats:
		var params struct {
			SubredditName string `json:"subreddit_name"`
		}
		if err = json.Unmarshal(input, &params); err != nil {
			return nil, fmt.Errorf("invalid input for %s: %w", ToolNameGetSubredditStats, err)
		}
		var res *SubredditStats
		err = workflow.ExecuteActivity(ctx, a.GetSubredditStats, params.SubredditName).Get(ctx, &res)
		result = res
	case ToolNameGetYouTubeChannelStats:
		var params struct {
			ChannelID string `json:"channel_id"`
		}
		if err = json.Unmarshal(input, &params); err != nil {
			return nil, fmt.Errorf("invalid input for %s: %w", ToolNameGetYouTubeChannelStats, err)
		}
		var res *YouTubeChannelStats
		err = workflow.ExecuteActivity(ctx, a.GetYoutubeChannelStats, params.ChannelID).Get(ctx, &res)
		result = res
	case ToolNameGetBlueskyUserStats:
		var params struct {
			UserHandle string `json:"user_handle"`
		}
		if err = json.Unmarshal(input, &params); err != nil {
			return nil, fmt.Errorf("invalid input for %s: %w", ToolNameGetBlueskyUserStats, err)
		}
		var res *BlueskyUserStats
		err = workflow.ExecuteActivity(ctx, a.GetBlueskyUserStats, params.UserHandle).Get(ctx, &res)
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
	case ToolNameValidatePayoutWallet:
		var params struct {
			PayoutWallet     string `json:"payout_wallet"`
			ValidationPrompt string `json:"validation_prompt"`
		}
		if err = json.Unmarshal(input, &params); err != nil {
			return nil, fmt.Errorf("invalid input for %s: %w", ToolNameValidatePayoutWallet, err)
		}
		var res *ValidatePayoutWalletResult
		err = workflow.ExecuteActivity(ctx, a.ValidatePayoutWallet, params.PayoutWallet, params.ValidationPrompt).Get(ctx, &res)
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
	case ToolNameGetSteamPlayerInfo:
		var params struct {
			AccountID int `json:"account_id"`
		}
		if err = json.Unmarshal(input, &params); err != nil {
			return nil, fmt.Errorf("invalid input for %s: %w", ToolNameGetSteamPlayerInfo, err)
		}
		var res *OpenDotaPlayerInfo
		err = workflow.ExecuteActivity(ctx, a.GetSteamPlayerInfo, params.AccountID).Get(ctx, &res)
		result = res
	case ToolNameGetWalletAddressFromRedditProfile:
		var params struct {
			Username string `json:"username"`
		}
		if err = json.Unmarshal(input, &params); err != nil {
			return nil, fmt.Errorf("invalid input for %s: %w", ToolNameGetWalletAddressFromRedditProfile, err)
		}
		var res string
		err = workflow.ExecuteActivity(ctx, a.GetWalletAddressFromRedditProfile, params.Username).Get(ctx, &res)
		result = res
	case ToolNameGetWalletAddressFromGitHubProfile:
		var params struct {
			Username string `json:"username"`
		}
		if err = json.Unmarshal(input, &params); err != nil {
			return nil, fmt.Errorf("invalid input for %s: %w", ToolNameGetWalletAddressFromGitHubProfile, err)
		}
		var res string
		err = workflow.ExecuteActivity(ctx, a.GetWalletAddressFromGitHubProfile, params.Username).Get(ctx, &res)
		result = res
	case ToolNameGetWalletAddressFromBlueskyProfile:
		var params struct {
			UserHandle string `json:"user_handle"`
		}
		if err = json.Unmarshal(input, &params); err != nil {
			return nil, fmt.Errorf("invalid input for %s: %w", ToolNameGetWalletAddressFromBlueskyProfile, err)
		}
		var res string
		err = workflow.ExecuteActivity(ctx, a.GetWalletAddressFromBlueskyProfile, params.UserHandle).Get(ctx, &res)
		result = res
	case ToolNameGetWalletAddressFromInstagramProfile:
		var params struct {
			Username string `json:"username"`
		}
		if err = json.Unmarshal(input, &params); err != nil {
			return nil, fmt.Errorf("invalid input for %s: %w", ToolNameGetWalletAddressFromInstagramProfile, err)
		}
		var res string
		err = workflow.ExecuteActivity(ctx, a.GetWalletAddressFromInstagramProfile, params.Username).Get(ctx, &res)
		result = res
	case ToolNameGetWalletAddressFromSteamProfile:
		var params struct {
			AccountID int `json:"account_id"`
		}
		if err = json.Unmarshal(input, &params); err != nil {
			return nil, fmt.Errorf("invalid input for %s: %w", ToolNameGetWalletAddressFromSteamProfile, err)
		}
		var res string
		err = workflow.ExecuteActivity(ctx, a.GetWalletAddressFromSteamProfile, params.AccountID).Get(ctx, &res)
		result = res
	case ToolNameGetWalletAddressFromYouTubeProfile:
		var params struct {
			ChannelID string `json:"channel_id"`
		}
		if err = json.Unmarshal(input, &params); err != nil {
			return nil, fmt.Errorf("invalid input for %s: %w", ToolNameGetWalletAddressFromYouTubeProfile, err)
		}
		var res string
		err = workflow.ExecuteActivity(ctx, a.GetWalletAddressFromYouTubeProfile, params.ChannelID).Get(ctx, &res)
		result = res
	case ToolNameGetWalletAddressFromTwitchProfile:
		var params struct {
			Username string `json:"username"`
		}
		if err = json.Unmarshal(input, &params); err != nil {
			return nil, fmt.Errorf("invalid input for %s: %w", ToolNameGetWalletAddressFromTwitchProfile, err)
		}
		var res string
		err = workflow.ExecuteActivity(ctx, a.GetWalletAddressFromTwitchProfile, params.Username).Get(ctx, &res)
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
