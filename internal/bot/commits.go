package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const upstreamRepository = "4lwayz/portwatch-vi"
const commitsPage = "https://github.com/" + upstreamRepository + "/commits/"

var commitSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

type GitHubCommits struct {
	http       *http.Client
	cached     string
	cachedErr  error
	validUntil time.Time
}

func NewGitHubCommits() *GitHubCommits {
	return &GitHubCommits{http: &http.Client{Timeout: 5 * time.Second}}
}

func (client *GitHubCommits) Recent(ctx context.Context) (string, error) {
	if time.Now().Before(client.validUntil) {
		return client.cached, client.cachedErr
	}
	client.cached, client.cachedErr = client.fetch(ctx)
	client.validUntil = time.Now().Add(2 * time.Minute)
	return client.cached, client.cachedErr
}

func (client *GitHubCommits) fetch(ctx context.Context) (string, error) {
	endpoint := "https://api.github.com/repos/" + upstreamRepository + "/commits?per_page=5"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "PortWatchBot/1.0")
	response, err := client.http.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub commits HTTP status %d", response.StatusCode)
	}
	var commits []struct {
		SHA    string `json:"sha"`
		Commit struct {
			Message string `json:"message"`
		} `json:"commit"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 512<<10)).Decode(&commits); err != nil {
		return "", err
	}
	var reply strings.Builder
	reply.WriteString("Последние коммиты " + upstreamRepository + ":")
	for _, commit := range commits[:min(len(commits), 5)] {
		if !commitSHA.MatchString(commit.SHA) {
			continue
		}
		title, _, _ := strings.Cut(commit.Commit.Message, "\n")
		title = strings.TrimSpace(title)
		runes := []rune(title)
		if len(runes) > 160 {
			title = string(runes[:160]) + "…"
		}
		fmt.Fprintf(&reply, "\n\n%s %s\nhttps://github.com/%s/commit/%s", commit.SHA[:7], title, upstreamRepository, commit.SHA)
	}
	return reply.String(), nil
}
