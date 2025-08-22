package abb

const (
	ToolNamePullContent               = "pull_content"
	ToolNameSubmitDecision            = "submit_decision"
	ToolNameAnalyzeImageURL           = "analyze_image_url"
	ToolNameDetectMaliciousContent    = "detect_malicious_content"
	ToolNameGetRedditChildrenComments = "get_reddit_children_comments"
	ToolNameGetClosingPR              = "get_githubclosing_pr"
)

var PullContentTool = Tool{
	Name: ToolNamePullContent,
	Description: `General-purpose fetch for internet content and user profiles.

Use this to retrieve:
- Posts, comments, videos, clips, issues, reviews, etc.
- User profiles (content_kind = "user") to get normalized profile_text you can reason over.

Usage patterns:
- For bounties that require content on a platform, first call pull_content with the appropriate content_kind and content_id.
- For bounty checks that require finding a wallet in a user's profile, first call pull_content with content_kind = "user" to obtain profile_text, then call validate_payout_wallet with a prompt that includes that profile_text.

Valid content_kind by platform (and expected content_id):
- Reddit: post, comment, user
- YouTube: video, comment, user
- Twitch: video, clip, user
- Hacker News: post, comment
- Bluesky: post, user
- Instagram: post, user
- IncentivizeThis: bounty
- TripAdvisor: review
- Steam: dota2chat (only)
- GitHub: issue, user (content_id = owner/repo/number for issues, username for user)

Content ID formats (examples):
- GitHub:
  - issue: "owner/repo/123" or "https://github.com/owner/repo/issues/123" (both supported)
  - user: "username"
- Reddit:
  - post: "t3_abcdef"
  - comment: "t1_abc123"
  - user: "username"
- YouTube:
  - video: "VIDEO_ID" (e.g., "dQw4w9WgXcQ")
  - user: "CHANNEL_ID"
- Twitch:
  - video: "VIDEO_ID"
  - clip: "CLIP_ID"
  - user: "username"
- Bluesky:
  - post: "https://bsky.app/profile/{handle}/post/{rkey}" or "at://{did}/app.bsky.feed.post/{rkey}" (both supported)
  - user: "handle" (with or without domain, e.g., "alice" or "alice.bsky.social")
- Instagram:
  - post: reel/post short code or full URL (both supported)
  - user: "username"
- TripAdvisor:
  - review: site-specific review ID
- Steam:
  - dota2chat: match identifier from OpenDota

`,
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
				"description": "The type of content. This is platform dependent. The valid options are:\n- Reddit: post, comment, user, subreddit\n- YouTube: video, comment, user, channel\n- Twitch: video, clip, user\n- Hacker News: post, comment\n- Bluesky: post, user\n- Instagram: post, user\n- IncentivizeThis: bounty\n- TripAdvisor: review\n- Steam: dota2chat, user (user uses OpenDota player info)\n- GitHub: issue, user",
				"enum":        []interface{}{"post", "comment", "video", "clip", "bounty", "review", "dota2chat", "issue", "user", "subreddit", "channel"},
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
	Name:        ToolNameSubmitDecision,
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
	Name:        ToolNameAnalyzeImageURL,
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
	Name:        ToolNameDetectMaliciousContent,
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

var GetRedditChildrenCommentsTool = Tool{
	Name:        ToolNameGetRedditChildrenComments,
	Description: "Fetches the direct replies (children comments) for a given Reddit post or comment. Use this tool when you need to analyze a discussion thread, such as checking for replies to a specific comment to fulfill a bounty requirement.",
	Parameters: map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"id": map[string]interface{}{
				"type":        "string",
				"description": "The full Reddit ID for the post or comment (e.g., 't3_abcdef' for a post, 't1_abc123' for a comment) whose children you want to fetch.",
			},
		},
		"required": []interface{}{"id"},
	},
}

var GetClosingPRTool = Tool{
	Name:        ToolNameGetClosingPR,
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
