package http

import (
	"embed"
	"encoding/json"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
)

// Embed static assets into the binary
//
//go:embed static/images/*
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
		tmpl, err := parseTemplates()
		if err != nil {
			logger.Error("failed to parse templates", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		// Sample bounty data (to be replaced with real database queries)
		sampleBounties := []map[string]interface{}{
			{
				"id":              "bounty-1",
				"title":           "Create a Reddit post about our new AI feature",
				"status":          "Active",
				"funded":          true,
				"reward_per_post": 25.50,
				"platform":        "Reddit",
				"content_type":    "Post",
				"unlimited":       false,
				"remaining":       5,
			},
			{
				"id":              "bounty-2",
				"title":           "YouTube video reviewing our product",
				"status":          "Active",
				"funded":          true,
				"reward_per_post": 100.00,
				"platform":        "YouTube",
				"content_type":    "Video",
				"unlimited":       false,
				"remaining":       2,
			},
			{
				"id":              "bounty-3",
				"title":           "Tweet about our latest feature launch",
				"status":          "Unfunded",
				"funded":          false,
				"reward_per_post": 0,
				"platform":        "Twitter",
				"content_type":    "Post",
				"unlimited":       true,
				"remaining":       0,
			},
		}

		// Sample recently paid data
		recentlyPaid := []map[string]interface{}{
			{
				"signature": "sig1",
				"amount":    25.50,
				"timestamp": "2024-11-08T15:30:00Z",
			},
			{
				"signature": "sig2",
				"amount":    50.00,
				"timestamp": "2024-11-08T14:15:00Z",
			},
		}

		data := map[string]interface{}{
			"Title":        "Bounties",
			"Bounties":     toJSON(sampleBounties),
			"RecentlyPaid": toJSON(recentlyPaid),
		}

		// Execute the base layout with the bounties page
		err = tmpl.ExecuteTemplate(w, "templates/layouts/base.html", data)
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
