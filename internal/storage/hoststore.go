package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ZeroClue/uptime-monitor/internal/config"
)

// HostStore methods on *DB
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

func (db *DB) CreateHost(ctx context.Context, h *Host) (int64, error) {
	tags, _ := json.Marshal(h.Tags)
	res, err := db.ExecContext(ctx, `
		INSERT INTO hosts (name, connection, endpoint, port, user, key_path, sudo, timeout, proxy_jump, tags, collector_preference, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, h.Name, h.Connection, h.Endpoint, h.Port, h.User, h.KeyPath, h.Sudo, h.TimeoutRaw, h.ProxyJump, string(tags), h.CollectorPreference, time.Now().Unix(), time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (db *DB) GetHost(ctx context.Context, id int64) (*Host, error) {
	row := db.QueryRowContext(ctx, `SELECT id, name, connection, endpoint, port, user, key_path, sudo, timeout, proxy_jump, tags, collector_preference FROM hosts WHERE id = ?`, id)
	var h Host
	var tagsJSON string
	if err := row.Scan(&h.ID, &h.Name, &h.Connection, &h.Endpoint, &h.Port, &h.User, &h.KeyPath, &h.Sudo, &h.TimeoutRaw, &h.ProxyJump, &tagsJSON, &h.CollectorPreference); err != nil {
		return nil, err
	}
	h.Timeout = time.Duration(h.TimeoutRaw)
	h.Tags = parseTags(tagsJSON)
	return &h, nil
}

func (db *DB) UpdateHost(ctx context.Context, h *Host) error {
	tags, _ := json.Marshal(h.Tags)
	_, err := db.ExecContext(ctx, `
		UPDATE hosts SET
			name = ?, connection = ?, endpoint = ?, port = ?, user = ?, key_path = ?, sudo = ?, timeout = ?, proxy_jump = ?, tags = ?, collector_preference = ?, updated_at = ?
		WHERE id = ?
	`, h.Name, h.Connection, h.Endpoint, h.Port, h.User, h.KeyPath, h.Sudo, h.TimeoutRaw, h.ProxyJump, string(tags), h.CollectorPreference, time.Now().Unix(), h.ID)
	return err
}

func (db *DB) DeleteHost(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM hosts WHERE id = ?`, id)
	return err
}
