package store

import (
	"path/filepath"
	"testing"
	"time"

	"portwatch/internal/feed"
)

func TestRecordProgressSurvivesRestart(t *testing.T) {
	ctx := t.Context()
	filename := filepath.Join(t.TempDir(), "watch.sqlite")
	db, err := Open(ctx, filename, "@channel")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	if err := db.Subscribe(ctx, "7"); err != nil {
		t.Fatal(err)
	}
	snapshot := feed.Progress{Score: 670, Leader: "Old", URL: "https://old.example.org/", UpdatedAt: time.Now()}
	record := func(progress feed.Progress, wantChange bool) {
		t.Helper()
		if changed, err := db.RecordProgress(ctx, "portwatch", progress); err != nil || changed != wantChange {
			t.Fatalf("recording %s: changed = %v, error = %v", progress.Percentage(), changed, err)
		}
	}
	record(snapshot, false)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, filename, "@channel")
	if err != nil {
		t.Fatal(err)
	}
	db = reopened

	progress := feed.Progress{Score: 725, Leader: "Leader", URL: "https://new.example.org/", UpdatedAt: snapshot.UpdatedAt.Add(time.Hour)}
	record(progress, true)
	record(progress, false)
	record(snapshot, false)
	for _, chat := range []string{"@channel", "7"} {
		item, err := db.OldestPending(ctx)
		if err != nil || item.ChatID != chat || item.Article.Title != "Прогресс PortWatch VI: 67% → 72.5%" ||
			item.Article.Summary != "Лидер: Leader" || item.Article.URL != progress.URL {
			t.Fatalf("pending progress = %+v, error = %v", item, err)
		}
		if err := db.MarkSent(ctx, item.QueueID); err != nil {
			t.Fatal(err)
		}
	}
	if pending, err := db.OldestPending(ctx); err != nil || pending.QueueID != 0 {
		t.Fatalf("extra progress notification: %+v, error = %v", pending, err)
	}
	latest, found, err := db.Progress(ctx, "portwatch")
	if err != nil || !found || latest.Score != 725 || latest.URL != progress.URL {
		t.Fatalf("latest progress = %+v, found = %v, error = %v", latest, found, err)
	}
	snapshot.UpdatedAt = progress.UpdatedAt.Add(time.Hour)
	record(snapshot, true)
	progress.UpdatedAt = snapshot.UpdatedAt.Add(time.Hour)
	record(progress, true)
}
