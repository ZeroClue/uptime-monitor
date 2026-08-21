package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

func scanProjectRow(row interface{ Scan(...any) error }, p *Project) error {
	var hostIDsJSON sql.NullString
	var createdAt, updatedAt int64
	if err := row.Scan(&p.ID, &p.Name, &p.Type, &p.TagQuery, &hostIDsJSON, &p.OwnerID, &p.IsolationLevel, &p.IsDefault, &createdAt, &updatedAt); err != nil {
		return err
	}
	if hostIDsJSON.Valid {
		json.Unmarshal([]byte(hostIDsJSON.String), &p.HostIDs)
	}
	p.CreatedAt = time.Unix(createdAt, 0)
	p.UpdatedAt = time.Unix(updatedAt, 0)
	return nil
}

func (db *DB) GetProjects(ctx context.Context) ([]Project, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, name, type, tag_query, host_ids, owner_id, isolation_level, is_default, created_at, updated_at FROM projects`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var projects []Project
	for rows.Next() {
		var p Project
		if err := scanProjectRow(rows, &p); err != nil {
			return nil, err
		}
		projects = append(projects, p)
	}
	return projects, nil
}

func (db *DB) GetProject(ctx context.Context, id int64) (*Project, error) {
	row := db.QueryRowContext(ctx, `SELECT id, name, type, tag_query, host_ids, owner_id, isolation_level, is_default, created_at, updated_at FROM projects WHERE id = ?`, id)
	var p Project
	if err := scanProjectRow(row, &p); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return &p, nil
}

func (db *DB) CreateProject(ctx context.Context, project *Project) (int64, error) {
	now := time.Now().Unix()

	hostIDsJSON := "[]"
	if len(project.HostIDs) > 0 {
		data, _ := json.Marshal(project.HostIDs)
		hostIDsJSON = string(data)
	}

	isDefault := 0
	if project.IsDefault {
		isDefault = 1
	}

	res, err := db.ExecContext(ctx, `
		INSERT INTO projects (name, type, tag_query, host_ids, owner_id, isolation_level, is_default, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, project.Name, project.Type, project.TagQuery, hostIDsJSON, project.OwnerID, project.IsolationLevel, isDefault, now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (db *DB) UpdateProject(ctx context.Context, project *Project) error {
	now := time.Now().Unix()

	hostIDsJSON := "[]"
	if len(project.HostIDs) > 0 {
		data, _ := json.Marshal(project.HostIDs)
		hostIDsJSON = string(data)
	}

	isDefault := 0
	if project.IsDefault {
		isDefault = 1
	}

	_, err := db.ExecContext(ctx, `
		UPDATE projects SET
			name = ?, type = ?, tag_query = ?, host_ids = ?, owner_id = ?, isolation_level = ?, is_default = ?, updated_at = ?
		WHERE id = ?
	`, project.Name, project.Type, project.TagQuery, hostIDsJSON, project.OwnerID, project.IsolationLevel, isDefault, now, project.ID)
	return err
}

func (db *DB) DeleteProject(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, id)
	return err
}

func (db *DB) GetProjectHosts(ctx context.Context, project Project) ([]Host, error) {
	var hosts []Host
	if project.Type == "explicit" {
		for _, id := range project.HostIDs {
			row := db.QueryRowContext(ctx, `SELECT id, name, connection, endpoint, port, user, key_path, sudo, timeout, proxy_jump, tags, collector_preference FROM hosts WHERE id = ?`, id)
			var h Host
			var tagsJSON string
			if err := row.Scan(&h.ID, &h.Name, &h.Connection, &h.Endpoint, &h.Port, &h.User, &h.KeyPath, &h.Sudo, &h.TimeoutRaw, &h.ProxyJump, &tagsJSON, &h.CollectorPreference); err == nil {
				h.Timeout = time.Duration(h.TimeoutRaw)
				h.Tags = parseTags(tagsJSON)
				hosts = append(hosts, h)
			}
		}
	} else if project.Type == "tag_query" {
		allHosts, err := db.GetHosts()
		if err != nil {
			return nil, err
		}
		for _, h := range allHosts {
			if matchesTagQuery(h.Tags, project.TagQuery) {
				hosts = append(hosts, h)
			}
		}
	}
	return hosts, nil
}

func (db *DB) GetProjectHealth(ctx context.Context, project Project, hostStatuses map[int64]HostStatusInfo) (string, error) {
	hosts, err := db.GetProjectHosts(ctx, project)
	if err != nil {
		return "unknown", err
	}
	if len(hosts) == 0 {
		return "ok", nil
	}

	worst := 0
	for _, h := range hosts {
		status := hostStatuses[h.ID]
		hostStatus := 0
		if status.ConsecutiveFails >= 3 {
			hostStatus = 3
		} else if status.ConsecutiveFails > 0 {
			hostStatus = 2
		}
		if hostStatus > worst {
			worst = hostStatus
		}
	}

	switch worst {
	case 3:
		return "down", nil
	case 2:
		return "critical", nil
	case 1:
		return "warning", nil
	default:
		return "ok", nil
	}
}
