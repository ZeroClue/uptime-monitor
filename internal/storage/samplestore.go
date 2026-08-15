package storage

import (
	"context"
	"time"

	"github.com/ZeroClue/uptime-monitor/internal/collector"
)

// SampleStore methods on *DB
func (db *DB) SaveSamples(samples []collector.Sample) error {
	if len(samples) == 0 {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT INTO samples_raw (host_id, metric, value, timestamp, collector) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, s := range samples {
		if _, err := stmt.Exec(s.HostID, s.Metric, s.Value, s.Timestamp.Unix(), s.Collector); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (db *DB) GetSamples(ctx context.Context, hostID int64, metric string, from, to time.Time, resolution string) ([]Sample, error) {
	cfg, ok := resolutionMap[resolution]
	if !ok {
		cfg = resolutionMap["raw"]
	}

	query := `SELECT host_id, metric, ` + cfg.valueCol + `, timestamp, '' FROM ` + cfg.table + ` WHERE host_id = ? AND metric = ? AND timestamp >= ? AND timestamp <= ? ORDER BY timestamp`
	rows, err := db.QueryContext(ctx, query, hostID, metric, from.Unix(), to.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var samples []Sample
	for rows.Next() {
		var s Sample
		var ts int64
		if err := rows.Scan(&s.HostID, &s.Metric, &s.Value, &ts, &s.Collector); err != nil {
			return nil, err
		}
		s.Timestamp = time.Unix(ts, 0)
		samples = append(samples, s)
	}
	return samples, nil
}