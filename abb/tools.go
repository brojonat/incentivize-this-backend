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
