package http

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/brojonat/affiliate-bounty-board/abb"
	"github.com/brojonat/affiliate-bounty-board/solana"
	solanago "github.com/gagliardetto/solana-go"
	"github.com/google/uuid"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
)

// Embed static assets into the binary
//
//go:embed static/images/* static/css/* static/*.html
var staticAssets embed.FS

// Embed templates into the binary
//
//go:embed templates/**/*.html
var templateFiles embed.FS

// getTemplateFS returns the template filesystem, supporting hot-reload in development
func getTemplateFS() fs.FS {
	return templateFiles
}

// getStaticFS returns the static asset filesystem
func getStaticFS() fs.FS {
	return staticAssets
}

// parseTemplates parses all templates from the embedded filesystem
func parseTemplates() (*template.Template, error) {
	tmpl := template.New("")

	// Parse all template files
	err := fs.WalkDir(getTemplateFS(), "templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !d.IsDir() && (len(path) > 5 && path[len(path)-5:] == ".html") {
			content, err := fs.ReadFile(getTemplateFS(), path)
			if err != nil {
				return err
			}

			_, err = tmpl.New(path).Parse(string(content))
			if err != nil {
				return err
			}
		}

		return nil
	})

	return tmpl, err
}

// handleLanding serves the marketing landing page
func handleLanding(logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tmpl, err := parseTemplates()
		if err != nil {
			logger.Error("failed to parse templates", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		data := map[string]interface{}{
			"Title": "Welcome",
		}

		// Execute the base layout with the marketing landing page
		err = tmpl.ExecuteTemplate(w, "templates/layouts/base.html", data)
		if err != nil {
			logger.Error("failed to execute template", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
	}
}

// handleBounties serves the bounty listing page
func handleBounties(logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Parse base and bounties templates together
		tmpl, err := template.ParseFS(getTemplateFS(),
			"templates/layouts/base.html",
			"templates/pages/bounties.html",
			"templates/partials/create_bounty_modal.html")
		if err != nil {
			logger.Error("failed to parse templates", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		// Return empty arrays - HTMX will fetch the actual data
		data := map[string]interface{}{
			"Title": "Bounties",
		}

		// Execute the base.html template (the filename, not a defined block)
		err = tmpl.Execute(w, data)
		if err != nil {
			logger.Error("failed to execute template", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
	}
}

// handleAbout serves the about page
func handleAbout(logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Parse base and about templates together
		tmpl, err := template.ParseFS(getTemplateFS(),
			"templates/layouts/base.html",
			"templates/pages/about.html")
		if err != nil {
			logger.Error("failed to parse templates", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		data := map[string]interface{}{
			"Title": "About",
		}

		// Execute the template
		err = tmpl.Execute(w, data)
		if err != nil {
			logger.Error("failed to execute template", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
	}
}

// toJSON converts a Go value to JSON string for use in templates
func toJSON(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// writeHTMLBadRequestError writes an HTML error response for form submissions using a template
func writeHTMLBadRequestError(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusBadRequest)

	// Parse error template
	tmpl, parseErr := template.ParseFS(getTemplateFS(), "templates/partials/error_bad_request.html")
	if parseErr != nil {
		// Fallback to simple HTML if template parsing fails
		fmt.Fprintf(w, `<div class="text-red-500">%s</div>`, err.Error())
		return
	}

	// Execute template with error message
	data := map[string]interface{}{
		"Message": err.Error(),
	}
	if execErr := tmpl.ExecuteTemplate(w, "error-bad-request", data); execErr != nil {
		// Fallback to simple HTML if template execution fails
		fmt.Fprintf(w, `<div class="text-red-500">%s</div>`, err.Error())
	}
}

// writeHTMLInternalError writes an HTML error response for internal errors using a template
func writeHTMLInternalError(logger *slog.Logger, w http.ResponseWriter, err error) {
	logger.Error("internal error", "error", err.Error())
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusInternalServerError)

	// Parse error template
	tmpl, parseErr := template.ParseFS(getTemplateFS(), "templates/partials/error_internal.html")
	if parseErr != nil {
		// Fallback to simple HTML if template parsing fails
		fmt.Fprintf(w, `<div class="text-red-500">An internal error occurred. Please try again later.</div>`)
		return
	}

	// Execute template
	data := map[string]interface{}{}
	if execErr := tmpl.ExecuteTemplate(w, "error-internal", data); execErr != nil {
		// Fallback to simple HTML if template execution fails
		fmt.Fprintf(w, `<div class="text-red-500">An internal error occurred. Please try again later.</div>`)
	}
}

// writeHTMLErrorDialog writes an HTML error dialog response for modal displays
func writeHTMLErrorDialog(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "text/html")
	// Tell HTMX to swap the error response into the target
	w.Header().Set("HX-Reswap", "innerHTML")
	w.Header().Set("HX-Retarget", "#create-bounty-form-container")
	w.WriteHeader(http.StatusBadRequest)

	// Parse error dialog template
	tmpl, parseErr := template.ParseFS(getTemplateFS(), "templates/partials/dialog_error.html")
	if parseErr != nil {
		// Fallback to simple HTML if template parsing fails
		fmt.Fprintf(w, `<div class="message-error">%s</div>`, err.Error())
		return
	}

	// Execute template with error message
	data := map[string]interface{}{
		"Message": err.Error(),
	}
	if execErr := tmpl.ExecuteTemplate(w, "dialog-error", data); execErr != nil {
		// Fallback to simple HTML if template execution fails
		fmt.Fprintf(w, `<div class="message-error">%s</div>`, err.Error())
	}
}

// writeHTMLSuccessDialog writes an HTML success dialog response for modal displays
func writeHTMLSuccessDialog(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)

	// Parse success dialog template
	tmpl, parseErr := template.ParseFS(getTemplateFS(), "templates/partials/dialog_success.html")
	if parseErr != nil {
		// Fallback to simple HTML if template parsing fails
		fmt.Fprintf(w, `<div class="message-success">%s</div>`, message)
		return
	}

	// Execute template with success message
	data := map[string]interface{}{
		"Message": message,
	}
	if execErr := tmpl.ExecuteTemplate(w, "dialog-success", data); execErr != nil {
		// Fallback to simple HTML if template execution fails
		fmt.Fprintf(w, `<div class="message-success">%s</div>`, message)
	}
}

// handleCreateBountyForm handles form submission for creating a bounty
// It does the same thing as handleCreateBounty but returns HTML responses instead of JSON
func handleCreateBountyForm(
	logger *slog.Logger,
	tc client.Client,
	llmProvider abb.LLMProvider,
	llmEmbedProvider abb.LLMEmbeddingProvider,
	platformFeePercent float64,
	defaultMaxPayoutsPerUser int,
	env string,
	prompts struct {
		InferBountyTitle   string
		InferContentParams string
		ContentModeration  string
		HardenBounty       string
	},
	escrowWallet solanago.PublicKey,
	usdcMint solanago.PublicKey,
) http.HandlerFunc {
	// Define a local struct for parsing the LLM's JSON response for content parameters
	type inferredContentParamsRequest struct {
		PlatformKind string `json:"PlatformKind"`
		ContentKind  string `json:"ContentKind"`
		Error        string `json:"Error,omitempty"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		// Parse form data
		if err := r.ParseForm(); err != nil {
			writeHTMLErrorDialog(w, fmt.Errorf("invalid form data: %w", err))
			return
		}

		requirements := r.FormValue("requirements")
		rewardPerPostStr := r.FormValue("reward_per_post")
		numberOfBountiesStr := r.FormValue("number_of_bounties")
		durationStr := r.FormValue("duration")

		// Basic validation
		if requirements == "" || rewardPerPostStr == "" || numberOfBountiesStr == "" {
			writeHTMLErrorDialog(w, fmt.Errorf("all fields are required"))
			return
		}

		// Parse reward per post
		rewardPerPost, err := strconv.ParseFloat(rewardPerPostStr, 64)
		if err != nil {
			writeHTMLErrorDialog(w, fmt.Errorf("invalid reward_per_post: %w", err))
			return
		}

		// Parse number of bounties
		numberOfBounties, err := strconv.Atoi(numberOfBountiesStr)
		if err != nil {
			writeHTMLErrorDialog(w, fmt.Errorf("invalid number_of_bounties: %w", err))
			return
		}

		// Calculate total bounty
		totalBounty := rewardPerPost * float64(numberOfBounties)

		// Parse duration (form sends days, convert to duration string)
		timeoutDuration := ""
		if durationStr != "" {
			days, err := strconv.Atoi(durationStr)
			if err != nil {
				writeHTMLErrorDialog(w, fmt.Errorf("invalid duration: %w", err))
				return
			}
			if days <= 0 {
				writeHTMLErrorDialog(w, fmt.Errorf("duration must be positive"))
				return
			}
			timeoutDuration = fmt.Sprintf("%dd", days)
		}

		// Convert requirements to array (split by newlines)
		requirementsLines := strings.Split(strings.TrimSpace(requirements), "\n")
		requirementsArray := make([]string, 0, len(requirementsLines))
		for _, line := range requirementsLines {
			trimmed := strings.TrimSpace(line)
			if trimmed != "" {
				requirementsArray = append(requirementsArray, trimmed)
			}
		}
		if len(requirementsArray) == 0 {
			writeHTMLErrorDialog(w, fmt.Errorf("requirements cannot be empty"))
			return
		}

		// Create CreateBountyRequest from form data
		req := CreateBountyRequest{
			Requirements:    requirementsArray,
			BountyPerPost:   rewardPerPost,
			TotalBounty:     totalBounty,
			TimeoutDuration: timeoutDuration,
		}

		// Now use the same validation and processing logic as handleCreateBounty
		// Validate request
		if len(req.Requirements) == 0 {
			writeHTMLErrorDialog(w, fmt.Errorf("requirements is required"))
			return
		}
		requirementsStr := strings.Join(req.Requirements, "\n")
		if requirementsStr == "" {
			writeHTMLErrorDialog(w, fmt.Errorf("requirements cannot be empty"))
			return
		}
		if req.BountyPerPost <= 0 {
			writeHTMLErrorDialog(w, fmt.Errorf("bounty_per_post must be greater than 0"))
			return
		}
		if req.TotalBounty <= 0 {
			writeHTMLErrorDialog(w, fmt.Errorf("total_bounty must be greater than 0"))
			return
		}

		// --- Content Moderation ---
		// Note: Form endpoint has no authentication, so we ALWAYS perform content moderation
		// Unauthenticated users cannot be trusted, so all content must be moderated
		{
			type contentModerationResponse struct {
				IsAcceptable bool   `json:"is_acceptable"`
				Reason       string `json:"reason,omitempty"`
				Error        string `json:"error,omitempty"`
			}
			schema := map[string]interface{}{
				"name":   "content_moderation",
				"strict": true,
				"schema": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"is_acceptable": map[string]interface{}{
							"type":        "boolean",
							"description": "Whether the content is acceptable or not.",
						},
						"reason": map[string]interface{}{
							"type":        "string",
							"description": "The reason why the content is not acceptable. Leave blank if it is acceptable.",
						},
						"error": map[string]interface{}{
							"type":        "string",
							"description": "An optional error message if moderation cannot be determined. Leave blank if content is acceptable.",
						},
					},
					"required":             []string{"is_acceptable", "reason", "error"},
					"additionalProperties": false,
				},
			}
			prompt := fmt.Sprintf(prompts.ContentModeration, requirementsStr)
			schemaJSON, err := json.Marshal(schema)
			if err != nil {
				logger.Error("Failed to marshal content moderation schema", "error", err)
				writeHTMLErrorDialog(w, fmt.Errorf("failed to process request: unable to validate content"))
				return
			}
			resp, err := llmProvider.GenerateResponse(r.Context(), "content_moderation", prompt, schemaJSON)
			if err != nil {
				logger.Error("Failed to moderate content", "error", err)
				writeHTMLErrorDialog(w, fmt.Errorf("failed to validate content: %w", err))
				return
			}
			var moderationResp contentModerationResponse
			if err := json.Unmarshal([]byte(resp), &moderationResp); err != nil {
				logger.Error("Failed to parse moderation response", "error", err)
				writeHTMLErrorDialog(w, fmt.Errorf("failed to process content validation response"))
				return
			}
			if moderationResp.Error != "" {
				logger.Warn("Could not moderate content", "error", moderationResp.Error)
				writeHTMLErrorDialog(w, fmt.Errorf("could not moderate content: %s", moderationResp.Error))
				return
			}
			if !moderationResp.IsAcceptable {
				writeHTMLErrorDialog(w, fmt.Errorf("content is not acceptable: %s", moderationResp.Reason))
				return
			}
		}
		// --- End Content Moderation ---

		// --- Tier Processing ---
		var bountyTier abb.BountyTier
		if req.Tier != "" {
			tier, valid := abb.FromString(req.Tier)
			if !valid {
				writeHTMLErrorDialog(w, fmt.Errorf("invalid tier specified: '%s'", req.Tier))
				return
			}
			bountyTier = tier
		} else {
			bountyTier = abb.DefaultBountyTier
		}
		// --- End Tier Processing ---

		// --- Title Processing ---
		bountyTitle := req.Title
		if bountyTitle == "" {
			type inferredTitleRequest struct {
				Title string `json:"title"`
				Error string `json:"error,omitempty"`
			}
			schema := map[string]interface{}{
				"name":   "infer_bounty_title",
				"strict": true,
				"schema": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"title": map[string]interface{}{
							"type":        "string",
							"description": "A concise, descriptive title for the bounty based on its requirements.",
						},
						"error": map[string]interface{}{
							"type":        "string",
							"description": "An optional error message if a title cannot be inferred. Leave blank if a title can be inferred.",
						},
					},
					"required":             []string{"title", "error"},
					"additionalProperties": false,
				},
			}
			schemaJSON, err := json.Marshal(schema)
			if err != nil {
				logger.Error("Failed to marshal infer bounty title schema", "error", err)
				writeHTMLErrorDialog(w, fmt.Errorf("failed to process request: unable to generate title"))
				return
			}
			resp, err := llmProvider.GenerateResponse(r.Context(), prompts.InferBountyTitle, requirementsStr, schemaJSON)
			if err != nil {
				logger.Error("Failed to infer bounty title", "error", err)
				writeHTMLErrorDialog(w, fmt.Errorf("failed to generate bounty title: %w", err))
				return
			}
			var inferredTitle inferredTitleRequest
			if err := json.Unmarshal([]byte(resp), &inferredTitle); err != nil {
				logger.Error("Failed to parse inferred title response", "error", err)
				writeHTMLErrorDialog(w, fmt.Errorf("failed to process title generation response"))
				return
			}
			if inferredTitle.Error != "" {
				logger.Warn("Could not infer title", "error", inferredTitle.Error, "title", inferredTitle.Title)
				writeHTMLErrorDialog(w, fmt.Errorf("could not infer title: %s", inferredTitle.Error))
				return
			}
			bountyTitle = inferredTitle.Title
		}
		// --- End Title Processing ---

		// --- Requirements Length Check ---
		if len(requirementsStr) > abb.MaxRequirementsCharsForLLMCheck {
			warnMsg := fmt.Sprintf("Total length of requirements exceeds maximum limit (%d > %d)", len(requirementsStr), abb.MaxRequirementsCharsForLLMCheck)
			logger.Warn(warnMsg)
			writeHTMLErrorDialog(w, fmt.Errorf("%s", warnMsg))
			return
		}
		// --- End Requirements Length Check ---

		// --- Infer PlatformKind and ContentKind using LLM ---
		validPlatformKinds := []abb.PlatformKind{
			abb.PlatformReddit,
			abb.PlatformYouTube,
			abb.PlatformTwitch,
			abb.PlatformHackerNews,
			abb.PlatformBluesky,
			abb.PlatformInstagram,
			abb.PlatformIncentivizeThis,
			abb.PlatformTripAdvisor,
			abb.PlatformSteam,
			abb.PlatformGitHub,
		}
		validContentKinds := []abb.ContentKind{
			abb.ContentKindPost,
			abb.ContentKindComment,
			abb.ContentKindVideo,
			abb.ContentKindClip,
			abb.ContentKindBounty,
			abb.ContentKindReview,
			abb.ContentKindDota2Chat,
			abb.ContentKindIssue,
		}

		// Convert valid kinds to string slices for the prompt
		validPlatformKindsStr := make([]string, len(validPlatformKinds))
		for i, pk := range validPlatformKinds {
			validPlatformKindsStr[i] = string(pk)
		}
		validContentKindsStr := make([]string, len(validContentKinds))
		for i, ck := range validContentKinds {
			validContentKindsStr[i] = string(ck)
		}

		// Define the JSON schema for the expected output
		schema := map[string]interface{}{
			"name":   "infer_content_parameters",
			"strict": true,
			"schema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"PlatformKind": map[string]interface{}{
						"type":        "string",
						"description": "The platform for the content (e.g., 'reddit', 'youtube').",
						"enum":        validPlatformKindsStr,
					},
					"ContentKind": map[string]interface{}{
						"type": "string",
						"description": `The kind of content that the bounty is for. This is platform dependent. The valid options are:
- Reddit: post, comment
- YouTube: video
- Twitch: video, clip
- Hacker News: post, comment
- Bluesky: post
- Instagram: post
- IncentivizeThis: bounty
- TripAdvisor: review
- Steam: dota2chat
- GitHub: issue`,
						"enum": validContentKindsStr,
					},
					"Error": map[string]interface{}{
						"type":        "string",
						"description": "Set only if determination is not possible.",
					},
				},
				"required":             []string{"PlatformKind", "ContentKind", "Error"},
				"additionalProperties": false,
			},
		}
		schemaJSON, err := json.Marshal(schema)
		if err != nil {
			logger.Error("Failed to marshal content parameters schema", "error", err)
			writeHTMLErrorDialog(w, fmt.Errorf("failed to process request: unable to determine content type"))
			return
		}
		resp, err := llmProvider.GenerateResponse(r.Context(), prompts.InferContentParams, requirementsStr, schemaJSON)
		if err != nil {
			logger.Error("Failed to infer content parameters", "error", err)
			writeHTMLErrorDialog(w, fmt.Errorf("failed to determine content type: %w", err))
			return
		}
		var inferredParams inferredContentParamsRequest
		if err := json.Unmarshal([]byte(resp), &inferredParams); err != nil {
			logger.Error("Failed to parse inferred content parameters", "error", err)
			writeHTMLErrorDialog(w, fmt.Errorf("failed to process content type determination response"))
			return
		}

		if inferredParams.Error != "" {
			writeHTMLErrorDialog(w, fmt.Errorf("failed to infer content parameters: %s", inferredParams.Error))
			return
		}

		// Validate inferred PlatformKind
		normalizedPlatform := abb.PlatformKind(strings.ToLower(inferredParams.PlatformKind))
		validPlatformFound := false
		for _, vp := range validPlatformKinds {
			if normalizedPlatform == vp {
				validPlatformFound = true
				break
			}
		}
		if !validPlatformFound {
			errMsg := fmt.Sprintf("LLM inferred an unsupported PlatformKind: '%s'. Supported kinds are: %s", inferredParams.PlatformKind, strings.Join(validPlatformKindsStr, ", "))
			logger.Warn(errMsg)
			writeHTMLErrorDialog(w, fmt.Errorf("%s", errMsg))
			return
		}

		// Validate inferred ContentKind
		normalizedContentKind := abb.ContentKind(strings.ToLower(inferredParams.ContentKind))
		validContentFound := false
		for _, vc := range validContentKinds {
			if normalizedContentKind == vc {
				validContentFound = true
				break
			}
		}
		if !validContentFound {
			errMsg := fmt.Sprintf("LLM inferred an unsupported ContentKind: '%s'. Supported kinds are: %s", inferredParams.ContentKind, strings.Join(validContentKindsStr, ", "))
			logger.Warn(errMsg)
			writeHTMLErrorDialog(w, fmt.Errorf("%s", errMsg))
			return
		}

		// Validate platform type and content kind using normalized values
		switch normalizedPlatform {
		case abb.PlatformReddit:
			if normalizedContentKind != abb.ContentKindPost && normalizedContentKind != abb.ContentKindComment {
				writeHTMLErrorDialog(w, fmt.Errorf("invalid content_kind for Reddit: must be '%s' or '%s'", abb.ContentKindPost, abb.ContentKindComment))
				return
			}
		case abb.PlatformYouTube:
			if normalizedContentKind != abb.ContentKindVideo && normalizedContentKind != abb.ContentKindComment {
				writeHTMLErrorDialog(w, fmt.Errorf("invalid content_kind for YouTube: must be '%s' or '%s'", abb.ContentKindVideo, abb.ContentKindComment))
				return
			}
		case abb.PlatformTwitch:
			if normalizedContentKind != abb.ContentKindVideo && normalizedContentKind != abb.ContentKindClip {
				writeHTMLErrorDialog(w, fmt.Errorf("invalid content_kind for Twitch: must be '%s' or '%s'", abb.ContentKindVideo, abb.ContentKindClip))
				return
			}
		case abb.PlatformHackerNews:
			if normalizedContentKind != abb.ContentKindPost && normalizedContentKind != abb.ContentKindComment {
				writeHTMLErrorDialog(w, fmt.Errorf("invalid content_kind for Hacker News: must be '%s' or '%s'", abb.ContentKindPost, abb.ContentKindComment))
				return
			}
		case abb.PlatformBluesky:
			if normalizedContentKind != abb.ContentKindPost {
				writeHTMLErrorDialog(w, fmt.Errorf("invalid content_kind for Bluesky: must be '%s'", abb.ContentKindPost))
				return
			}
		case abb.PlatformInstagram:
			if normalizedContentKind != abb.ContentKindPost {
				writeHTMLErrorDialog(w, fmt.Errorf("invalid content_kind for Instagram: must be '%s' (not '%s')", abb.ContentKindPost, normalizedContentKind))
				return
			}
		case abb.PlatformIncentivizeThis:
			if normalizedContentKind != abb.ContentKindBounty {
				writeHTMLErrorDialog(w, fmt.Errorf("invalid content_kind for IncentivizeThis: must be '%s'", abb.ContentKindBounty))
				return
			}
		case abb.PlatformTripAdvisor:
			if normalizedContentKind != abb.ContentKindReview {
				writeHTMLErrorDialog(w, fmt.Errorf("invalid content_kind for TripAdvisor: must be '%s'", abb.ContentKindReview))
				return
			}
		case abb.PlatformSteam:
			if normalizedContentKind != abb.ContentKindDota2Chat {
				writeHTMLErrorDialog(w, fmt.Errorf("invalid content_kind for Steam: must be '%s'", abb.ContentKindDota2Chat))
				return
			}
		case abb.PlatformGitHub:
			if normalizedContentKind != abb.ContentKindIssue {
				writeHTMLErrorDialog(w, fmt.Errorf("invalid content_kind for GitHub: must be '%s'", abb.ContentKindIssue))
				return
			}
		default:
			writeHTMLErrorDialog(w, fmt.Errorf("invalid platform_kind: must be one of %s, %s, %s, %s, %s, %s, %s, %s, %s, or %s", abb.PlatformReddit, abb.PlatformYouTube, abb.PlatformTwitch, abb.PlatformHackerNews, abb.PlatformBluesky, abb.PlatformInstagram, abb.PlatformIncentivizeThis, abb.PlatformTripAdvisor, abb.PlatformSteam, abb.PlatformGitHub))
			return
		}

		// Read and validate overall bounty timeout
		bountyTimeoutDuration := 7 * 24 * time.Hour
		if req.TimeoutDuration != "" {
			parsedDurationString := req.TimeoutDuration
			if strings.HasSuffix(strings.ToLower(req.TimeoutDuration), "d") {
				daysStr := strings.TrimSuffix(strings.ToLower(req.TimeoutDuration), "d")
				days, err := strconv.Atoi(daysStr)
				if err != nil {
					writeHTMLErrorDialog(w, fmt.Errorf("invalid day value in timeout_duration '%s': %w", req.TimeoutDuration, err))
					return
				}
				if days <= 0 {
					writeHTMLErrorDialog(w, fmt.Errorf("day value in timeout_duration '%s' must be positive", req.TimeoutDuration))
					return
				}
				hours := days * 24
				parsedDurationString = fmt.Sprintf("%dh", hours)
				logger.Info("Converted day-based duration to hours", "original_duration", req.TimeoutDuration, "converted_duration", parsedDurationString)
			}

			duration, err := time.ParseDuration(parsedDurationString)
			if err != nil {
				writeHTMLErrorDialog(w, fmt.Errorf("invalid timeout_duration format '%s' (parsed as '%s'): %w", req.TimeoutDuration, parsedDurationString, err))
				return
			}
			bountyTimeoutDuration = duration
		}

		// Validate minimum duration
		if bountyTimeoutDuration < 24*time.Hour {
			writeHTMLErrorDialog(w, fmt.Errorf("timeout_duration must be at least 24 hours (e.g., \"24h\")"))
			return
		}

		// Max Payouts Per User
		// Note: Form endpoint has no auth, so custom max payouts per user is not supported
		// Use default value from environment variable
		maxPayoutsPerUser := defaultMaxPayoutsPerUser

		// Validate amounts
		if req.BountyPerPost <= 0 {
			writeHTMLErrorDialog(w, fmt.Errorf("bounty_per_post must be greater than 0"))
			return
		}
		if req.TotalBounty <= 0 {
			writeHTMLErrorDialog(w, fmt.Errorf("total_bounty must be greater than 0"))
			return
		}
		if req.BountyPerPost > req.TotalBounty {
			writeHTMLErrorDialog(w, fmt.Errorf("bounty_per_post (%.6f) cannot be greater than total_bounty (%.6f)", req.BountyPerPost, req.TotalBounty))
			return
		}

		// Convert amounts to USDCAmount
		bountyPerPostAmount, err := solana.NewUSDCAmount(req.BountyPerPost)
		if err != nil {
			writeHTMLErrorDialog(w, fmt.Errorf("invalid bounty_per_post amount: %w", err))
			return
		}
		totalBountyAmount, err := solana.NewUSDCAmount(req.TotalBounty)
		if err != nil {
			writeHTMLErrorDialog(w, fmt.Errorf("invalid total_bounty amount: %w", err))
			return
		}

		// Calculate total amount user must pay (bounty subtotal + platform fee)
		// If platform fee is 100%, and bounty subtotal is 0.06, total charged is 0.06 + 0.06 = 0.12
		// Formula: totalCharged = subtotal * (1 + platformFeePercent / 100)
		totalChargedAmount := req.TotalBounty * (1 + platformFeePercent/100)
		totalCharged, err := solana.NewUSDCAmount(totalChargedAmount)
		if err != nil {
			writeHTMLErrorDialog(w, fmt.Errorf("invalid total charged amount: %w", err))
			return
		}

		// Create workflow input
		input := abb.BountyAssessmentWorkflowInput{
			Title:             bountyTitle,
			Requirements:      req.Requirements,
			BountyPerPost:     bountyPerPostAmount,
			TotalBounty:       totalBountyAmount,
			TotalCharged:      totalCharged,
			Platform:          normalizedPlatform,
			ContentKind:       normalizedContentKind,
			Tier:              bountyTier,
			Timeout:           bountyTimeoutDuration,
			PaymentTimeout:    10 * time.Minute,
			TreasuryWallet:    os.Getenv(EnvSolanaTreasuryWallet),
			EscrowWallet:      os.Getenv(EnvSolanaEscrowWallet),
			MaxPayoutsPerUser: maxPayoutsPerUser,
		}

		// Execute workflow
		workflowID := fmt.Sprintf("bounty-%s", uuid.New().String())
		now := time.Now().UTC()

		// Create typed search attributes using defined keys
		saMap := temporal.NewSearchAttributes(
			abb.EnvironmentKey.ValueSet(env),
			abb.BountyPlatformKey.ValueSet(string(input.Platform)),
			abb.BountyContentKindKey.ValueSet(string(input.ContentKind)),
			abb.BountyTierKey.ValueSet(int64(input.Tier)),
			abb.BountyTotalAmountKey.ValueSet(input.TotalBounty.ToUSDC()),
			abb.BountyPerPostAmountKey.ValueSet(input.BountyPerPost.ToUSDC()),
			abb.BountyCreationTimeKey.ValueSet(now),
			abb.BountyTimeoutTimeKey.ValueSet(now.Add(input.Timeout)),
			abb.BountyStatusKey.ValueSet(string(abb.BountyStatusAwaitingFunding)),
			abb.BountyValueRemainingKey.ValueSet(input.TotalBounty.ToUSDC()),
		)

		workflowOptions := client.StartWorkflowOptions{
			ID:                    workflowID,
			TaskQueue:             os.Getenv(EnvTaskQueue),
			TypedSearchAttributes: saMap,
		}

		_, err = tc.ExecuteWorkflow(r.Context(), workflowOptions, abb.BountyAssessmentWorkflow, input)
		if err != nil {
			logger.Error("Failed to start bounty workflow", "error", err, "workflow_id", workflowID)
			writeHTMLErrorDialog(w, fmt.Errorf("failed to create bounty: %w", err))
			return
		}

		// Generate payment invoice with QR code
		paymentInvoice, err := generatePaymentInvoice(
			escrowWallet,
			usdcMint,
			input.TotalCharged.ToUSDC(),
			workflowID,
			input.PaymentTimeout,
		)
		if err != nil {
			logger.Error("Failed to generate payment invoice", "error", err, "bounty_id", workflowID)
			// Still return success, but without QR code
			w.Header().Set("HX-Trigger", "bountyCreated")
			fmt.Fprintf(w, `<div class="text-green-500">Bounty created successfully! ID: %s (QR code generation failed)</div>`, workflowID)
			return
		}

		// Parse and render QR code template
		tmpl, err := template.ParseFS(getTemplateFS(), "templates/partials/funding_qr.html")
		if err != nil {
			logger.Error("Failed to parse QR code template", "error", err)
			// Fallback to simple success message
			w.Header().Set("HX-Trigger", "bountyCreated")
			fmt.Fprintf(w, `<div class="text-green-500">Bounty created successfully! ID: %s</div>`, workflowID)
			return
		}

		// Render QR code template with payment invoice data
		data := map[string]interface{}{
			"PaymentInvoice": paymentInvoice,
			"BountyID":       workflowID,
		}
		w.Header().Set("HX-Trigger", "bountyCreated")
		if err := tmpl.ExecuteTemplate(w, "funding-qr", data); err != nil {
			logger.Error("Failed to execute QR code template", "error", err)
			// Fallback to simple success message
			fmt.Fprintf(w, `<div class="text-green-500">Bounty created successfully! ID: %s</div>`, workflowID)
			return
		}
	}
}

// handleBountyListPartial returns just the bounty list HTML fragment for HTMX polling
func handleBountyListPartial(logger *slog.Logger, tc client.Client, env string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		// Build query for running bounty workflows
		query := fmt.Sprintf("WorkflowType = '%s' AND ExecutionStatus = 'Running' AND %s = '%s'",
			"BountyAssessmentWorkflow",
			abb.EnvironmentKey.GetName(),
			env)

		// List workflows
		listResp, err := tc.ListWorkflow(ctx, &workflowservice.ListWorkflowExecutionsRequest{
			Query: query,
		})
		if err != nil {
			logger.Error("failed to list workflows", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		bounties := make([]map[string]interface{}, 0)
		for _, execution := range listResp.Executions {
			// Query each workflow for its details to get title and other info
			detailsResp, err := tc.QueryWorkflow(ctx, execution.Execution.WorkflowId, "", abb.GetBountyDetailsQueryType)
			if err != nil {
				logger.Warn("failed to query workflow details", "workflow_id", execution.Execution.WorkflowId, "error", err)
				continue
			}

			var details abb.BountyDetails
			if err := detailsResp.Get(&details); err != nil {
				logger.Warn("failed to decode workflow details", "workflow_id", execution.Execution.WorkflowId, "error", err)
				continue
			}

			bounty := map[string]interface{}{
				"BountyID":                details.BountyID,
				"Title":                   details.Title,
				"Status":                  string(details.Status),
				"BountyPerPost":           details.BountyPerPost,
				"PlatformKind":            string(details.PlatformKind),
				"ContentKind":             string(details.ContentKind),
				"Remaining":               calculateRemaining(details),
				"TotalCharged":            details.TotalCharged,
				"PaymentTimeoutExpiresAt": details.PaymentTimeoutExpiresAt,
			}

			bounties = append(bounties, bounty)
		}

		// Parse partial template
		tmpl, err := template.ParseFS(getTemplateFS(), "templates/partials/bounty_list.html")
		if err != nil {
			logger.Error("failed to parse partial template", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		data := map[string]interface{}{
			"Bounties": bounties,
		}

		if err := tmpl.ExecuteTemplate(w, "bounty-list", data); err != nil {
			logger.Error("failed to execute partial template", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
	}
}

// handleGetBountyFundingQRHTML handles fetching the payment QR code HTML for a bounty awaiting funding
func handleGetBountyFundingQRHTML(
	logger *slog.Logger,
	tc client.Client,
	escrowWallet solanago.PublicKey,
	usdcMint solanago.PublicKey,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bountyID := r.PathValue("bounty_id")
		if bountyID == "" {
			writeHTMLBadRequestError(w, fmt.Errorf("missing bounty ID in path"))
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()

		// Query workflow for bounty details
		resp, err := tc.QueryWorkflow(ctx, bountyID, "", abb.GetBountyDetailsQueryType)
		if err != nil {
			var notFoundErr *serviceerror.NotFound
			if errors.As(err, &notFoundErr) {
				writeHTMLBadRequestError(w, fmt.Errorf("bounty not found"))
			} else {
				writeHTMLInternalError(logger, w, fmt.Errorf("failed to query workflow %s for details: %w", bountyID, err))
			}
			return
		}

		var bountyDetails abb.BountyDetails
		if err := resp.Get(&bountyDetails); err != nil {
			writeHTMLInternalError(logger, w, fmt.Errorf("failed to decode bounty details: %w", err))
			return
		}

		// Only allow QR code generation for bounties awaiting funding
		if bountyDetails.Status != abb.BountyStatusAwaitingFunding {
			writeHTMLBadRequestError(w, fmt.Errorf("bounty is not awaiting funding (status: %s)", bountyDetails.Status))
			return
		}

		// Calculate payment timeout (default to 10 minutes if not set)
		paymentTimeout := 10 * time.Minute
		if bountyDetails.PaymentTimeoutExpiresAt != nil {
			now := time.Now().UTC()
			if bountyDetails.PaymentTimeoutExpiresAt.After(now) {
				paymentTimeout = bountyDetails.PaymentTimeoutExpiresAt.Sub(now)
			} else {
				writeHTMLBadRequestError(w, fmt.Errorf("payment timeout has expired"))
				return
			}
		}

		// Generate payment invoice with QR code
		paymentInvoice, err := generatePaymentInvoice(
			escrowWallet,
			usdcMint,
			bountyDetails.TotalCharged,
			bountyDetails.BountyID,
			paymentTimeout,
		)
		if err != nil {
			writeHTMLInternalError(logger, w, fmt.Errorf("failed to generate payment invoice: %w", err))
			return
		}

		// Parse and render QR code template
		tmpl, err := template.ParseFS(getTemplateFS(), "templates/partials/funding_qr.html")
		if err != nil {
			writeHTMLInternalError(logger, w, fmt.Errorf("failed to parse QR code template: %w", err))
			return
		}

		// Render QR code template with payment invoice data
		data := map[string]interface{}{
			"PaymentInvoice": paymentInvoice,
			"BountyID":       bountyID,
		}
		if err := tmpl.ExecuteTemplate(w, "funding-qr", data); err != nil {
			writeHTMLInternalError(logger, w, fmt.Errorf("failed to execute QR code template: %w", err))
			return
		}
	}
}

// handleGetBountyDetailHeaderHTML returns the bounty detail header HTML for polling updates
func handleGetBountyDetailHeaderHTML(
	logger *slog.Logger,
	tc client.Client,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bountyID := r.PathValue("bounty_id")
		if bountyID == "" {
			writeHTMLBadRequestError(w, fmt.Errorf("missing bounty ID in path"))
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()

		// Query workflow for bounty details
		resp, err := tc.QueryWorkflow(ctx, bountyID, "", abb.GetBountyDetailsQueryType)
		if err != nil {
			var notFoundErr *serviceerror.NotFound
			if errors.As(err, &notFoundErr) {
				writeHTMLBadRequestError(w, fmt.Errorf("bounty not found"))
			} else {
				writeHTMLInternalError(logger, w, fmt.Errorf("failed to query workflow %s for details: %w", bountyID, err))
			}
			return
		}

		var bountyDetails abb.BountyDetails
		if err := resp.Get(&bountyDetails); err != nil {
			writeHTMLInternalError(logger, w, fmt.Errorf("failed to decode bounty details: %w", err))
			return
		}

		// Convert to template-friendly format
		status := string(bountyDetails.Status)

		// Check if AwaitingFunding bounty has expired payment timeout (HATEOAS)
		if bountyDetails.Status == abb.BountyStatusAwaitingFunding && bountyDetails.PaymentTimeoutExpiresAt != nil {
			if time.Now().After(*bountyDetails.PaymentTimeoutExpiresAt) {
				// Override status to indicate funding expired
				status = "FundingExpired"
				logger.Debug("bounty funding has expired", "bounty_id", bountyDetails.BountyID, "expires_at", *bountyDetails.PaymentTimeoutExpiresAt)
			}
		}

		bounty := map[string]interface{}{
			"id":              bountyDetails.BountyID,
			"title":           bountyDetails.Title,
			"status":          status,
			"requirements":    strings.Join(bountyDetails.Requirements, "\n"),
			"reward_per_post": bountyDetails.BountyPerPost,
			"platform":        string(bountyDetails.PlatformKind),
			"content_type":    string(bountyDetails.ContentKind),
			"remaining":       calculateRemaining(bountyDetails),
			"created_at":      bountyDetails.CreatedAt,
			"end_at":          bountyDetails.EndAt,
			"days_remaining":  calculateDaysRemaining(bountyDetails.EndAt),
			"time_remaining":  formatTimeRemaining(bountyDetails.EndAt),
		}

		// Parse and render partial template
		tmpl, err := template.ParseFS(getTemplateFS(), "templates/partials/bounty_detail_header.html")
		if err != nil {
			writeHTMLInternalError(logger, w, fmt.Errorf("failed to parse bounty detail header template: %w", err))
			return
		}

		// Render template
		data := map[string]interface{}{
			"Bounty": bounty,
		}
		if err := tmpl.ExecuteTemplate(w, "bounty-detail-header", data); err != nil {
			writeHTMLInternalError(logger, w, fmt.Errorf("failed to execute bounty detail header template: %w", err))
			return
		}
	}
}

// handleGetCreateBountyFormHTML returns the create bounty form HTML
func handleGetCreateBountyFormHTML(logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tmpl, err := parseTemplates()
		if err != nil {
			writeHTMLInternalError(logger, w, fmt.Errorf("failed to parse templates: %w", err))
			return
		}

		if err := tmpl.ExecuteTemplate(w, "create-bounty-form", nil); err != nil {
			writeHTMLInternalError(logger, w, fmt.Errorf("failed to execute create bounty form template: %w", err))
			return
		}
	}
}

// handleBountyDetail serves the bounty detail page
func handleBountyDetail(logger *slog.Logger, tc client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		bountyID := r.PathValue("id")

		if bountyID == "" {
			logger.Error("missing bounty ID in path")
			http.Error(w, "Bad Request", http.StatusBadRequest)
			return
		}

		// Fetch bounty details from Temporal workflow
		resp, err := tc.QueryWorkflow(ctx, bountyID, "", abb.GetBountyDetailsQueryType)
		if err != nil {
			logger.Error("failed to query bounty details", "bounty_id", bountyID, "error", err)
			http.Error(w, "Bounty Not Found", http.StatusNotFound)
			return
		}

		var bountyDetails abb.BountyDetails
		if err := resp.Get(&bountyDetails); err != nil {
			logger.Error("failed to decode bounty details", "bounty_id", bountyID, "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		// Fetch paid bounties for this workflow
		paidResp, err := tc.QueryWorkflow(ctx, bountyID, "", abb.GetPaidBountiesQueryType)
		if err != nil {
			logger.Warn("failed to query paid bounties", "bounty_id", bountyID, "error", err)
			// Continue without paid bounties - not critical
		}

		var paidBounties []abb.PayoutDetail
		if paidResp != nil {
			if err := paidResp.Get(&paidBounties); err != nil {
				logger.Warn("failed to decode paid bounties", "bounty_id", bountyID, "error", err)
				// Continue without paid bounties - not critical
			}
		}

		// Convert to template-friendly format
		status := string(bountyDetails.Status)

		// Check if AwaitingFunding bounty has expired payment timeout (HATEOAS)
		if bountyDetails.Status == abb.BountyStatusAwaitingFunding && bountyDetails.PaymentTimeoutExpiresAt != nil {
			if time.Now().After(*bountyDetails.PaymentTimeoutExpiresAt) {
				// Override status to indicate funding expired
				status = "FundingExpired"
				logger.Debug("bounty funding has expired", "bounty_id", bountyDetails.BountyID, "expires_at", *bountyDetails.PaymentTimeoutExpiresAt)
			}
		}

		bounty := map[string]interface{}{
			"id":              bountyDetails.BountyID,
			"title":           bountyDetails.Title,
			"status":          status,
			"requirements":    strings.Join(bountyDetails.Requirements, "\n"),
			"reward_per_post": bountyDetails.BountyPerPost,
			"platform":        string(bountyDetails.PlatformKind),
			"content_type":    string(bountyDetails.ContentKind),
			"remaining":       calculateRemaining(bountyDetails),
			"created_at":      bountyDetails.CreatedAt,
			"end_at":          bountyDetails.EndAt,
			"days_remaining":  calculateDaysRemaining(bountyDetails.EndAt),
			"time_remaining":  formatTimeRemaining(bountyDetails.EndAt),
		}

		// Convert paid bounties to template format
		paidBountiesData := make([]map[string]interface{}, 0, len(paidBounties))
		for _, pb := range paidBounties {
			paidBountiesData = append(paidBountiesData, map[string]interface{}{
				"content_id":    pb.ContentID,
				"payout_wallet": pb.PayoutWallet,
				"amount":        pb.Amount.ToUSDC(),
				"timestamp":     pb.Timestamp,
				"platform":      string(pb.Platform),
				"content_kind":  string(pb.ContentKind),
			})
		}

		// Parse templates
		tmpl, err := template.ParseFS(getTemplateFS(),
			"templates/layouts/base.html",
			"templates/pages/bounty_detail.html")
		if err != nil {
			logger.Error("failed to parse templates", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		data := map[string]interface{}{
			"Title":        bountyDetails.Title,
			"Bounty":       bounty,
			"PaidBounties": paidBountiesData,
		}

		// Execute the template
		err = tmpl.Execute(w, data)
		if err != nil {
			logger.Error("failed to execute template", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
	}
}

// calculateRemaining calculates how many bounties are remaining
func calculateRemaining(details abb.BountyDetails) int {
	if details.BountyPerPost <= 0 {
		return 0
	}
	remaining := int(details.RemainingBountyValue / details.BountyPerPost)
	if remaining < 0 {
		return 0
	}
	return remaining
}

// calculateDaysRemaining calculates the number of days until the bounty ends
func calculateDaysRemaining(endAt time.Time) int {
	if endAt.IsZero() {
		return 0
	}
	now := time.Now()
	duration := endAt.Sub(now)
	if duration < 0 {
		return 0
	}
	daysRemaining := duration.Hours() / 24
	return int(daysRemaining)
}

// formatTimeRemaining formats the time remaining in a human-readable format
func formatTimeRemaining(endAt time.Time) string {
	if endAt.IsZero() {
		return "No end date"
	}
	now := time.Now()
	duration := endAt.Sub(now)
	if duration < 0 {
		return "Expired"
	}

	hours := int(duration.Hours())
	if hours < 24 {
		return fmt.Sprintf("%d hours left", hours)
	}

	days := hours / 24
	return fmt.Sprintf("%d days left", days)
}

// handleStaticFiles serves embedded static files
func handleStaticFiles() http.HandlerFunc {
	// Create a sub-filesystem rooted at "static"
	staticFS, err := fs.Sub(getStaticFS(), "static")
	if err != nil {
		panic(err)
	}

	fileServer := http.FileServer(http.FS(staticFS))

	return func(w http.ResponseWriter, r *http.Request) {
		// Set cache headers for static assets
		w.Header().Set("Cache-Control", "public, max-age=31536000")
		fileServer.ServeHTTP(w, r)
	}
}

// handle404 serves the 404 error page
func handle404(logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logger.Debug("404 not found", "path", r.URL.Path, "method", r.Method)

		// Parse base and 404 templates together
		tmpl, err := template.ParseFS(getTemplateFS(),
			"templates/layouts/base.html",
			"templates/pages/404.html")
		if err != nil {
			logger.Error("failed to parse 404 template", "error", err)
			http.Error(w, "Page Not Found", http.StatusNotFound)
			return
		}

		data := map[string]interface{}{
			"Title": "Page Not Found",
		}

		w.WriteHeader(http.StatusNotFound)
		if err := tmpl.Execute(w, data); err != nil {
			logger.Error("failed to execute 404 template", "error", err)
			http.Error(w, "Page Not Found", http.StatusNotFound)
			return
		}
	}
}

// handleClaimBountyForm handles form submission for claiming a bounty
// It does the same thing as handleAssessContent but returns HTML dialog responses
func handleClaimBountyForm(logger *slog.Logger, tc client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Parse form data
		if err := r.ParseForm(); err != nil {
			writeHTMLErrorDialog(w, fmt.Errorf("invalid form data: %w", err))
			return
		}

		// Extract form values
		bountyID := r.FormValue("bounty_id")
		contentURL := r.FormValue("content_url")
		payoutWallet := r.FormValue("payout_wallet")
		platform := r.FormValue("platform")
		contentKind := r.FormValue("content_kind")

		// Validate required fields
		if bountyID == "" || contentURL == "" || payoutWallet == "" || platform == "" {
			writeHTMLErrorDialog(w, fmt.Errorf("all fields are required"))
			return
		}

		// Validate that PayoutWallet is a valid Solana address
		if _, err := solanago.PublicKeyFromBase58(payoutWallet); err != nil {
			writeHTMLErrorDialog(w, fmt.Errorf("invalid Solana wallet address"))
			return
		}

		// Normalize platform and content kind to lowercase for consistent handling
		normalizedPlatform := abb.PlatformKind(strings.ToLower(platform))
		normalizedContentKind := abb.ContentKind(strings.ToLower(contentKind))

		// Signal the workflow
		err := tc.SignalWorkflow(r.Context(), bountyID, "", abb.AssessmentSignalName, abb.AssessContentSignal{
			ContentID:    contentURL,
			PayoutWallet: payoutWallet,
			Platform:     normalizedPlatform,
			ContentKind:  normalizedContentKind,
		})
		if err != nil {
			logger.Error("failed to signal workflow", "error", err, "bounty_id", bountyID)
			writeHTMLErrorDialog(w, fmt.Errorf("failed to submit claim. Please try again"))
			return
		}

		// Return status stream dialog with workflow ID for SSE connection
		tmpl, parseErr := template.ParseFS(getTemplateFS(), "templates/partials/dialog_status_stream.html")
		if parseErr != nil {
			logger.Error("failed to parse status stream template", "error", parseErr)
			writeHTMLErrorDialog(w, fmt.Errorf("failed to initialize status updates"))
			return
		}

		data := map[string]interface{}{
			"WorkflowID": bountyID,
		}

		w.Header().Set("Content-Type", "text/html")
		if execErr := tmpl.ExecuteTemplate(w, "dialog-status-stream", data); execErr != nil {
			logger.Error("failed to execute status stream template", "error", execErr)
			writeHTMLErrorDialog(w, fmt.Errorf("failed to initialize status updates"))
		}
	}
}

// handleAssessmentStatusSSE streams workflow assessment status via Server-Sent Events
func handleAssessmentStatusSSE(logger *slog.Logger, tc client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workflowID := r.PathValue("id")
		if workflowID == "" {
			http.Error(w, "Missing workflow ID", http.StatusBadRequest)
			return
		}

		// Set SSE headers
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no") // Disable nginx buffering

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
			return
		}

		ctx := r.Context()
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		timeout := time.After(5 * time.Minute)

		// Send initial connection message
		fmt.Fprintf(w, "data: {\"message\": \"Connected\", \"current_step\": \"initializing\"}\n\n")
		flusher.Flush()

		for {
			select {
			case <-ctx.Done():
				// Client disconnected
				logger.Debug("SSE client disconnected", "workflow_id", workflowID)
				return

			case <-timeout:
				// Timeout after 5 minutes
				fmt.Fprintf(w, "data: {\"is_complete\": true, \"is_approved\": false, \"error_message\": \"Assessment timed out. Please check your bounty status later.\"}\n\n")
				flusher.Flush()
				return

			case <-ticker.C:
				// Query workflow for status
				var status map[string]interface{}
				resp, err := tc.QueryWorkflow(ctx, workflowID, "", "GetAssessmentStatus")
				if err != nil {
					logger.Debug("failed to query workflow", "error", err, "workflow_id", workflowID)
					// Don't fail immediately, workflow might not be ready yet
					continue
				}

				if err := resp.Get(&status); err != nil {
					logger.Error("failed to decode workflow status", "error", err, "workflow_id", workflowID)
					continue
				}

				// Send status update as SSE event
				statusJSON, _ := json.Marshal(status)
				fmt.Fprintf(w, "data: %s\n\n", statusJSON)
				flusher.Flush()

				// Check if assessment is complete
				if isComplete, ok := status["is_complete"].(bool); ok && isComplete {
					logger.Debug("assessment complete, closing SSE", "workflow_id", workflowID)
					return
				}
			}
		}
	}
}
