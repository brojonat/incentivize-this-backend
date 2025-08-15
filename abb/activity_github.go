package abb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/go-github/v63/github"
)

// GitHubDependencies holds dependencies for GitHub activities.
// No API key is needed for public repository access.
type GitHubDependencies struct{}

// Type returns the platform type for GitHub.
func (deps GitHubDependencies) Type() PlatformKind {
	return PlatformGitHub
}

// MarshalJSON implements json.Marshaler for GitHubDependencies.
func (deps GitHubDependencies) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct{}{})
}

// UnmarshalJSON implements json.Unmarshaler for GitHubDependencies.
func (deps *GitHubDependencies) UnmarshalJSON(data []byte) error {
	var aux struct{}
	if err := json.Unmarshal(data, &aux); err != nil {
		return fmt.Errorf("failed to unmarshal GitHubDependencies: %w", err)
	}
	return nil
}

// GitHubIssueContent represents the data extracted for a GitHub Issue.
type GitHubIssueContent struct {
	ID        int64  `json:"id"`
	URL       string `json:"url"`
	Title     string `json:"title"`
	State     string `json:"state"`
	Body      string `json:"body"`
	Author    string `json:"author"`
	ClosedBy  string `json:"closed_by,omitempty"`
	IsClosed  bool   `json:"is_closed"`
	IsLocked  bool   `json:"is_locked"`
	CreatedAt int64  `json:"created_at"`
	ClosedAt  int64  `json:"closed_at,omitempty"`
}

// GitHubPullRequestContent represents data for a GitHub Pull Request.
type GitHubPullRequestContent struct {
	ID        int64  `json:"id"`
	URL       string `json:"url"`
	State     string `json:"state"`
	Title     string `json:"title"`
	Author    string `json:"author"`
	IsMerged  bool   `json:"is_merged"`
	MergedBy  string `json:"merged_by,omitempty"`
	CreatedAt int64  `json:"created_at"`
	MergedAt  int64  `json:"merged_at,omitempty"`
}

// GitHubUserContent represents data for a GitHub User.
type GitHubUserContent struct {
	Login     string `json:"login"`
	ID        int64  `json:"id"`
	URL       string `json:"url"`
	Bio       string `json:"bio,omitempty"`
	Location  string `json:"location,omitempty"`
	Blog      string `json:"blog,omitempty"`
	CreatedAt int64  `json:"created_at"`
}

func (a *Activities) parseGitHubURL(contentID string) (owner, repo string, issueNum int, err error) {
	// Try parsing as a full URL first
	if strings.HasPrefix(contentID, "http://") || strings.HasPrefix(contentID, "https://") {
		parsedURL, err := url.Parse(contentID)
		if err != nil {
			return "", "", 0, fmt.Errorf("invalid URL: %w", err)
		}

		pathParts := strings.Split(strings.Trim(parsedURL.Path, "/"), "/")
		if len(pathParts) < 4 || pathParts[2] != "issues" {
			return "", "", 0, fmt.Errorf("URL format must be https://github.com/owner/repo/issues/number")
		}

		owner = pathParts[0]
		repo = pathParts[1]
		issueNum, err = strconv.Atoi(pathParts[3])
		if err != nil {
			return "", "", 0, fmt.Errorf("invalid issue number in URL: %w", err)
		}
	} else {
		// Assume owner/repo/issue_number format
		parts := strings.Split(contentID, "/")
		if len(parts) != 3 {
			return "", "", 0, fmt.Errorf("invalid content ID format: expected owner/repo/issue_number or full URL")
		}
		owner = parts[0]
		repo = parts[1]
		issueNum, err = strconv.Atoi(parts[2])
		if err != nil {
			return "", "", 0, fmt.Errorf("invalid issue number in shorthand: %w", err)
		}
	}

	return owner, repo, issueNum, nil
}

func (a *Activities) GetGitHubIssue(ctx context.Context, owner, repo string, issueNumber int) (*GitHubIssueContent, error) {
	client := github.NewClient(nil)
	issue, _, err := client.Issues.Get(ctx, owner, repo, issueNumber)
	if err != nil {
		return nil, fmt.Errorf("failed to get issue: %w", err)
	}

	content := &GitHubIssueContent{
		ID:        issue.GetID(),
		URL:       issue.GetHTMLURL(),
		Title:     issue.GetTitle(),
		State:     issue.GetState(),
		Body:      issue.GetBody(),
		Author:    issue.GetUser().GetLogin(),
		IsClosed:  issue.GetState() == "closed",
		IsLocked:  issue.GetLocked(),
		CreatedAt: issue.GetCreatedAt().Unix(),
	}

	if issue.ClosedBy != nil {
		content.ClosedBy = issue.GetClosedBy().GetLogin()
	}
	if issue.ClosedAt != nil {
		content.ClosedAt = issue.GetClosedAt().Unix()
	}

	return content, nil
}

func (a *Activities) GetClosingPullRequest(ctx context.Context, owner, repo string, issueNumber int) (*GitHubPullRequestContent, error) {
	client := github.NewClient(nil)
	// Paginate through all issue events to find the closing event, then
	// resolve the closing PR via the closing commit SHA.
	var closingPR *github.PullRequest
	listOpts := &github.ListOptions{PerPage: 100}
	for {
		events, resp, err := client.Issues.ListIssueEvents(ctx, owner, repo, issueNumber, listOpts)
		if err != nil {
			return nil, fmt.Errorf("failed to get issue events: %w", err)
		}

		for _, event := range events {
			if event.GetEvent() != "closed" {
				continue
			}
			// When an issue is closed by merging a PR, GitHub sets CommitID on the event.
			if sha := event.GetCommitID(); sha != "" {
				prs, _, err := client.PullRequests.ListPullRequestsWithCommit(ctx, owner, repo, sha, nil)
				if err != nil {
					continue
				}
				if len(prs) > 0 {
					// Prefer a merged PR if multiple are associated with the commit.
					for _, p := range prs {
						if p.GetMerged() {
							closingPR = p
							break
						}
					}
					if closingPR == nil {
						closingPR = prs[0]
					}
					break
				}
			}
		}

		if closingPR != nil || resp == nil || resp.NextPage == 0 {
			break
		}
		listOpts.Page = resp.NextPage
	}

	if closingPR == nil {
		// Fallback: use the issue timeline to find cross-referenced PRs (more reliable than events commit_id).
		// The timeline includes entries where a PR cross-references the issue; the PR that actually closed it
		// will appear here as well.
		tListOpts := &github.ListOptions{PerPage: 100}
		var candidatePRNumber int
		for {
			// Note: In recent GitHub API versions, timeline is generally available under the standard media type.
			// go-github exposes this via Issues.ListIssueTimeline.
			timeline, resp, err := client.Issues.ListIssueTimeline(ctx, owner, repo, issueNumber, tListOpts)
			if err != nil {
				break
			}
			for _, te := range timeline {
				if te.GetEvent() != "cross-referenced" || te.Source == nil || te.Source.Issue == nil {
					continue
				}
				iss := te.Source.Issue
				if iss.GetNumber() > 0 && iss.GetPullRequestLinks() != nil {
					candidatePRNumber = iss.GetNumber()
				}
			}
			if resp == nil || resp.NextPage == 0 {
				break
			}
			tListOpts.Page = resp.NextPage
		}

		if candidatePRNumber > 0 {
			pr, _, err := client.PullRequests.Get(ctx, owner, repo, candidatePRNumber)
			if err == nil {
				closingPR = pr
			}
		}
	}

	if closingPR == nil {
		return nil, fmt.Errorf("could not find a closing PR for issue #%d", issueNumber)
	}

	prContent := &GitHubPullRequestContent{
		ID:       closingPR.GetID(),
		URL:      closingPR.GetHTMLURL(),
		State:    closingPR.GetState(),
		Title:    closingPR.GetTitle(),
		Author:   closingPR.GetUser().GetLogin(),
		IsMerged: closingPR.GetMerged(),
		CreatedAt: func() int64 {
			if closingPR.CreatedAt != nil {
				return closingPR.GetCreatedAt().Unix()
			}
			return 0
		}(),
	}
	if closingPR.MergedBy != nil {
		prContent.MergedBy = closingPR.GetMergedBy().GetLogin()
	}
	if closingPR.MergedAt != nil {
		prContent.MergedAt = closingPR.GetMergedAt().Unix()
	}

	return prContent, nil
}

func (a *Activities) GetGitHubUser(ctx context.Context, username string) (*GitHubUserContent, error) {
	client := github.NewClient(nil)
	user, _, err := client.Users.Get(ctx, username)
	if err != nil {
		return nil, fmt.Errorf("failed to get user %s: %w", username, err)
	}

	return &GitHubUserContent{
		Login:     user.GetLogin(),
		ID:        user.GetID(),
		URL:       user.GetHTMLURL(),
		Bio:       user.GetBio(),
		Location:  user.GetLocation(),
		Blog:      user.GetBlog(),
		CreatedAt: user.GetCreatedAt().Unix(),
	}, nil
}
