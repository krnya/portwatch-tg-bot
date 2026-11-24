package bot

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"portwatch/internal/telegram"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func TestCommitCommandFetchesOnDemandAndCaches(t *testing.T) {
	const sha = "abcdef0123456789abcdef0123456789abcdef01"
	requests := 0
	commits := NewGitHubCommits()
	commits.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.URL.Host != "api.github.com" || r.URL.Path != "/repos/4lwayz/portwatch-vi/commits" || r.URL.Query().Get("per_page") != "5" {
			t.Errorf("unexpected GitHub request: %s", r.URL)
		}
		body := `[{"sha":"` + sha + `","commit":{"message":"Update score\n\nExtended message"}}]`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	api := &recordingTelegram{}
	commands := Commands{Commits: commits, Telegram: api}
	update := &telegram.Message{Text: "/commits"}
	update.Chat.ID, update.Chat.Type, update.From.ID = 7, "private", 7
	for range 2 {
		if err := commands.handle(t.Context(), update); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(api.reply, "abcdef0 Update score") || !strings.Contains(api.reply, "https://github.com/4lwayz/portwatch-vi/commit/"+sha) ||
			strings.Contains(api.reply, "Extended message") {
			t.Fatalf("unexpected /commits reply: %q", api.reply)
		}
	}
	if requests != 1 {
		t.Fatalf("GitHub requests = %d, want 1", requests)
	}
}
