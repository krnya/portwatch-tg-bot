package store

import "context"

func (store *Store) IsDefaultChat(chatID string) bool {
	return chatID == store.defaultChat
}

func (store *Store) Subscribe(ctx context.Context, chatID string) error {
	_, err := store.db.ExecContext(ctx, `INSERT INTO subscribers(chat_id) VALUES (?) ON CONFLICT DO NOTHING`, chatID)
	return err
}

func (store *Store) Unsubscribe(ctx context.Context, chatID string) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM subscribers WHERE chat_id = ?`, chatID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM deliveries WHERE chat_id = ?`, chatID); err != nil {
		return err
	}
	return tx.Commit()
}
