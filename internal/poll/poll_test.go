package poll

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"portwatch/internal/config"
	"portwatch/internal/feed"
	"portwatch/internal/store"
	"portwatch/internal/telegram"
)

type fetchFunc func(context.Context, config.Source) ([]feed.Article, error)

func (fetch fetchFunc) Fetch(ctx context.Context, source config.Source) ([]feed.Article, error) {
	return fetch(ctx, source)
}

type sendFunc func(context.Context, string, string) (int64, error)

func (send sendFunc) Send(ctx context.Context, chat, text string) (int64, error) {
	return send(ctx, chat, text)
}

type progressFunc func(context.Context, config.Source) (feed.Progress, error)

func (fetch progressFunc) FetchProgress(ctx context.Context, source config.Source) (feed.Progress, error) {
	return fetch(ctx, source)
}

func TestProgressChangeWithUnavailableRSS(t *testing.T) {
	ctx := t.Context()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "watch.sqlite"), "@channel")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sources := config.Config{Sources: []config.Source{{ID: "portwatch", Kind: config.PortWatch}}}
	score := 670
	wake := make(chan struct{}, 1)
	poller := Poller{
		Store: db, Wake: wake, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Feed: fetchFunc(func(context.Context, config.Source) ([]feed.Article, error) {
			return nil, errors.New("RSS unavailable")
		}),
		Tracker: progressFunc(func(context.Context, config.Source) (feed.Progress, error) {
			return feed.Progress{Score: score}, nil
		}),
	}
	poller.poll(ctx, sources)
	if len(wake) != 0 {
		t.Fatal("first progress read unexpectedly woke delivery")
	}
	score = 725
	poller.poll(ctx, sources)
	item, err := db.OldestPending(ctx)
	if err != nil || len(wake) != 1 || item.Article.Title != "Прогресс PortWatch VI: 67% → 72.5%" {
		t.Fatalf("pending progress = %+v, wake = %d, error = %v", item, len(wake), err)
	}
}

func TestRunningBotReloadsSources(t *testing.T) {
	ctx, stop := context.WithCancel(t.Context())
	path := filepath.Join(t.TempDir(), "sources.yaml")
	write := func(contents string) {
		t.Helper()
		if err := os.WriteFile(path+".tmp", []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(path+".tmp", path); err != nil {
			t.Fatal(err)
		}
	}
	const first = "https://first.example/news/feed.xml"
	const moved = "https://moved.example/news/feed.xml"
	const broken = "https://unavailable.example/news/feed.xml"
	sources := func(address string) string {
		return "poll_interval: 20ms\nsources:\n" +
			"  - {id: unavailable, kind: rss, feed_url: '" + broken + "'}\n" +
			"  - {id: portwatch, kind: portwatch, feed_url: '" + address + "'}\n"
	}
	write(sources(first))
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "watch.sqlite"), "@channel")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	seen := make(chan string, 100)
	sent := make(chan string, 10)
	wake := make(chan struct{}, 1)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	poller := Poller{ConfigPath: path, Store: db, Logger: logger, Wake: wake, Feed: fetchFunc(func(_ context.Context, source config.Source) ([]feed.Article, error) {
		seen <- source.FeedURL
		if source.FeedURL == broken {
			return nil, errors.New("source unavailable")
		}
		articles := []feed.Article{{ID: "old", Title: "Already published", URL: strings.Replace(source.FeedURL, "feed.xml", "old", 1)}}
		if source.FeedURL == moved {
			articles = append([]feed.Article{{ID: "second", Title: "Second", URL: "https://moved.example/news/second"},
				{ID: "first", Title: "First", URL: "https://moved.example/news/first"}}, articles...)
		}
		return articles, nil
	})}
	delivery := Delivery{Store: db, Logger: logger, Wake: wake, Sender: sendFunc(func(_ context.Context, _, text string) (int64, error) {
		sent <- text
		return 99, nil
	})}
	pollerDone, deliveryDone := make(chan struct{}), make(chan struct{})
	go func() { defer close(pollerDone); poller.Run(ctx, cfg) }()
	go func() { defer close(deliveryDone); delivery.Run(ctx) }()
	defer func() { stop(); <-pollerDone; <-deliveryDone }()

	await := func(events <-chan string, expected string) {
		t.Helper()
		deadline := time.After(2 * time.Second)
		for {
			select {
			case actual := <-events:
				if actual == expected {
					return
				}
			case <-deadline:
				t.Fatalf("never saw %q", expected)
			}
		}
	}
	await(seen, broken)
	await(seen, first)
	write("sources: [")
	await(seen, first)
	write(sources(moved))
	await(seen, moved)
	for _, expected := range []string{"First\n\nhttps://moved.example/news/first", "Second\n\nhttps://moved.example/news/second"} {
		select {
		case actual := <-sent:
			if actual != expected {
				t.Fatalf("sent %q, expected %q", actual, expected)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("never sent %q", expected)
		}
	}
	await(seen, moved)
	write("poll_interval: 20ms\nsources: []\n")
	select {
	case extra := <-sent:
		t.Fatalf("sent extra message %q", extra)
	case <-time.After(80 * time.Millisecond):
	}
	for len(seen) > 0 {
		<-seen
	}
	select {
	case source := <-seen:
		t.Fatalf("polled removed source %q", source)
	case <-time.After(60 * time.Millisecond):
	}
}

func TestDeliveryPersistsRetryAndAcknowledgment(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "watch.sqlite")
	db, err := store.Open(ctx, path, "@channel")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	if err := db.RecordPoll(ctx, "news", nil); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordPoll(ctx, "news", []feed.Article{{ID: "new", Title: "New", URL: "https://example.com/news/new"}}); err != nil {
		t.Fatal(err)
	}
	item, err := db.OldestPending(ctx)
	if err != nil || item.QueueID == 0 {
		t.Fatalf("pending = %+v, error = %v", item, err)
	}
	worker := Delivery{Store: db, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Sender: sendFunc(func(context.Context, string, string) (int64, error) {
		return 0, &telegram.APIError{Code: 429, RetryAfter: time.Minute}
	})}
	started := time.Now()
	worker.deliver(ctx, item)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(ctx, path, "@channel")
	if err != nil {
		t.Fatal(err)
	}
	item, err = db.OldestPending(ctx)
	if err != nil || item.Attempts != 1 || item.NextAttemptAt.Before(started.Add(59*time.Second)) {
		t.Fatalf("retry after reopen = %+v, error = %v", item, err)
	}
	worker.Store = db
	worker.Sender = sendFunc(func(context.Context, string, string) (int64, error) { return 99, nil })
	worker.deliver(ctx, item)
	if item, err := db.OldestPending(ctx); err != nil || item.QueueID != 0 {
		t.Fatalf("after acknowledgment = %+v, error = %v", item, err)
	}
}

func TestMessageFitsAndKeepsLink(t *testing.T) {
	article := feed.Article{Title: strings.Repeat("🏝️", 1000), Summary: strings.Repeat("📰", 3000), URL: "https://example.com/news/new"}
	text := message(article)
	if len(utf16.Encode([]rune(text))) > 4096 || !strings.HasSuffix(text, "\n\n"+article.URL) {
		t.Fatal("message exceeded limit or lost the complete URL")
	}
}
