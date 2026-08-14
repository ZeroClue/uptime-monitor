package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/ZeroClue/uptime-monitor/internal/collector"
	"github.com/ZeroClue/uptime-monitor/internal/config"
	_ "github.com/mattn/go-sqlite3"
)

type DB struct {
	*sql.DB
	logger *slog.Logger
}

func New(dataDir string) (*DB, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, err
	}
	dbPath := filepath.Join(dataDir, "monitor.db")
	sqlDB, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(1)
	// Ensure database file is created
	if err := sqlDB.Ping(); err != nil {
		return nil, err
	}
	return &DB{DB: sqlDB, logger: slog.Default()}, nil
}

func (db *DB) Migrate() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS hosts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT UNIQUE NOT NULL,
			connection TEXT NOT NULL,
			endpoint TEXT NOT NULL,
			port INTEGER NOT NULL DEFAULT 22,
			user TEXT NOT NULL,
			key_path TEXT NOT NULL,
			sudo BOOLEAN NOT NULL DEFAULT FALSE,
			timeout INTEGER NOT NULL DEFAULT 10000000000,
			proxy_jump TEXT,
			tags TEXT,
			collector_preference TEXT,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS samples_raw (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			host_id INTEGER NOT NULL,
			metric TEXT NOT NULL,
			value REAL NOT NULL,
			timestamp INTEGER NOT NULL,
			collector TEXT,
			FOREIGN KEY (host_id) REFERENCES hosts(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_samples_raw_host_metric_time ON samples_raw(host_id, metric, timestamp)`,
		`CREATE TABLE IF NOT EXISTS samples_1m (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			host_id INTEGER NOT NULL,
			metric TEXT NOT NULL,
			value_avg REAL NOT NULL,
			value_min REAL NOT NULL,
			value_max REAL NOT NULL,
			count INTEGER NOT NULL,
			timestamp INTEGER NOT NULL,
			FOREIGN KEY (host_id) REFERENCES hosts(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_samples_1m_host_metric_time ON samples_1m(host_id, metric, timestamp)`,
		`CREATE TABLE IF NOT EXISTS samples_1h (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			host_id INTEGER NOT NULL,
			metric TEXT NOT NULL,
			value_avg REAL NOT NULL,
			value_min REAL NOT NULL,
			value_max REAL NOT NULL,
			count INTEGER NOT NULL,
			timestamp INTEGER NOT NULL,
			FOREIGN KEY (host_id) REFERENCES hosts(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_samples_1h_host_metric_time ON samples_1h(host_id, metric, timestamp)`,
		`CREATE TABLE IF NOT EXISTS projects (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT UNIQUE NOT NULL,
			type TEXT NOT NULL,
			tag_query TEXT,
			host_ids TEXT,
			created_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS alerts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			host_id INTEGER NOT NULL,
			type TEXT NOT NULL,
			metric TEXT,
			severity TEXT NOT NULL,
			message TEXT NOT NULL,
			value REAL,
			threshold REAL,
			fired_at INTEGER NOT NULL,
			acknowledged_at INTEGER,
			resolved_at INTEGER,
			FOREIGN KEY (host_id) REFERENCES hosts(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_alerts_host_fired ON alerts(host_id, fired_at)`,
		`CREATE INDEX IF NOT EXISTS idx_alerts_resolved ON alerts(resolved_at)`,
	}

	for _, q := range queries {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("migration failed: %w", err)
		}
	}
	return nil
}

func (db *DB) SeedHosts(hosts []config.Host) error {
	for _, h := range hosts {
		tags, _ := json.Marshal(h.Tags)
		_, err := db.Exec(`
			INSERT INTO hosts (name, connection, endpoint, port, user, key_path, sudo, timeout, proxy_jump, tags, collector_preference, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(name) DO UPDATE SET
				connection=excluded.connection,
				endpoint=excluded.endpoint,
				port=excluded.port,
				user=excluded.user,
				key_path=excluded.key_path,
				sudo=excluded.sudo,
				timeout=excluded.timeout,
				proxy_jump=excluded.proxy_jump,
				tags=excluded.tags,
				collector_preference=excluded.collector_preference,
				updated_at=excluded.updated_at
		`, h.Name, h.Connection, h.Endpoint, h.Port, h.User, h.KeyPath, h.Sudo, h.Timeout.Nanoseconds(), h.ProxyJump, string(tags), h.CollectorPreference, time.Now().Unix(), time.Now().Unix())
		if err != nil {
			return fmt.Errorf("failed to seed host %s: %w", h.Name, err)
		}
	}
	return nil
}

func (db *DB) GetHosts() ([]Host, error) {
	rows, err := db.Query(`SELECT id, name, connection, endpoint, port, user, key_path, sudo, timeout, proxy_jump, tags, collector_preference FROM hosts`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var hosts []Host
	for rows.Next() {
		var h Host
		var tagsJSON string
		if err := rows.Scan(&h.ID, &h.Name, &h.Connection, &h.Endpoint, &h.Port, &h.User, &h.KeyPath, &h.Sudo, &h.Timeout, &h.ProxyJump, &tagsJSON, &h.CollectorPreference); err != nil {
			return nil, err
		}
		h.Timeout = time.Duration(h.TimeoutRaw)
		h.Tags = parseTags(tagsJSON)
		hosts = append(hosts, h)
	}
	return hosts, nil
}

func parseTags(s string) []string {
	if s == "" {
		return nil
	}
	var tags []string
	if err := json.Unmarshal([]byte(s), &tags); err != nil {
		return nil
	}
	return tags
}

type Host struct {
	ID                  int64
	Name                string
	Connection          string
	Endpoint            string
	Port                int
	User                string
	KeyPath             string
	Sudo                bool
	TimeoutRaw          int64
	Timeout             time.Duration
	ProxyJump           string
	Tags                []string
	CollectorPreference string
}

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

type resolutionConfig struct {
	table    string
	valueCol string
}

var resolutionMap = map[string]resolutionConfig{
	"raw": {"samples_raw", "value"},
	"1m":  {"samples_1m", "value_avg"},
	"1h":  {"samples_1h", "value_avg"},
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

type Sample struct {
	HostID    int64
	Metric    string
	Value     float64
	Timestamp time.Time
	Collector string
}
