package store

import (
	"path/filepath"
	"testing"
	"time"

	"portwatch/internal/feed"
)

func TestRecordPollAndReopen(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "watch.sqlite")
	db, err := Open(ctx, path)
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
	reopened, err := Open(ctx, path)
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
		if err := db.MarkSent(ctx, item.QueueID, 123); err != nil {
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
	db, err := Open(ctx, filepath.Join(t.TempDir(), "watch.sqlite"))
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
