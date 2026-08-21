package storage

import (
	"fmt"
)

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
		owner_id INTEGER,
		isolation_level TEXT NOT NULL DEFAULT 'shared',
		is_default BOOLEAN NOT NULL DEFAULT FALSE,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		FOREIGN KEY (owner_id) REFERENCES users(id)
	)`,
	`CREATE TABLE IF NOT EXISTS project_members (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		project_id INTEGER NOT NULL,
		user_id INTEGER NOT NULL,
		role TEXT NOT NULL DEFAULT 'member',
		created_at INTEGER NOT NULL,
		FOREIGN KEY (project_id) REFERENCES projects(id),
		FOREIGN KEY (user_id) REFERENCES users(id),
		UNIQUE(project_id, user_id)
	)`,
	`CREATE TABLE IF NOT EXISTS users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT UNIQUE NOT NULL,
		password_hash TEXT NOT NULL,
		email TEXT,
		role TEXT NOT NULL DEFAULT 'user',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
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
			silenced_until INTEGER,
			FOREIGN KEY (host_id) REFERENCES hosts(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_alerts_host_fired ON alerts(host_id, fired_at)`,
		`CREATE INDEX IF NOT EXISTS idx_alerts_resolved ON alerts(resolved_at)`,
		`CREATE TABLE IF NOT EXISTS alert_rules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			metric TEXT NOT NULL,
			scope TEXT NOT NULL DEFAULT 'global',
			host_id INTEGER,
			warning REAL NOT NULL,
			critical REAL NOT NULL,
			below BOOLEAN NOT NULL DEFAULT FALSE,
			enabled BOOLEAN NOT NULL DEFAULT TRUE,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			FOREIGN KEY (host_id) REFERENCES hosts(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_alert_rules_metric_scope ON alert_rules(metric, scope, host_id)`,
		`CREATE TABLE IF NOT EXISTS notification_channels (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT UNIQUE NOT NULL,
		type TEXT NOT NULL,
		config TEXT NOT NULL,
		enabled BOOLEAN NOT NULL DEFAULT TRUE,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	)`,
`CREATE TABLE IF NOT EXISTS alert_config (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		collection_failure_threshold INTEGER NOT NULL DEFAULT 3,
		webhooks TEXT NOT NULL DEFAULT '[]',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS api_tokens (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		token_hash TEXT NOT NULL,
		project_id INTEGER,
		scopes TEXT NOT NULL DEFAULT 'read',
		expires_at INTEGER,
		last_used_at INTEGER,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		FOREIGN KEY (project_id) REFERENCES projects(id)
	)`,
	`CREATE INDEX IF NOT EXISTS idx_api_tokens_token_hash ON api_tokens(token_hash)`,
	`CREATE INDEX IF NOT EXISTS idx_api_tokens_project_id ON api_tokens(project_id)`,
}

	for _, q := range queries {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("migration failed: %w", err)
		}
	}
	return nil
}
