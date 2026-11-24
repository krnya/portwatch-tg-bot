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
    UNIQUE(source_id, article_id)
);
CREATE TABLE IF NOT EXISTS subscribers (chat_id TEXT PRIMARY KEY NOT NULL);
CREATE TABLE IF NOT EXISTS deliveries (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    article_id INTEGER NOT NULL,
    chat_id TEXT NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at INTEGER NOT NULL DEFAULT 0,
    UNIQUE(article_id, chat_id)
);
CREATE INDEX IF NOT EXISTS deliveries_ready ON deliveries(next_attempt_at, id);
CREATE INDEX IF NOT EXISTS deliveries_by_chat ON deliveries(chat_id, id);
CREATE TABLE IF NOT EXISTS source_overrides (
    id TEXT PRIMARY KEY NOT NULL,
    kind TEXT NOT NULL,
    feed_url TEXT NOT NULL,
    removed INTEGER NOT NULL
);
`

type Store struct {
	db          *sql.DB
	defaultChat string
}

type Pending struct {
	QueueID       int64
	SourceID      string
	ChatID        string
	Article       feed.Article
	Attempts      int
	NextAttemptAt time.Time
}

func Open(ctx context.Context, path, defaultChat string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := initialize(ctx, db); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize SQLite: %w", err)
	}
	return &Store{db: db, defaultChat: defaultChat}, nil
}

func initialize(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, schema); err != nil {
		return err
	}
	var legacy bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pragma_table_info('articles') WHERE name = 'status')`).Scan(&legacy); err != nil {
		return err
	}
	if legacy {
		// The empty recipient represents the configurable destination used by the old queue.
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO deliveries(article_id, chat_id, attempts, next_attempt_at)
			SELECT queue_id, '', attempts, next_attempt_at FROM articles WHERE status = 'pending' ORDER BY queue_id;
			DROP INDEX IF EXISTS pending_articles;
			ALTER TABLE articles DROP COLUMN status;
			ALTER TABLE articles DROP COLUMN attempts;
			ALTER TABLE articles DROP COLUMN next_attempt_at;
			ALTER TABLE articles DROP COLUMN message_id;
		`); err != nil {
			return err
		}
	}
	return tx.Commit()
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
	for _, article := range ordered {
		var articleID int64
		err := tx.QueryRowContext(ctx, `INSERT INTO articles(source_id, article_id, title, summary, url)
			VALUES (?, ?, ?, ?, ?) ON CONFLICT(source_id, article_id) DO NOTHING RETURNING queue_id`,
			sourceID, article.ID, article.Title, article.Summary, article.URL).Scan(&articleID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if inserted != 0 || errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO deliveries(article_id, chat_id)
			SELECT ?, '' WHERE ? <> '' UNION ALL SELECT ?, chat_id FROM subscribers WHERE chat_id <> ?`,
			articleID, store.defaultChat, articleID, store.defaultChat); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (store *Store) OldestPending(ctx context.Context) (Pending, error) {
	var item Pending
	var nextAttempt int64
	err := store.db.QueryRowContext(ctx, `SELECT d.id, a.source_id, d.chat_id, a.article_id, a.title, a.summary, a.url, d.attempts, d.next_attempt_at
		FROM deliveries d JOIN articles a ON a.queue_id = d.article_id WHERE (d.chat_id <> '' OR ? <> '')
		AND NOT EXISTS (SELECT 1 FROM deliveries earlier WHERE earlier.chat_id = d.chat_id AND earlier.id < d.id)
		ORDER BY d.next_attempt_at, d.id LIMIT 1`, store.defaultChat).Scan(&item.QueueID, &item.SourceID, &item.ChatID,
		&item.Article.ID, &item.Article.Title, &item.Article.Summary, &item.Article.URL, &item.Attempts, &nextAttempt)
	if errors.Is(err, sql.ErrNoRows) {
		return Pending{}, nil
	}
	item.NextAttemptAt = time.UnixMilli(nextAttempt)
	if item.ChatID == "" {
		item.ChatID = store.defaultChat
	}
	return item, err
}

func (store *Store) MarkSent(ctx context.Context, queueID int64) error {
	_, err := store.db.ExecContext(ctx, `DELETE FROM deliveries WHERE id = ?`, queueID)
	return err
}

func (store *Store) RetryAt(ctx context.Context, queueID int64, next time.Time) error {
	_, err := store.db.ExecContext(ctx, `UPDATE deliveries SET attempts = attempts + 1, next_attempt_at = ? WHERE id = ?`, next.UnixMilli(), queueID)
	return err
}
