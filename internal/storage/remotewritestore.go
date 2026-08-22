package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

type RemoteWriteConfig struct {
	ID          int64             `json:"id"`
	Enabled     bool              `json:"enabled"`
	URL         string            `json:"url"`
	AuthType    string            `json:"auth_type"` // '' | basic | bearer
	Username    string            `json:"username"`
	Password    string            `json:"password"`
	BearerToken string            `json:"bearer_token"`
	ExtraLabels map[string]string `json:"extra_labels"`
	BatchSize   int               `json:"batch_size"`
	TimeoutMs   int64             `json:"timeout_ms"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

func scanRemoteWriteConfigRow(row interface{ Scan(...any) error }, c *RemoteWriteConfig) error {
	var labelsJSON string
	var createdAt, updatedAt int64
	if err := row.Scan(&c.ID, &c.Enabled, &c.URL, &c.AuthType, &c.Username, &c.Password,
		&c.BearerToken, &labelsJSON, &c.BatchSize, &c.TimeoutMs, &createdAt, &updatedAt); err != nil {
		return err
	}
	c.ExtraLabels = map[string]string{}
	if err := json.Unmarshal([]byte(labelsJSON), &c.ExtraLabels); err != nil {
		c.ExtraLabels = map[string]string{}
	}
	c.CreatedAt = time.Unix(createdAt, 0)
	c.UpdatedAt = time.Unix(updatedAt, 0)
	return nil
}

// GetRemoteWriteConfig returns the global remote-write config, or nil when
// it has never been created.
func (db *DB) GetRemoteWriteConfig(ctx context.Context) (*RemoteWriteConfig, error) {
	row := db.QueryRowContext(ctx, `SELECT id, enabled, url, auth_type, username, password,
		bearer_token, extra_labels, batch_size, timeout_ms, created_at, updated_at
		FROM remote_write_config ORDER BY id LIMIT 1`)
	var c RemoteWriteConfig
	if err := scanRemoteWriteConfigRow(row, &c); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return &c, nil
}

func (db *DB) CreateRemoteWriteConfig(ctx context.Context, c *RemoteWriteConfig) (int64, error) {
	labelsJSON, err := json.Marshal(c.ExtraLabels)
	if err != nil {
		return 0, err
	}
	now := time.Now().Unix()
	res, err := db.ExecContext(ctx, `INSERT INTO remote_write_config
		(enabled, url, auth_type, username, password, bearer_token, extra_labels, batch_size, timeout_ms, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.Enabled, c.URL, c.AuthType, c.Username, c.Password, c.BearerToken,
		string(labelsJSON), c.BatchSize, c.TimeoutMs, now, now)
	if err != nil {
		return 0, err
	}
	c.ID, _ = res.LastInsertId()
	c.CreatedAt = time.Unix(now, 0)
	c.UpdatedAt = time.Unix(now, 0)
	return c.ID, nil
}

func (db *DB) UpdateRemoteWriteConfig(ctx context.Context, c *RemoteWriteConfig) error {
	labelsJSON, err := json.Marshal(c.ExtraLabels)
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	c.UpdatedAt = time.Unix(now, 0)
	_, err = db.ExecContext(ctx, `UPDATE remote_write_config SET
		enabled = ?, url = ?, auth_type = ?, username = ?, password = ?, bearer_token = ?,
		extra_labels = ?, batch_size = ?, timeout_ms = ?, updated_at = ?
		WHERE id = ?`,
		c.Enabled, c.URL, c.AuthType, c.Username, c.Password, c.BearerToken,
		string(labelsJSON), c.BatchSize, c.TimeoutMs, now, c.ID)
	return err
}
