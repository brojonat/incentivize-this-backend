package abb

var GetContentDetailsTool = Tool{
	Name:        "get_content_details",
	Description: "Fetches the full details of a piece of content (like a post, comment, or video) from a specified platform. This is the primary way to get information about content.",
	Parameters: map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"platform": map[string]interface{}{
				"type":        "string",
				"description": "The platform the content is on.",
				"enum":        []interface{}{"reddit", "youtube", "twitch", "hackernews", "bluesky", "instagram", "incentivizethis", "tripadvisor", "steam", "github"},
			},
			"content_kind": map[string]interface{}{
				"type":        "string",
				"description": "The type of content. This is platform dependent. The valid options are:\n- Reddit: post, comment\n- YouTube: video, comment\n- Twitch: video, clip\n- Hacker News: post, comment\n- Bluesky: post\n- Instagram: post\n- IncentivizeThis: bounty\n- TripAdvisor: review\n- Steam: dota2chat (this is the ONLY valid content kind for Steam)\n- GitHub: issue",
				"enum":        []interface{}{"post", "comment", "video", "clip", "bounty", "review", "dota2chat", "issue"},
			},
			"content_id": map[string]interface{}{
				"type":        "string",
				"description": "The unique identifier for the content on the specified platform.",
			},
		},
		"required": []interface{}{"platform", "content_kind", "content_id"},
	},
}

var SubmitDecisionTool = Tool{
	Name:        "submit_decision",
	Description: "Submits the final decision on whether the content is approved for the bounty, along with the reason and payout amount.",
	Parameters: map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"is_approved": map[string]interface{}{
				"type":        "boolean",
				"description": "Whether the content is approved for the bounty payout.",
			},
			"reason": map[string]interface{}{
				"type":        "string",
				"description": "A detailed explanation for the decision.",
			},
		},
		"required": []interface{}{"is_approved", "reason"},
	},
}

var AnalyzeImageURLTool = Tool{
	Name:        "analyze_image_url",
	Description: "Analyzes an image from a URL to determine if it meets the bounty requirements. This is useful for bounties that require specific types of images.",
	Parameters: map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"image_url": map[string]interface{}{
				"type":        "string",
				"description": "The URL of the image to analyze.",
			},
			"prompt": map[string]interface{}{
				"type":        "string",
				"description": "The specific requirement the image must meet. For example, 'the image must contain a cat'.",
			},
		},
		"required": []interface{}{"image_url", "prompt"},
	},
}

var DetectMaliciousContentTool = Tool{
	Name:        "detect_malicious_content",
	Description: "Detects if a piece of content contains a prompt injection attack or other malicious content.",
	Parameters: map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"content": map[string]interface{}{
				"type":        "string",
				"description": "The content to check.",
			},
		},
		"required": []interface{}{"content"},
	},
}

var ValidatePayoutWalletTool = Tool{
	Name:        "validate_payout_wallet",
	Description: "Validates if a payout wallet is eligible for a bounty based on the content and bounty prompt.",
	Parameters: map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"payout_wallet": map[string]interface{}{
				"type":        "string",
				"description": "The payout wallet address to validate.",
			},
			"validation_prompt": map[string]interface{}{
				"type":        "string",
				"description": "A specific prompt to guide the LLM in validating the wallet. This prompt should contain the bounty requirements as well as any relevant content that may be relevant to the payout wallet requirements.",
			},
		},
		"required": []interface{}{"payout_wallet", "validation_prompt"},
	},
}

var GetGitHubIssueTool = Tool{
	Name:        "get_github_issue",
	Description: "Gets the details of a GitHub issue.",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"owner": map[string]any{
				"type":        "string",
				"description": "The owner of the repository.",
			},
			"repo": map[string]any{
				"type":        "string",
				"description": "The name of the repository.",
			},
			"issue_number": map[string]any{
				"type":        "integer",
				"description": "The number of the issue.",
			},
		},
		"required": []string{"owner", "repo", "issue_number"},
	},
}

var GetClosingPRTool = Tool{
	Name:        "get_githubclosing_pr",
	Description: "Gets the details of the pull request that closed a GitHub issue.",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"owner": map[string]any{
				"type":        "string",
				"description": "The owner of the repository.",
			},
			"repo": map[string]any{
				"type":        "string",
				"description": "The name of the repository.",
			},
			"issue_number": map[string]any{
				"type":        "integer",
				"description": "The number of the issue that the PR closed.",
			},
		},
		"required": []string{"owner", "repo", "issue_number"},
	},
}

var GetGitHubUserTool = Tool{
	Name:        "get_github_user",
	Description: "Gets the details of a GitHub user.",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"username": map[string]any{
				"type":        "string",
				"description": "The username of the GitHub user.",
			},
		},
		"required": []string{"username"},
	},
}

var GetRedditUserStatsTool = Tool{
	Name:        "get_reddit_user_stats",
	Description: "Gets the statistics of a Reddit user, such as karma and account age.",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"username": map[string]any{
				"type":        "string",
				"description": "The username of the Reddit user.",
			},
		},
		"required": []string{"username"},
	},
}

var GetSubredditStatsTool = Tool{
	Name:        "get_subreddit_stats",
	Description: "Gets the statistics of a subreddit, such as subscriber count.",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"subreddit_name": map[string]any{
				"type":        "string",
				"description": "The name of the subreddit.",
			},
		},
		"required": []string{"subreddit_name"},
	},
}

var GetYouTubeChannelStatsTool = Tool{
	Name:        "get_youtube_channel_stats",
	Description: "Gets the statistics of a YouTube channel, such as subscriber count.",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"channel_id": map[string]any{
				"type":        "string",
				"description": "The ID of the YouTube channel.",
			},
		},
		"required": []string{"channel_id"},
	},
}

var GetBlueskyUserStatsTool = Tool{
	Name:        "get_bluesky_user_stats",
	Description: "Gets the statistics of a Bluesky user, such as follower count.",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"user_handle": map[string]any{
				"type":        "string",
				"description": "The handle of the Bluesky user.",
			},
		},
		"required": []string{"user_handle"},
	},
}

var GetSteamPlayerInfoTool = Tool{
	Name:        "get_steam_player_info",
	Description: "Gets player information from OpenDota using their Steam32 account ID.",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"account_id": map[string]any{
				"type":        "integer",
				"description": "The player's Steam32 account ID.",
			},
		},
		"required": []string{"account_id"},
	},
}

var GetWalletAddressFromRedditProfileTool = Tool{
	Name:        "get_wallet_address_from_reddit_profile",
	Description: "Gets a Solana wallet address from a Reddit user's profile description.",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"username": map[string]any{
				"type":        "string",
				"description": "The username of the Reddit user.",
			},
		},
		"required": []string{"username"},
	},
}

var GetWalletAddressFromGitHubProfileTool = Tool{
	Name:        "get_wallet_address_from_github_profile",
	Description: "Gets a Solana wallet address from a GitHub user's profile.",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"username": map[string]any{
				"type":        "string",
				"description": "The username of the GitHub user.",
			},
		},
		"required": []string{"username"},
	},
}

var GetWalletAddressFromBlueskyProfileTool = Tool{
	Name:        "get_wallet_address_from_bluesky_profile",
	Description: "Gets a Solana wallet address from a Bluesky user's profile.",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"user_handle": map[string]any{
				"type":        "string",
				"description": "The handle of the Bluesky user.",
			},
		},
		"required": []string{"user_handle"},
	},
}

var GetWalletAddressFromInstagramProfileTool = Tool{
	Name:        "get_wallet_address_from_instagram_profile",
	Description: "Gets a Solana wallet address from an Instagram user's profile.",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"username": map[string]any{
				"type":        "string",
				"description": "The username of the Instagram user.",
			},
		},
		"required": []string{"username"},
	},
}

var GetWalletAddressFromSteamProfileTool = Tool{
	Name:        "get_wallet_address_from_steam_profile",
	Description: "Gets a Solana wallet address from a Steam user's profile.",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"account_id": map[string]any{
				"type":        "integer",
				"description": "The player's Steam32 account ID.",
			},
		},
		"required": []string{"account_id"},
	},
}

var GetWalletAddressFromYouTubeProfileTool = Tool{
	Name:        "get_wallet_address_from_youtube_profile",
	Description: "Gets a Solana wallet address from a YouTube channel's description.",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"channel_id": map[string]any{
				"type":        "string",
				"description": "The ID of the YouTube channel.",
			},
		},
		"required": []string{"channel_id"},
	},
}

var GetWalletAddressFromTwitchProfileTool = Tool{
	Name:        "get_wallet_address_from_twitch_profile",
	Description: "Gets a Solana wallet address from a Twitch user's profile.",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"username": map[string]any{
				"type":        "string",
				"description": "The username of the Twitch user.",
			},
		},
		"required": []string{"username"},
	},
}

// TODO: additional tools to implement:
// - Get wallet address from reddit profile
// - Get wallet address from github profile
// - Get wallet address from bluesky profile
// - Get wallet address from instagram profile
// - Get wallet address from steam profile
// - Get wallet address from youtube profile
// - Get wallet address from twitch profile
