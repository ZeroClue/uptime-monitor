package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ZeroClue/uptime-monitor/internal/config"
)

// HostStore methods on *DB
func (db *DB) GetHosts() ([]Host, error) {
	return db.GetHostsByProject(context.Background(), nil)
}

// hostField couples one hosts-table column to its read and write wiring for
// *Host. The order of hostFields is the single source of truth from which
// every SELECT column list, INSERT, UPDATE SET, upsert assignment and row
// scan below is built: adding a column means adding exactly one entry here.
type hostField struct {
	name      string
	yamlOwned bool                                  // ADR-0007: yaml owns this column; refreshed on every re-seed
	bind      func(*Host) any                       // INSERT / UPDATE value; nil = never written here (id)
	scan      func(*Host) (dest any, finish func()) // row-scan destination plus post-scan normalization
}

var hostFields = []hostField{
	{
		name: "id",
		scan: func(h *Host) (any, func()) { return &h.ID, nil },
	},
	{
		name: "name",
		bind: func(h *Host) any { return h.Name },
		scan: func(h *Host) (any, func()) { return &h.Name, nil },
	},
	{
		name:      "connection",
		yamlOwned: true,
		bind:      func(h *Host) any { return h.Connection },
		scan:      func(h *Host) (any, func()) { return &h.Connection, nil },
	},
	{
		name:      "endpoint",
		yamlOwned: true,
		bind:      func(h *Host) any { return h.Endpoint },
		scan:      func(h *Host) (any, func()) { return &h.Endpoint, nil },
	},
	{
		name:      "port",
		yamlOwned: true,
		bind:      func(h *Host) any { return h.Port },
		scan:      func(h *Host) (any, func()) { return &h.Port, nil },
	},
	{
		name:      "user",
		yamlOwned: true,
		bind:      func(h *Host) any { return h.User },
		scan:      func(h *Host) (any, func()) { return &h.User, nil },
	},
	{
		name:      "key_path",
		yamlOwned: true,
		bind:      func(h *Host) any { return h.KeyPath },
		scan:      func(h *Host) (any, func()) { return &h.KeyPath, nil },
	},
	{
		name:      "sudo",
		yamlOwned: true,
		bind:      func(h *Host) any { return h.Sudo },
		scan:      func(h *Host) (any, func()) { return &h.Sudo, nil },
	},
	{
		name: "timeout",
		bind: func(h *Host) any { return h.TimeoutRaw },
		scan: func(h *Host) (any, func()) {
			raw := new(int64)
			return raw, func() {
				h.TimeoutRaw = *raw
				h.Timeout = time.Duration(*raw)
			}
		},
	},
	{
		name:      "proxy_jump",
		yamlOwned: true,
		bind:      func(h *Host) any { return h.ProxyJump },
		scan:      func(h *Host) (any, func()) { return &h.ProxyJump, nil },
	},
	{
		name:      "tags",
		yamlOwned: true,
		bind: func(h *Host) any {
			tags, _ := json.Marshal(h.Tags)
			return string(tags)
		},
		scan: func(h *Host) (any, func()) {
			raw := new(string)
			return raw, func() { h.Tags = parseTags(*raw) }
		},
	},
	{
		name: "collector_preference",
		bind: func(h *Host) any { return h.CollectorPreference },
		scan: func(h *Host) (any, func()) { return &h.CollectorPreference, nil },
	},
	{
		name: "project_id",
		bind: func(h *Host) any { return nullIfZero(h.ProjectID) },
		scan: nullableIntScan(func(h *Host, v int64) { h.ProjectID = &v }),
	},
	{
		name: "ssh_host_key_policy",
		bind: func(h *Host) any { return nullIfNilPtr(h.SSHHostKeyPolicy) },
		scan: nullableStringScan(func(h *Host, v string) { h.SSHHostKeyPolicy = &v }),
	},
	{
		name: "retry_max_retries",
		bind: func(h *Host) any { return nullIfZero(h.RetryMaxRetries) },
		scan: nullableIntScan(func(h *Host, v int64) { h.RetryMaxRetries = &v }),
	},
	{
		name: "retry_base_delay_ms",
		bind: func(h *Host) any { return nullIfZero(h.RetryBaseMs) },
		scan: nullableIntScan(func(h *Host, v int64) { h.RetryBaseMs = &v }),
	},
	{
		name: "retry_max_delay_ms",
		bind: func(h *Host) any { return nullIfZero(h.RetryMaxMs) },
		scan: nullableIntScan(func(h *Host, v int64) { h.RetryMaxMs = &v }),
	},
	{
		name: "ssh_timeout_ms",
		bind: func(h *Host) any { return nullIfZero(h.SshTimeoutMs) },
		scan: nullableIntScan(func(h *Host, v int64) { h.SshTimeoutMs = &v }),
	},
	{
		name: "collector_timeout_ms",
		bind: func(h *Host) any { return nullIfZero(h.CollectorTimeoutMs) },
		scan: nullableIntScan(func(h *Host, v int64) { h.CollectorTimeoutMs = &v }),
	},
	{
		name: "script_name",
		bind: func(h *Host) any { return h.ScriptName },
		scan: func(h *Host) (any, func()) { return &h.ScriptName, nil },
	},
	{
		name: "script_command",
		bind: func(h *Host) any { return h.ScriptCommand },
		scan: func(h *Host) (any, func()) { return &h.ScriptCommand, nil },
	},
	{
		name: "script_parse",
		bind: func(h *Host) any { return h.ScriptParse },
		scan: func(h *Host) (any, func()) { return &h.ScriptParse, nil },
	},
}

func nullableIntScan(set func(*Host, int64)) func(*Host) (any, func()) {
	return func(h *Host) (any, func()) {
		n := new(sql.NullInt64)
		return n, func() {
			if n.Valid {
				set(h, n.Int64)
			}
		}
	}
}

func nullableStringScan(set func(*Host, string)) func(*Host) (any, func()) {
	return func(h *Host) (any, func()) {
		n := new(sql.NullString)
		return n, func() {
			if n.Valid {
				set(h, n.String)
			}
		}
	}
}

func hostColumnNames(keep func(hostField) bool) []string {
	names := make([]string, 0, len(hostFields))
	for i := range hostFields {
		if keep == nil || keep(hostFields[i]) {
			names = append(names, hostFields[i].name)
		}
	}
	return names
}

var (
	hostSelectColumns = strings.Join(hostColumnNames(nil), ", ")
	hostWriteColumns  = hostColumnNames(func(f hostField) bool { return f.bind != nil })
)

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

func hostInsertSQL() string {
	cols := append(append([]string{}, hostWriteColumns...), "created_at", "updated_at")
	return `INSERT INTO hosts (` + strings.Join(cols, ", ") + `)
			VALUES (` + placeholders(len(cols)) + `)`
}

func hostUpsertSet() string {
	assignments := make([]string, 0, len(hostFields))
	for i := range hostFields {
		if hostFields[i].yamlOwned {
			assignments = append(assignments, hostFields[i].name+"=excluded."+hostFields[i].name)
		}
	}
	return strings.Join(assignments, ", ")
}

func hostUpdateSet() string {
	assignments := make([]string, 0, len(hostWriteColumns)+1)
	for _, col := range hostWriteColumns {
		assignments = append(assignments, col+" = ?")
	}
	assignments = append(assignments, "updated_at = ?")
	return strings.Join(assignments, ", ")
}

func hostBindValues(h *Host) []any {
	values := make([]any, 0, len(hostWriteColumns))
	for i := range hostFields {
		if f := hostFields[i]; f.bind != nil {
			values = append(values, f.bind(h))
		}
	}
	return values
}

// scanHostRow scans one hosts row (column order per hostFields) into h.
func scanHostRow(row interface{ Scan(...any) error }, h *Host) error {
	dests := make([]any, len(hostFields))
	var finish []func()
	for i := range hostFields {
		dest, after := hostFields[i].scan(h)
		dests[i] = dest
		if after != nil {
			finish = append(finish, after)
		}
	}
	if err := row.Scan(dests...); err != nil {
		return err
	}
	for _, fn := range finish {
		fn()
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

// seedToHost maps yaml config onto storage.Host so seeding reuses the exact
// write wiring of the API path instead of a parallel positional argument list.
func seedToHost(c config.Host) *Host {
	h := &Host{
		Name:                c.Name,
		Connection:          c.Connection,
		Endpoint:            c.Endpoint,
		Port:                c.Port,
		User:                c.User,
		KeyPath:             c.KeyPath,
		Sudo:                c.Sudo,
		TimeoutRaw:          c.Timeout.Nanoseconds(),
		ProxyJump:           c.ProxyJump,
		Tags:                c.Tags,
		CollectorPreference: c.CollectorPreference,
		ProjectID:           c.ProjectID,
		RetryMaxRetries:     c.RetryMaxRetries,
		SSHHostKeyPolicy:    c.SSHHostKeyPolicy,
	}
	if c.RetryBaseDelay != nil {
		ms := c.RetryBaseDelay.Milliseconds()
		h.RetryBaseMs = &ms
	}
	if c.RetryMaxDelay != nil {
		ms := c.RetryMaxDelay.Milliseconds()
		h.RetryMaxMs = &ms
	}
	if c.SSHTimeout != nil {
		ms := c.SSHTimeout.Milliseconds()
		h.SshTimeoutMs = &ms
	}
	if c.CollectorTimeout != nil {
		ms := c.CollectorTimeout.Milliseconds()
		h.CollectorTimeoutMs = &ms
	}
	return h
}

// GetHostsByProject returns hosts in the given project; nil means all
// hosts. The *int64 type (not interface{}) prevents a typed-nil from being
// mistaken for an active filter.
func (db *DB) GetHostsByProject(ctx context.Context, projectID *int64) ([]Host, error) {
	query := `SELECT ` + hostSelectColumns + ` FROM hosts`
	var args []interface{}
	if projectID != nil {
		query += ` WHERE project_id = ?`
		args = append(args, *projectID)
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
	for _, c := range hosts {
		h := seedToHost(c)
		now := time.Now().Unix()
		args := append(hostBindValues(h), now, now)
		query := hostInsertSQL() + `
			ON CONFLICT(name) DO UPDATE SET
				` + hostUpsertSet() + `,
				updated_at=excluded.updated_at`
		if _, err := db.Exec(query, args...); err != nil {
			return fmt.Errorf("failed to seed host %s: %w", c.Name, err)
		}
	}
	return nil
}

func (db *DB) CreateHost(ctx context.Context, h *Host) (int64, error) {
	args := append(hostBindValues(h), time.Now().Unix(), time.Now().Unix())
	res, err := db.ExecContext(ctx, hostInsertSQL(), args...)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (db *DB) GetHost(ctx context.Context, id int64) (*Host, error) {
	row := db.QueryRowContext(ctx, `SELECT `+hostSelectColumns+` FROM hosts WHERE id = ?`, id)
	var h Host
	if err := scanHostRow(row, &h); err != nil {
		return nil, err
	}
	return &h, nil
}

func (db *DB) UpdateHost(ctx context.Context, h *Host) error {
	args := append(hostBindValues(h), time.Now().Unix(), h.ID)
	_, err := db.ExecContext(ctx, `UPDATE hosts SET `+hostUpdateSet()+` WHERE id = ?`, args...)
	return err
}

func (db *DB) DeleteHost(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM hosts WHERE id = ?`, id)
	return err
}

// GetHostByName returns the host with the given name, or nil when absent.
func (db *DB) GetHostByName(ctx context.Context, name string) (*Host, error) {
	row := db.QueryRowContext(ctx, `SELECT `+hostSelectColumns+` FROM hosts WHERE name = ?`, name)
	var h Host
	if err := scanHostRow(row, &h); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return &h, nil
}
