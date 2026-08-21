package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ZeroClue/uptime-monitor/internal/config"
)

// HostStore methods on *DB
func (db *DB) GetHosts() ([]Host, error) {
	return db.GetHostsByProject(context.Background(), nil)
}

const hostColumns = `id, name, connection, endpoint, port, user, key_path, sudo, timeout, proxy_jump, tags, collector_preference, project_id, ssh_host_key_policy, retry_max_retries, retry_base_delay_ms, retry_max_delay_ms, ssh_timeout_ms, collector_timeout_ms`

// scanHostRow scans one hosts row (column order per hostColumns) into h.
func scanHostRow(row interface{ Scan(...any) error }, h *Host) error {
	var tagsJSON string
	var timeoutRaw int64
	var sshKeyPolicy sql.NullString
	var retryMax, retryBaseMs, retryMaxMs, sshTimeoutMs, collectorTimeoutMs, projectID sql.NullInt64
	if err := row.Scan(&h.ID, &h.Name, &h.Connection, &h.Endpoint, &h.Port, &h.User, &h.KeyPath, &h.Sudo, &timeoutRaw, &h.ProxyJump, &tagsJSON, &h.CollectorPreference, &projectID, &sshKeyPolicy, &retryMax, &retryBaseMs, &retryMaxMs, &sshTimeoutMs, &collectorTimeoutMs); err != nil {
		return err
	}
	if sshKeyPolicy.Valid {
		h.SSHHostKeyPolicy = &sshKeyPolicy.String
	}
	h.TimeoutRaw = timeoutRaw
	h.Timeout = time.Duration(timeoutRaw)
	h.Tags = parseTags(tagsJSON)
	if retryMax.Valid {
		h.RetryMaxRetries = &retryMax.Int64
	}
	if retryBaseMs.Valid {
		h.RetryBaseMs = &retryBaseMs.Int64
	}
	if retryMaxMs.Valid {
		h.RetryMaxMs = &retryMaxMs.Int64
	}
	if projectID.Valid {
		h.ProjectID = &projectID.Int64
	}
	if sshTimeoutMs.Valid {
		h.SshTimeoutMs = &sshTimeoutMs.Int64
	}
	if collectorTimeoutMs.Valid {
		h.CollectorTimeoutMs = &collectorTimeoutMs.Int64
	}
	return nil
}

func nullIfZero(p *int64) interface{} {
	if p == nil {
		return nil
	}
	return *p
}

func nullIfNilPtr(p *string) interface{} {
	if p == nil || *p == "" {
		return nil
	}
	return *p
}

func durationMsOrNull(d *time.Duration) interface{} {
	if d == nil {
		return nil
	}
	return d.Milliseconds()
}

func (db *DB) GetHostsByProject(ctx context.Context, projectID interface{}) ([]Host, error) {
	query := `SELECT ` + hostColumns + ` FROM hosts`
	var args []interface{}
	if projectID != nil {
		query += ` WHERE project_id = ?`
		args = append(args, projectID)
	}

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var hosts []Host
	for rows.Next() {
		var h Host
		if err := scanHostRow(rows, &h); err != nil {
			return nil, err
		}
		hosts = append(hosts, h)
	}
	return hosts, nil
}

func (db *DB) SeedHosts(hosts []config.Host) error {
	for _, h := range hosts {
		tags, _ := json.Marshal(h.Tags)
		_, err := db.Exec(`
			INSERT INTO hosts (name, connection, endpoint, port, user, key_path, sudo, timeout, proxy_jump, tags, collector_preference, project_id, ssh_host_key_policy, retry_max_retries, retry_base_delay_ms, retry_max_delay_ms, ssh_timeout_ms, collector_timeout_ms, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
				ssh_timeout_ms=excluded.ssh_timeout_ms,
				collector_timeout_ms=excluded.collector_timeout_ms,
				retry_max_retries=excluded.retry_max_retries,
				retry_base_delay_ms=excluded.retry_base_delay_ms,
				retry_max_delay_ms=excluded.retry_max_delay_ms,
				updated_at=excluded.updated_at
		`, h.Name, h.Connection, h.Endpoint, h.Port, h.User, h.KeyPath, h.Sudo, h.Timeout.Nanoseconds(), h.ProxyJump, string(tags), h.CollectorPreference, nullIfZero(h.ProjectID), nullIfNilPtr(h.SSHHostKeyPolicy), nullIfZero(h.RetryMaxRetries), durationMsOrNull(h.RetryBaseDelay), durationMsOrNull(h.RetryMaxDelay), durationMsOrNull(h.SSHTimeout), durationMsOrNull(h.CollectorTimeout), time.Now().Unix(), time.Now().Unix())
		if err != nil {
			return fmt.Errorf("failed to seed host %s: %w", h.Name, err)
		}
	}
	return nil
}

func (db *DB) CreateHost(ctx context.Context, h *Host) (int64, error) {
	tags, _ := json.Marshal(h.Tags)
	res, err := db.ExecContext(ctx, `
		INSERT INTO hosts (name, connection, endpoint, port, user, key_path, sudo, timeout, proxy_jump, tags, collector_preference, project_id, ssh_host_key_policy, retry_max_retries, retry_base_delay_ms, retry_max_delay_ms, ssh_timeout_ms, collector_timeout_ms, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, h.Name, h.Connection, h.Endpoint, h.Port, h.User, h.KeyPath, h.Sudo, h.TimeoutRaw, h.ProxyJump, string(tags), h.CollectorPreference, nullIfZero(h.ProjectID), nullIfNilPtr(h.SSHHostKeyPolicy), nullIfZero(h.RetryMaxRetries), nullIfZero(h.RetryBaseMs), nullIfZero(h.RetryMaxMs), nullIfZero(h.SshTimeoutMs), nullIfZero(h.CollectorTimeoutMs), time.Now().Unix(), time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (db *DB) GetHost(ctx context.Context, id int64) (*Host, error) {
	row := db.QueryRowContext(ctx, `SELECT `+hostColumns+` FROM hosts WHERE id = ?`, id)
	var h Host
	if err := scanHostRow(row, &h); err != nil {
		return nil, err
	}
	return &h, nil
}

func (db *DB) UpdateHost(ctx context.Context, h *Host) error {
	tags, _ := json.Marshal(h.Tags)
	_, err := db.ExecContext(ctx, `
		UPDATE hosts SET
			name = ?, connection = ?, endpoint = ?, port = ?, user = ?, key_path = ?, sudo = ?, timeout = ?, proxy_jump = ?, tags = ?, collector_preference = ?, project_id = ?, ssh_host_key_policy = ?, retry_max_retries = ?, retry_base_delay_ms = ?, retry_max_delay_ms = ?, ssh_timeout_ms = ?, collector_timeout_ms = ?, updated_at = ?
		WHERE id = ?
	`, h.Name, h.Connection, h.Endpoint, h.Port, h.User, h.KeyPath, h.Sudo, h.TimeoutRaw, h.ProxyJump, string(tags), h.CollectorPreference, nullIfZero(h.ProjectID), nullIfNilPtr(h.SSHHostKeyPolicy), nullIfZero(h.RetryMaxRetries), nullIfZero(h.RetryBaseMs), nullIfZero(h.RetryMaxMs), nullIfZero(h.SshTimeoutMs), nullIfZero(h.CollectorTimeoutMs), time.Now().Unix(), h.ID)
	return err
}

func (db *DB) DeleteHost(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM hosts WHERE id = ?`, id)
	return err
}
