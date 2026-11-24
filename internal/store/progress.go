package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"portwatch/internal/feed"
)

func (store *Store) RecordProgress(ctx context.Context, sourceID string, progress feed.Progress) (bool, error) {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var previous feed.Progress
	var updatedAt, version int64
	err = tx.QueryRowContext(ctx, `SELECT score, updated_at, version FROM tracker_progress WHERE source_id = ?`, sourceID).
		Scan(&previous.Score, &updatedAt, &version)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	var observedAt int64
	if !progress.UpdatedAt.IsZero() {
		observedAt = progress.UpdatedAt.UnixMilli()
		if observedAt < updatedAt {
			return false, nil
		}
	}
	changed := err == nil && previous.Score != progress.Score
	if changed {
		version++
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO tracker_progress(source_id, score, leader, url, updated_at, version)
		VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(source_id) DO UPDATE SET score = excluded.score, leader = excluded.leader,
		url = excluded.url, updated_at = excluded.updated_at, version = excluded.version`,
		sourceID, progress.Score, progress.Leader, progress.URL, max(observedAt, updatedAt), version); err != nil {
		return false, err
	}
	if changed {
		title := fmt.Sprintf("Прогресс PortWatch VI: %s → %s", previous.Percentage(), progress.Percentage())
		var summary string
		if progress.Leader != "" {
			summary = "Лидер: " + progress.Leader
		}
		var articleID int64
		if err := tx.QueryRowContext(ctx, `INSERT INTO articles(source_id, article_id, title, summary, url)
			VALUES (?, ?, ?, ?, ?) RETURNING queue_id`, sourceID, fmt.Sprintf("progress:%d", version),
			title, summary, progress.URL).Scan(&articleID); err != nil {
			return false, err
		}
		if err := store.enqueue(ctx, tx, articleID); err != nil {
			return false, err
		}
	}
	return changed, tx.Commit()
}

func (store *Store) Progress(ctx context.Context, sourceID string) (feed.Progress, bool, error) {
	var progress feed.Progress
	err := store.db.QueryRowContext(ctx, `SELECT score, leader, url FROM tracker_progress WHERE source_id = ?`, sourceID).
		Scan(&progress.Score, &progress.Leader, &progress.URL)
	if errors.Is(err, sql.ErrNoRows) {
		return feed.Progress{}, false, nil
	}
	return progress, err == nil, err
}
