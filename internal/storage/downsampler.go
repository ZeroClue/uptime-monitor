package storage

import (
	"context"
	"time"
)

func (db *DB) Downsample(ctx context.Context) error {
	now := time.Now()
	oneMinAgo := now.Truncate(time.Minute).Add(-time.Minute)
	oneHourAgo := now.Truncate(time.Hour).Add(-time.Hour)

	if err := db.downsampleRawTo1m(ctx, oneMinAgo); err != nil {
		return err
	}
	if err := db.downsample1mTo1h(ctx, oneHourAgo); err != nil {
		return err
	}
	return nil
}

func (db *DB) downsampleRawTo1m(ctx context.Context, bucketTime time.Time) error {
	bucketStart := bucketTime.Unix()
	bucketEnd := bucketStart + 60

	_, err := db.ExecContext(ctx, `
		INSERT INTO samples_1m (host_id, metric, value_avg, value_min, value_max, count, timestamp)
		SELECT host_id, metric, AVG(value), MIN(value), MAX(value), COUNT(*), ?
		FROM samples_raw
		WHERE timestamp >= ? AND timestamp < ?
		GROUP BY host_id, metric
	`, bucketStart, bucketStart, bucketEnd)
	return err
}

func (db *DB) downsample1mTo1h(ctx context.Context, bucketTime time.Time) error {
	bucketStart := bucketTime.Unix()
	bucketEnd := bucketStart + 3600

	_, err := db.ExecContext(ctx, `
		INSERT INTO samples_1h (host_id, metric, value_avg, value_min, value_max, count, timestamp)
		SELECT host_id, metric, AVG(value_avg), MIN(value_min), MAX(value_max), SUM(count), ?
		FROM samples_1m
		WHERE timestamp >= ? AND timestamp < ?
		GROUP BY host_id, metric
	`, bucketStart, bucketStart, bucketEnd)
	return err
}
