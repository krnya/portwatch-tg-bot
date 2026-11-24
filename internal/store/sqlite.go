package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	_ "modernc.org/sqlite"
	"portwatch/internal/feed"
)

const schema = `
CREATE TABLE IF NOT EXISTS sources (
    id TEXT PRIMARY KEY NOT NULL
);
CREATE TABLE IF NOT EXISTS articles (
    queue_id INTEGER PRIMARY KEY,
    source_id TEXT NOT NULL,
    article_id TEXT NOT NULL CHECK(length(article_id) > 0),
    title TEXT NOT NULL,
    summary TEXT NOT NULL,
    url TEXT NOT NULL,
    status TEXT NOT NULL CHECK(status IN ('baseline', 'pending', 'sent')),
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at INTEGER NOT NULL DEFAULT 0,
    message_id INTEGER,
    UNIQUE(source_id, article_id)
);
CREATE INDEX IF NOT EXISTS pending_articles ON articles(queue_id) WHERE status = 'pending';
`

type Store struct {
	db *sql.DB
}

type Pending struct {
	QueueID       int64
	SourceID      string
	Article       feed.Article
	Attempts      int
	NextAttemptAt time.Time
}

func Open(ctx context.Context, path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize SQLite: %w", err)
	}
	return &Store{db: db}, nil
}

func (store *Store) Close() error {
	return store.db.Close()
}

func (store *Store) RecordPoll(ctx context.Context, sourceID string, articles []feed.Article) error {
	ordered := slices.Clone(articles)
	slices.Reverse(ordered)
	slices.SortStableFunc(ordered, func(a, b feed.Article) int { return a.PublishedAt.Compare(b.PublishedAt) })

	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `INSERT INTO sources(id) VALUES (?) ON CONFLICT(id) DO NOTHING`, sourceID)
	if err != nil {
		return err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return err
	}
	status := "pending"
	if inserted != 0 {
		status = "baseline"
	}
	for _, article := range ordered {
		_, err := tx.ExecContext(ctx, `INSERT INTO articles(source_id, article_id, title, summary, url, status)
			VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(source_id, article_id) DO NOTHING`,
			sourceID, article.ID, article.Title, article.Summary, article.URL, status)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (store *Store) OldestPending(ctx context.Context) (Pending, error) {
	var item Pending
	var nextAttempt int64
	err := store.db.QueryRowContext(ctx, `SELECT queue_id, source_id, article_id, title, summary, url, attempts, next_attempt_at
		FROM articles WHERE status = 'pending' ORDER BY queue_id LIMIT 1`).Scan(&item.QueueID, &item.SourceID,
		&item.Article.ID, &item.Article.Title, &item.Article.Summary, &item.Article.URL, &item.Attempts, &nextAttempt)
	if errors.Is(err, sql.ErrNoRows) {
		return Pending{}, nil
	}
	item.NextAttemptAt = time.UnixMilli(nextAttempt)
	return item, err
}

func (store *Store) MarkSent(ctx context.Context, queueID, messageID int64) error {
	_, err := store.db.ExecContext(ctx, `UPDATE articles SET status = 'sent', message_id = ? WHERE queue_id = ?`, messageID, queueID)
	return err
}

func (store *Store) RetryAt(ctx context.Context, queueID int64, next time.Time) error {
	_, err := store.db.ExecContext(ctx, `UPDATE articles SET attempts = attempts + 1, next_attempt_at = ? WHERE queue_id = ?`, next.UnixMilli(), queueID)
	return err
}
