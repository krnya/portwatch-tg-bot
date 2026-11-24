package store

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"portwatch/internal/feed"
)

func TestMigratesLegacyDeliveries(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "watch.sqlite")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = legacy.ExecContext(ctx, `
		CREATE TABLE sources(id TEXT PRIMARY KEY NOT NULL);
		INSERT INTO sources VALUES ('portwatch');
		CREATE TABLE articles (
			queue_id INTEGER PRIMARY KEY, source_id TEXT NOT NULL, article_id TEXT NOT NULL CHECK(length(article_id) > 0),
			title TEXT NOT NULL, summary TEXT NOT NULL, url TEXT NOT NULL,
			status TEXT NOT NULL CHECK(status IN ('baseline', 'pending', 'sent')),
			attempts INTEGER NOT NULL DEFAULT 0, next_attempt_at INTEGER NOT NULL DEFAULT 0, message_id INTEGER,
			UNIQUE(source_id, article_id)
		);
		CREATE INDEX pending_articles ON articles(queue_id) WHERE status = 'pending';
		INSERT INTO articles VALUES (1, 'portwatch', 'old', 'Old', '', 'https://example.org/old', 'baseline', 0, 0, NULL);
		INSERT INTO articles VALUES (2, 'portwatch', 'sent', 'Sent', '', 'https://example.org/sent', 'sent', 0, 0, 123);
		INSERT INTO articles VALUES (3, 'portwatch', 'queued', 'Queued', '', 'https://example.org/queued', 'pending', 2, 60000, NULL);
	`)
	legacy.Close()
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		db, err := Open(ctx, path, "@channel")
		if err != nil {
			t.Fatal(err)
		}
		item, err := db.OldestPending(ctx)
		db.Close()
		if err != nil || item.ChatID != "@channel" || item.Article.ID != "queued" || item.Attempts != 2 || item.NextAttemptAt.UnixMilli() != 60000 {
			t.Fatalf("migrated delivery = %+v, error = %v", item, err)
		}
	}
	db, err := Open(ctx, path, "@channel")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var deliveries, articles int
	if err := db.db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM deliveries), (SELECT count(*) FROM articles)`).Scan(&deliveries, &articles); err != nil || deliveries != 1 || articles != 3 {
		t.Fatalf("after migration deliveries = %d, articles = %d, error = %v", deliveries, articles, err)
	}
}

func TestRecordPollAndReopen(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "watch.sqlite")
	db, err := Open(ctx, path, "@channel")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()

	baseline := feed.Article{ID: "old"}
	if err := db.RecordPoll(ctx, "portwatch", []feed.Article{baseline}); err != nil {
		t.Fatal(err)
	}
	if item, err := db.OldestPending(ctx); err != nil || item.QueueID != 0 {
		t.Fatalf("initial pending = %+v, error = %v", item, err)
	}
	date := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	batch := []feed.Article{{ID: "newest", PublishedAt: date}, {ID: "next", PublishedAt: date}, baseline}
	for range 2 {
		if err := db.RecordPoll(ctx, "portwatch", batch); err != nil {
			t.Fatal(err)
		}
	}
	item, err := db.OldestPending(ctx)
	if err != nil || item.Article.ID != "next" {
		t.Fatalf("next pending = %+v, error = %v", item, err)
	}
	nextAttempt := time.Now().Truncate(time.Millisecond).Add(time.Minute)
	if err := db.RetryAt(ctx, item.QueueID, nextAttempt); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path, "@channel")
	if err != nil {
		t.Fatal(err)
	}
	db = reopened
	item, err = db.OldestPending(ctx)
	if err != nil || item.Article.ID != "next" || item.Attempts != 1 || !item.NextAttemptAt.Equal(nextAttempt) {
		t.Fatalf("after reopen = %+v, error = %v", item, err)
	}
	for _, expected := range []string{"next", "newest"} {
		item, err := db.OldestPending(ctx)
		if err != nil || item.Article.ID != expected {
			t.Fatalf("expected %q, got %+v, error = %v", expected, item, err)
		}
		if err := db.MarkSent(ctx, item.QueueID); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.RecordPoll(ctx, "portwatch", batch); err != nil {
		t.Fatal(err)
	}
	if item, err := db.OldestPending(ctx); err != nil || item.QueueID != 0 {
		t.Fatalf("repeated pending = %+v, error = %v", item, err)
	}
}

func TestFirstPollIsAtomic(t *testing.T) {
	ctx := t.Context()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "watch.sqlite"), "@channel")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.RecordPoll(ctx, "portwatch", []feed.Article{{}, {ID: "valid"}}); err == nil {
		t.Fatal("expected constraint error")
	}
	if err := db.RecordPoll(ctx, "portwatch", []feed.Article{{ID: "after-error"}}); err != nil {
		t.Fatal(err)
	}
	if item, err := db.OldestPending(ctx); err != nil || item.QueueID != 0 {
		t.Fatalf("rolled back poll initialized source: %+v, error = %v", item, err)
	}
}

func TestSubscriberRetryDoesNotHoldOtherRecipients(t *testing.T) {
	ctx := t.Context()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "watch.sqlite"), "1")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, chat := range []string{"1", "2"} {
		if err := db.Subscribe(ctx, chat); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.RecordPoll(ctx, "news", nil); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordPoll(ctx, "news", []feed.Article{{ID: "later"}, {ID: "first"}}); err != nil {
		t.Fatal(err)
	}
	item, err := db.OldestPending(ctx)
	if err != nil || item.ChatID != "1" || item.Article.ID != "first" {
		t.Fatalf("first pending = %+v, error = %v", item, err)
	}
	if err := db.RetryAt(ctx, item.QueueID, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	for _, article := range []string{"first", "later"} {
		item, err := db.OldestPending(ctx)
		if err != nil || item.ChatID != "2" || item.Article.ID != article {
			t.Fatalf("pending while chat 1 retries = %+v, error = %v", item, err)
		}
		if err := db.MarkSent(ctx, item.QueueID); err != nil {
			t.Fatal(err)
		}
	}
	item, err = db.OldestPending(ctx)
	if err != nil || item.ChatID != "1" || item.Article.ID != "first" || item.Attempts != 1 {
		t.Fatalf("retry = %+v, error = %v", item, err)
	}
}
