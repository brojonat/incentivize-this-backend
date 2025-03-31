package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"github.com/jmespath/go-jmespath"
	"github.com/urfave/cli/v2"
)

func getDefaultLogger(lvl slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		AddSource: true,
		Level:     lvl,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.SourceKey {
				source, _ := a.Value.Any().(*slog.Source)
				if source != nil {
					source.Function = ""
					source.File = filepath.Base(source.File)
				}
			}
			return a
		},
	}))
}

func printServerResponse(res *http.Response) error {
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		return fmt.Errorf("could not read response body: %w", err)
	}
	var data interface{}
	if err := json.Unmarshal(b, &data); err != nil {
		return fmt.Errorf("error deserializing response: %w", err)
	}

	iface, err := jmespath.Search("error", data)
	if err != nil {
		return fmt.Errorf("error checking error field in response: %w", err)
	}
	if iface != nil && iface != "" {
		err_msg := iface.(string)
		return fmt.Errorf("received error from server (response code %d): %s", res.StatusCode, err_msg)
	}
	out := struct {
		Status int             `json:"status"`
		Body   json.RawMessage `json:"body"`
	}{
		Status: res.StatusCode,
		Body:   b,
	}
	b, err = json.Marshal(out)
	if err != nil {
		return fmt.Errorf("could not serialize output")
	}
	fmt.Printf("%s\n", b)
	return nil
}

func main() {
	// Configure structured logging
	logger := getDefaultLogger(slog.LevelInfo)

	app := &cli.App{
		Name:  "rbb",
		Usage: "Reddit Bounty Board CLI",
		Commands: []*cli.Command{
			{
				Name:        "run",
				Usage:       "Run a service",
				Subcommands: append(serverCommands(), workerCommands()...),
			},
			{
				Name:        "admin",
				Usage:       "Admin commands",
				Subcommands: adminCommands(),
			},
			{
				Name:        "debug",
				Usage:       "Debug and development tools",
				Subcommands: debugCommands(),
			},
		},
	}

	if err := app.Run(os.Args); err != nil {
		logger.Error("application error", "error", err)
		os.Exit(1)
	}
}
