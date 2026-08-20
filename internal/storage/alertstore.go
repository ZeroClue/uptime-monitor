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
	var firedAt int64
	var ackedAt, resolvedAt, silencedAt sql.NullInt64
	err := row.Scan(&a.ID, &a.HostID, &a.Type, &a.Metric, &a.Severity, &a.Message, &a.Value, &a.Threshold, &firedAt, &ackedAt, &resolvedAt, &silencedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
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
	return &a, nil
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
		var firedAt int64
		var ackedAt, resolvedAt, silencedAt sql.NullInt64
		if err := rows.Scan(&a.ID, &a.HostID, &a.Type, &a.Metric, &a.Severity, &a.Message, &a.Value, &a.Threshold, &firedAt, &ackedAt, &resolvedAt, &silencedAt); err != nil {
			return nil, err
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
		var firedAt int64
		var ackedAt, resolvedAt, silencedAt sql.NullInt64
		if err := rows.Scan(&a.ID, &a.HostID, &a.Type, &a.Metric, &a.Severity, &a.Message, &a.Value, &a.Threshold, &firedAt, &ackedAt, &resolvedAt, &silencedAt, &a.HostName); err != nil {
			return nil, err
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
		alerts = append(alerts, a)
	}
	return alerts, nil
}