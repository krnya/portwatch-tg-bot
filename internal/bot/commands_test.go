package bot

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"portwatch/internal/config"
	"portwatch/internal/feed"
	"portwatch/internal/store"
	"portwatch/internal/telegram"
)

type recordingTelegram struct {
	reply string
}

func (*recordingTelegram) Updates(context.Context, int64) ([]telegram.Update, error) {
	return nil, nil
}

func (client *recordingTelegram) Send(_ context.Context, _, text string) (int64, error) {
	client.reply = text
	return 1, nil
}

func TestPublicSubscriptionsAndOwnerAuthorization(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	path := filepath.Join(dir, "watch.sqlite")
	configPath := filepath.Join(dir, "sources.yaml")
	contents := "sources: [{id: portwatch-vi, kind: portwatch, feed_url: 'https://original.example/news/feed.xml'}]"
	if err := os.WriteFile(configPath, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(ctx, path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	api := &recordingTelegram{}
	refresh := make(chan struct{}, 1)
	commands := Commands{Telegram: api, Store: db, OwnerID: 42, ConfigPath: configPath, Refresh: refresh,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	message := func(from, chat int64, kind, body string) {
		t.Helper()
		update := &telegram.Message{Text: body}
		update.From.ID, update.Chat.ID, update.Chat.Type = from, chat, kind
		api.reply = ""
		if err := commands.handle(ctx, update); err != nil {
			t.Fatal(err)
		}
	}
	assertSources := func(urls ...string) {
		t.Helper()
		sources, err := db.Sources(ctx, cfg.Sources)
		if err != nil || len(sources) != len(urls) {
			t.Fatalf("sources = %#v, error = %v", sources, err)
		}
		for index, url := range urls {
			if sources[index].FeedURL != url {
				t.Fatalf("sources = %#v, want %v", sources, urls)
			}
		}
	}
	message(7, 7, "private", "/start")
	message(7, 7, "private", "/subscribe")
	message(8, 8, "private", "/subscribe")
	message(7, 7, "private", "/setdomain portwatch-vi rejected.example")
	if !strings.Contains(api.reply, "владельцу") || len(refresh) != 0 {
		t.Fatalf("non-owner command: reply %q, refresh %d", api.reply, len(refresh))
	}
	message(42, -99, "group", "/removedomain portwatch-vi")
	assertSources("https://original.example/news/feed.xml")
	message(42, 42, "private", "/setdomain portwatch-vi mirror.example")
	if len(refresh) != 1 {
		t.Fatal("expected a refresh after a valid owner command")
	}
	message(42, 42, "private", "/adddomain second https://another.example/custom.xml")
	assertSources("https://mirror.example/news/feed.xml", "https://another.example/custom.xml")
	message(42, 42, "private", "/removedomain second")
	message(42, 42, "private", "/domains")
	if !strings.Contains(api.reply, "https://mirror.example/news/feed.xml") || strings.Contains(api.reply, "another.example") {
		t.Fatalf("unexpected /domains reply: %q", api.reply)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(ctx, path, "")
	if err != nil {
		t.Fatal(err)
	}
	db, commands.Store = reopened, reopened
	assertSources("https://mirror.example/news/feed.xml")
	if _, err := db.RecordProgress(ctx, "portwatch-vi", feed.Progress{Score: 725, Leader: "Leader", URL: "https://mirror.example/"}); err != nil {
		t.Fatal(err)
	}
	message(7, 7, "private", "/progress")
	if !strings.Contains(api.reply, "72.5%") || !strings.Contains(api.reply, "Лидер: Leader") {
		t.Fatalf("unexpected /progress reply: %q", api.reply)
	}
	if err := db.RecordPoll(ctx, "portwatch-vi", []feed.Article{{ID: "old"}}); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordPoll(ctx, "portwatch-vi", []feed.Article{{ID: "new"}}); err != nil {
		t.Fatal(err)
	}
	message(7, 7, "private", "/unsubscribe")
	message(9, 9, "private", "/start")
	item, err := db.OldestPending(ctx)
	if err != nil || item.ChatID != "8" || item.Article.ID != "new" {
		t.Fatalf("pending = %+v, error = %v", item, err)
	}
	if err := db.MarkSent(ctx, item.QueueID); err != nil {
		t.Fatal(err)
	}
	if item, err := db.OldestPending(ctx); err != nil || item.QueueID != 0 {
		t.Fatalf("extra pending = %+v, error = %v", item, err)
	}
}
