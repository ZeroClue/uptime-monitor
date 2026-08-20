package storage

import (
	"context"
	"time"
)

func (db *DB) Cleanup(ctx context.Context) error {
	now := time.Now()
	rawCutoff := now.Add(-7 * 24 * time.Hour).Unix()
	minCutoff := now.Add(-90 * 24 * time.Hour).Unix()

	if _, err := db.ExecContext(ctx, `DELETE FROM samples_raw WHERE timestamp < ?`, rawCutoff); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM samples_1m WHERE timestamp < ?`, minCutoff); err != nil {
		return err
	}
	return nil
}
