package store

import (
	"context"
	"slices"
	"strings"

	"portwatch/internal/config"
)

func (store *Store) Sources(ctx context.Context, defaults []config.Source) ([]config.Source, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT id, kind, feed_url, removed FROM source_overrides`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	merged := make(map[string]config.Source, len(defaults))
	for _, source := range defaults {
		merged[source.ID] = source
	}
	for rows.Next() {
		var source config.Source
		var removed bool
		if err := rows.Scan(&source.ID, &source.Kind, &source.FeedURL, &removed); err != nil {
			return nil, err
		}
		if removed {
			delete(merged, source.ID)
		} else {
			merged[source.ID] = source
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sources := make([]config.Source, 0, len(merged))
	for _, source := range merged {
		sources = append(sources, source)
	}
	slices.SortFunc(sources, func(a, b config.Source) int { return strings.Compare(a.ID, b.ID) })
	return sources, nil
}

func (store *Store) SaveSource(ctx context.Context, source config.Source, removed bool) error {
	_, err := store.db.ExecContext(ctx, `INSERT INTO source_overrides(id, kind, feed_url, removed) VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET kind = excluded.kind, feed_url = excluded.feed_url, removed = excluded.removed`,
		source.ID, source.Kind, source.FeedURL, removed)
	return err
}
