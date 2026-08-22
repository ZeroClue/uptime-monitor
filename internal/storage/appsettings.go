package storage

import (
	"context"
	"database/sql"
)

// GetSetting returns the stored value for key, or "" when unset.
// Backed by the generic app_settings KV table (see migrator).
func (db *DB) GetSetting(ctx context.Context, key string) (string, error) {
	var v string
	err := db.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

// SetSetting upserts a single settings key.
func (db *DB) SetSetting(ctx context.Context, key, value string) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO app_settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, key, value)
	return err
}
