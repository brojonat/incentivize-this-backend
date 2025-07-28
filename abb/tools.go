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
				"enum":        []interface{}{"reddit", "youtube", "twitch", "hackernews", "bluesky", "instagram", "incentivizethis", "tripadvisor"},
			},
			"content_kind": map[string]interface{}{
				"type":        "string",
				"description": "The type of content.",
				"enum":        []interface{}{"post", "comment", "video", "clip", "bounty", "review"},
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
