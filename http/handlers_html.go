package http

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"

	"github.com/brojonat/affiliate-bounty-board/abb"
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
			"templates/pages/bounties.html")
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

// handleCreateBountyForm handles form submission for creating a bounty
func handleCreateBountyForm(logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Parse form data
		if err := r.ParseForm(); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, `<div class="text-red-500">Invalid form data</div>`)
			return
		}

		requirements := r.FormValue("requirements")
		platform := r.FormValue("platform")
		contentType := r.FormValue("content_type")
		rewardPerPost := r.FormValue("reward_per_post")
		numberOfBounties := r.FormValue("number_of_bounties")
		duration := r.FormValue("duration")

		// Basic validation
		if requirements == "" || platform == "" || contentType == "" || rewardPerPost == "" || numberOfBounties == "" {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, `<div class="text-red-500">All fields are required</div>`)
			return
		}

		// TODO: Make actual API call to create bounty
		// For now, return success message
		logger.Info("bounty form submitted",
			"requirements", requirements,
			"platform", platform,
			"content_type", contentType,
			"reward", rewardPerPost,
			"count", numberOfBounties,
			"duration", duration)

		// Return success response that will close modal and show message
		w.Header().Set("HX-Trigger", "bountyCreated")
		fmt.Fprintf(w, `<div class="text-green-500">Bounty created successfully!</div>`)
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
				"BountyID":      details.BountyID,
				"Title":         details.Title,
				"Status":        string(details.Status),
				"BountyPerPost": details.BountyPerPost,
				"PlatformKind":  string(details.PlatformKind),
				"ContentKind":   string(details.ContentKind),
				"Remaining":     calculateRemaining(details),
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
		bounty := map[string]interface{}{
			"id":              bountyDetails.BountyID,
			"title":           bountyDetails.Title,
			"status":          string(bountyDetails.Status),
			"requirements":    strings.Join(bountyDetails.Requirements, "\n"),
			"reward_per_post": bountyDetails.BountyPerPost,
			"platform":        string(bountyDetails.PlatformKind),
			"content_type":    string(bountyDetails.ContentKind),
			"remaining":       calculateRemaining(bountyDetails),
			"created_at":      bountyDetails.CreatedAt,
			"end_at":           bountyDetails.EndAt,
		"days_remaining":   calculateDaysRemaining(bountyDetails.EndAt),
		"time_remaining":   formatTimeRemaining(bountyDetails.EndAt),
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
