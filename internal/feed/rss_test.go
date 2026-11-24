package feed

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"portwatch/internal/config"
)

func TestPortWatchFeed(t *testing.T) {
	fixture, err := os.ReadFile("testdata/portwatch.xml")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Write(fixture)
	}))
	defer server.Close()
	client := Client{http: server.Client()}
	articles, err := client.Fetch(t.Context(), config.Source{Kind: config.PortWatch, FeedURL: server.URL})
	if err != nil || len(articles) != 2 {
		t.Fatalf("articles = %#v, error = %v", articles, err)
	}
	first, second := articles[0], articles[1]
	if first.ID != "2026-11-24-second" || first.Title != "Second & latest story" || first.Summary != "Short <summary>." ||
		first.URL != "https://portwatch-vi.xyz/news/2026-11-24-second" || !first.PublishedAt.Equal(time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)) ||
		!first.PublishedAt.Equal(second.PublishedAt) || second.ID != "2026-11-24-first" {
		t.Fatalf("unexpected articles: %#v", articles)
	}
	base, _ := url.Parse(server.URL)
	movedFixture := strings.ReplaceAll(string(fixture), first.URL, "https://mirror.example/news/2026-11-24-second.html")
	moved, err := parse([]byte(movedFixture), config.PortWatch, base)
	if err != nil || len(moved) != 2 || moved[0].ID != first.ID || moved[0].URL == first.URL {
		t.Fatalf("moved articles = %#v, error = %v", moved, err)
	}
}

func TestRSSValidation(t *testing.T) {
	base, _ := url.Parse("https://example.com/feed.xml")
	empty, err := parse([]byte(`<rss version="2.0"><channel></channel></rss>`), config.RSS, base)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty feed = %#v, error = %v", empty, err)
	}
	for _, body := range []string{`<html></html>`, `<rss/>`, `<rss><channel>`, `<rss><channel><item/></channel></rss>`, `<rss><channel/></rss><html/>`} {
		if _, err := parse([]byte(body), config.RSS, base); err == nil {
			t.Errorf("accepted malformed feed %q", body)
		}
	}
	articles, err := parse([]byte(`<rss><channel><item><title>Test</title><link>/post</link></item></channel></rss>`), config.RSS, base)
	if err != nil || len(articles) != 1 || articles[0].ID != "https://example.com/post" {
		t.Fatalf("RSS fallback ID: articles = %#v, error = %v", articles, err)
	}
}

func TestFetchLimits(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/timeout" {
			<-req.Context().Done()
			return
		}
		io.WriteString(w, strings.Repeat("x", maxFeedBytes+1))
	}))
	defer server.Close()
	client := Client{http: server.Client()}
	if _, err := client.Fetch(t.Context(), config.Source{Kind: config.RSS, FeedURL: server.URL}); err == nil || !strings.Contains(err.Error(), "exceeds 2 MiB") {
		t.Fatalf("feed size error = %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, err := client.Fetch(ctx, config.Source{Kind: config.RSS, FeedURL: server.URL + "/timeout"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout error = %v", err)
	}
}
