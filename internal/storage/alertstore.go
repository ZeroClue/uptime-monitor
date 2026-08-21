package storage

import (
	"context"
	"database/sql"
	"time"
)

func (db *DB) InsertAlert(ctx context.Context, alert Alert) error {
	var silencedUntil *int64
	if alert.SilencedUntil != nil {
		ts := alert.SilencedUntil.Unix()
		silencedUntil = &ts
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO alerts (host_id, type, metric, severity, message, value, threshold, fired_at, silenced_until)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, alert.HostID, alert.Type, alert.Metric, alert.Severity, alert.Message, alert.Value, alert.Threshold, alert.FiredAt.Unix(), silencedUntil)
	return err
}

func (db *DB) GetActiveAlert(ctx context.Context, hostID int64, alertType, metric string) (*Alert, error) {
	query := `SELECT id, host_id, type, metric, severity, message, value, threshold, fired_at, acknowledged_at, resolved_at, silenced_until
		FROM alerts WHERE host_id = ? AND type = ? AND (metric = ? OR metric IS NULL) AND acknowledged_at IS NULL AND resolved_at IS NULL
		ORDER BY fired_at DESC LIMIT 1`
	row := db.QueryRowContext(ctx, query, hostID, alertType, metric)
	var a Alert
	err := scanAlertRow(row, &a, nil)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func scanAlertRow(row interface{ Scan(...any) error }, a *Alert, hostName *string) error {
	var firedAt int64
	var ackedAt, resolvedAt, silencedAt sql.NullInt64
	if err := row.Scan(&a.ID, &a.HostID, &a.Type, &a.Metric, &a.Severity, &a.Message, &a.Value, &a.Threshold, &firedAt, &ackedAt, &resolvedAt, &silencedAt, hostName); err != nil {
		return err
	}
	a.FiredAt = time.Unix(firedAt, 0)
	if ackedAt.Valid {
		t := time.Unix(ackedAt.Int64, 0)
		a.AcknowledgedAt = &t
	}
	if resolvedAt.Valid {
		t := time.Unix(resolvedAt.Int64, 0)
		a.ResolvedAt = &t
	}
	if silencedAt.Valid {
		t := time.Unix(silencedAt.Int64, 0)
		a.SilencedUntil = &t
	}
	return nil
}

func (db *DB) UpdateAlert(ctx context.Context, alert *Alert) error {
	_, err := db.ExecContext(ctx, `
		UPDATE alerts SET value = ?, threshold = ?, message = ?, fired_at = ?
		WHERE id = ?
	`, alert.Value, alert.Threshold, alert.Message, alert.FiredAt.Unix(), alert.ID)
	return err
}

func (db *DB) AcknowledgeAlert(ctx context.Context, alertID int64) error {
	_, err := db.ExecContext(ctx, `
		UPDATE alerts SET acknowledged_at = ? WHERE id = ?
	`, time.Now().Unix(), alertID)
	return err
}

func (db *DB) SilenceAlert(ctx context.Context, alertID int64, duration time.Duration) error {
	until := time.Now().Add(duration).Unix()
	_, err := db.ExecContext(ctx, `
		UPDATE alerts SET silenced_until = ? WHERE id = ?
	`, until, alertID)
	return err
}

func (db *DB) GetAlerts(ctx context.Context, hostID int64) ([]Alert, error) {
	query := `SELECT id, host_id, type, metric, severity, message, value, threshold, fired_at, acknowledged_at, resolved_at, silenced_until
		FROM alerts WHERE host_id = ? ORDER BY fired_at DESC`
	rows, err := db.QueryContext(ctx, query, hostID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var alerts []Alert
	for rows.Next() {
		var a Alert
		if err := scanAlertRow(rows, &a, nil); err != nil {
			return nil, err
		}
		alerts = append(alerts, a)
	}
	return alerts, nil
}

type AlertWithHost struct {
	Alert
	HostName string
}

func (db *DB) GetAllAlerts(ctx context.Context) ([]AlertWithHost, error) {
	query := `SELECT a.id, a.host_id, a.type, a.metric, a.severity, a.message, a.value, a.threshold, a.fired_at, a.acknowledged_at, a.resolved_at, a.silenced_until, COALESCE(h.name, '')
		FROM alerts a LEFT JOIN hosts h ON h.id = a.host_id
		ORDER BY a.fired_at DESC`
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var alerts []AlertWithHost
	for rows.Next() {
		var a AlertWithHost
		var hostName string
		if err := scanAlertRow(rows, &a.Alert, &hostName); err != nil {
			return nil, err
		}
		a.HostName = hostName
		alerts = append(alerts, a)
	}
	return alerts, nil
}

func (db *DB) GetAlertsByProject(ctx context.Context, projectID *int64) ([]AlertWithHost, error) {
	query := `SELECT a.id, a.host_id, a.type, a.metric, a.severity, a.message, a.value, a.threshold, a.fired_at, a.acknowledged_at, a.resolved_at, a.silenced_until, COALESCE(h.name, '')
		FROM alerts a LEFT JOIN hosts h ON h.id = a.host_id`
	args := []interface{}{}
	if projectID != nil {
		query += ` WHERE h.project_id = ?`
		args = append(args, *projectID)
	}
	query += ` ORDER BY a.fired_at DESC`

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var alerts []AlertWithHost
	for rows.Next() {
		var a AlertWithHost
		var hostName string
		if err := scanAlertRow(rows, &a.Alert, &hostName); err != nil {
			return nil, err
		}
		a.HostName = hostName
		alerts = append(alerts, a)
	}
	return alerts, nil
}

func (db *DB) AcknowledgeAllAlerts(ctx context.Context, severity string) error {
	query := `UPDATE alerts SET acknowledged_at = ? WHERE acknowledged_at IS NULL`
	args := []interface{}{time.Now().Unix()}
	if severity != "" && severity != "all" {
		query += ` AND severity = ?`
		args = append(args, severity)
	}
	_, err := db.ExecContext(ctx, query, args...)
	return err
}

func (db *DB) SilenceAllAlerts(ctx context.Context, severity string, duration time.Duration) error {
	until := time.Now().Add(duration).Unix()
	query := `UPDATE alerts SET silenced_until = ? WHERE acknowledged_at IS NULL AND resolved_at IS NULL`
	args := []interface{}{until}
	if severity != "" && severity != "all" {
		query += ` AND severity = ?`
		args = append(args, severity)
	}
	_, err := db.ExecContext(ctx, query, args...)
	return err
}

func (db *DB) DeleteAllAlerts(ctx context.Context, severity string) error {
	query := `DELETE FROM alerts WHERE acknowledged_at IS NOT NULL OR resolved_at IS NOT NULL`
	args := []interface{}{}
	if severity != "" && severity != "all" {
		query += ` AND severity = ?`
		args = append(args, severity)
	}
	_, err := db.ExecContext(ctx, query, args...)
	return err
}

func scanAlertRuleRow(row interface{ Scan(...any) error }, r *AlertRule) error {
	var hostID sql.NullInt64
	var createdAt, updatedAt int64
	if err := row.Scan(&r.ID, &r.Metric, &r.Scope, &hostID, &r.Warning, &r.Critical, &r.Below, &r.Enabled, &createdAt, &updatedAt); err != nil {
		return err
	}
	if hostID.Valid {
		r.HostID = &hostID.Int64
	}
	r.CreatedAt = time.Unix(createdAt, 0)
	r.UpdatedAt = time.Unix(updatedAt, 0)
	return nil
}

func (db *DB) GetAlertRules(ctx context.Context) ([]AlertRule, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, metric, scope, host_id, warning, critical, below, enabled, created_at, updated_at FROM alert_rules ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var rules []AlertRule
	for rows.Next() {
		var r AlertRule
		if err := scanAlertRuleRow(rows, &r); err != nil {
			return nil, err
		}
		rules = append(rules, r)
	}
	return rules, nil
}

func (db *DB) GetAlertRule(ctx context.Context, id int64) (*AlertRule, error) {
	row := db.QueryRowContext(ctx, `SELECT id, metric, scope, host_id, warning, critical, below, enabled, created_at, updated_at FROM alert_rules WHERE id = ?`, id)
	var r AlertRule
	if err := scanAlertRuleRow(row, &r); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return &r, nil
}

func (db *DB) CreateAlertRule(ctx context.Context, rule *AlertRule) (int64, error) {
	now := time.Now().Unix()
	rule.CreatedAt = time.Unix(now, 0)
	rule.UpdatedAt = time.Unix(now, 0)
	var hostID *int64
	if rule.HostID != nil {
		hostID = rule.HostID
	}
	res, err := db.ExecContext(ctx, `INSERT INTO alert_rules (metric, scope, host_id, warning, critical, below, enabled, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rule.Metric, rule.Scope, hostID, rule.Warning, rule.Critical, rule.Below, rule.Enabled, now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (db *DB) UpdateAlertRule(ctx context.Context, rule *AlertRule) error {
	now := time.Now().Unix()
	rule.UpdatedAt = time.Unix(now, 0)
	var hostID *int64
	if rule.HostID != nil {
		hostID = rule.HostID
	}
	_, err := db.ExecContext(ctx, `UPDATE alert_rules SET metric = ?, scope = ?, host_id = ?, warning = ?, critical = ?, below = ?, enabled = ?, updated_at = ? WHERE id = ?`,
		rule.Metric, rule.Scope, hostID, rule.Warning, rule.Critical, rule.Below, rule.Enabled, now, rule.ID)
	return err
}

func (db *DB) DeleteAlertRule(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM alert_rules WHERE id = ?`, id)
	return err
}

func (db *DB) GetAlertRulesForMetric(ctx context.Context, metric string) ([]AlertRule, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, metric, scope, host_id, warning, critical, below, enabled, created_at, updated_at FROM alert_rules WHERE metric = ? AND enabled = 1 ORDER BY scope, host_id`, metric)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var rules []AlertRule
	for rows.Next() {
		var r AlertRule
		if err := scanAlertRuleRow(rows, &r); err != nil {
			return nil, err
		}
		rules = append(rules, r)
	}
	return rules, nil
}

func scanNotificationChannelRow(row interface{ Scan(...any) error }, c *NotificationChannel) error {
	var createdAt, updatedAt int64
	if err := row.Scan(&c.ID, &c.Name, &c.Type, &c.Config, &c.Enabled, &createdAt, &updatedAt); err != nil {
		return err
	}
	c.CreatedAt = time.Unix(createdAt, 0)
	c.UpdatedAt = time.Unix(updatedAt, 0)
	return nil
}

func (db *DB) GetNotificationChannels(ctx context.Context) ([]NotificationChannel, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, name, type, config, enabled, created_at, updated_at FROM notification_channels ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var channels []NotificationChannel
	for rows.Next() {
		var c NotificationChannel
		if err := scanNotificationChannelRow(rows, &c); err != nil {
			return nil, err
		}
		channels = append(channels, c)
	}
	return channels, nil
}

func (db *DB) GetNotificationChannel(ctx context.Context, id int64) (*NotificationChannel, error) {
	row := db.QueryRowContext(ctx, `SELECT id, name, type, config, enabled, created_at, updated_at FROM notification_channels WHERE id = ?`, id)
	var c NotificationChannel
	if err := scanNotificationChannelRow(row, &c); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return &c, nil
}

func (db *DB) CreateNotificationChannel(ctx context.Context, channel *NotificationChannel) (int64, error) {
	now := time.Now().Unix()
	channel.CreatedAt = time.Unix(now, 0)
	channel.UpdatedAt = time.Unix(now, 0)
	res, err := db.ExecContext(ctx, `INSERT INTO notification_channels (name, type, config, enabled, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		channel.Name, channel.Type, channel.Config, channel.Enabled, now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (db *DB) UpdateNotificationChannel(ctx context.Context, channel *NotificationChannel) error {
	now := time.Now().Unix()
	channel.UpdatedAt = time.Unix(now, 0)
	_, err := db.ExecContext(ctx, `UPDATE notification_channels SET name = ?, type = ?, config = ?, enabled = ?, updated_at = ? WHERE id = ?`,
		channel.Name, channel.Type, channel.Config, channel.Enabled, now, channel.ID)
	return err
}

func (db *DB) DeleteNotificationChannel(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM notification_channels WHERE id = ?`, id)
	return err
}

func (db *DB) GetEnabledNotificationChannels(ctx context.Context) ([]NotificationChannel, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, name, type, config, enabled, created_at, updated_at FROM notification_channels WHERE enabled = 1 ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var channels []NotificationChannel
	for rows.Next() {
		var c NotificationChannel
		if err := scanNotificationChannelRow(rows, &c); err != nil {
			return nil, err
		}
		channels = append(channels, c)
	}
	return channels, nil
}

func scanAlertConfigRow(row interface{ Scan(...any) error }, c *AlertConfig) error {
	var createdAt, updatedAt int64
	if err := row.Scan(&c.ID, &c.CollectionFailureThreshold, &c.Webhooks, &createdAt, &updatedAt); err != nil {
		return err
	}
	c.CreatedAt = time.Unix(createdAt, 0)
	c.UpdatedAt = time.Unix(updatedAt, 0)
	return nil
}

func (db *DB) GetAlertConfig(ctx context.Context) (*AlertConfig, error) {
	row := db.QueryRowContext(ctx, `SELECT id, collection_failure_threshold, webhooks, created_at, updated_at FROM alert_config LIMIT 1`)
	var c AlertConfig
	if err := scanAlertConfigRow(row, &c); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return &c, nil
}

func (db *DB) UpdateAlertConfig(ctx context.Context, config *AlertConfig) error {
	now := time.Now().Unix()
	config.UpdatedAt = time.Unix(now, 0)
	_, err := db.ExecContext(ctx, `UPDATE alert_config SET collection_failure_threshold = ?, webhooks = ?, updated_at = ? WHERE id = ?`,
		config.CollectionFailureThreshold, config.Webhooks, now, config.ID)
	return err
}

func (db *DB) CreateAlertConfig(ctx context.Context, config *AlertConfig) (int64, error) {
	now := time.Now().Unix()
	config.CreatedAt = time.Unix(now, 0)
	config.UpdatedAt = time.Unix(now, 0)
	res, err := db.ExecContext(ctx, `INSERT INTO alert_config (collection_failure_threshold, webhooks, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		config.CollectionFailureThreshold, config.Webhooks, now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (db *DB) EnsureAlertConfig(ctx context.Context) error {
	_, err := db.GetAlertConfig(ctx)
	if err == sql.ErrNoRows {
		// Create default config
		_, err := db.CreateAlertConfig(ctx, &AlertConfig{
			CollectionFailureThreshold: 3,
			Webhooks:                   "[]",
		})
		return err
	}
	return err
}
