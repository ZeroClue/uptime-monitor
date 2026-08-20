package storage

import (
	"context"
	"encoding/json"
	"time"
)

func (db *DB) GetProjects(ctx context.Context) ([]Project, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, name, type, tag_query, host_ids FROM projects`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var projects []Project
	for rows.Next() {
		var p Project
		var hostIDsJSON string
		if err := rows.Scan(&p.ID, &p.Name, &p.Type, &p.TagQuery, &hostIDsJSON); err != nil {
			return nil, err
		}
		if hostIDsJSON != "" {
			json.Unmarshal([]byte(hostIDsJSON), &p.HostIDs)
		}
		projects = append(projects, p)
	}
	return projects, nil
}

func (db *DB) GetProjectHosts(ctx context.Context, project Project) ([]Host, error) {
	var hosts []Host
	if project.Type == "explicit" {
		for _, id := range project.HostIDs {
			row := db.QueryRowContext(ctx, `SELECT id, name, connection, endpoint, port, user, key_path, sudo, timeout, proxy_jump, tags, collector_preference FROM hosts WHERE id = ?`, id)
			var h Host
			var tagsJSON string
			var timeoutRaw int64
			if err := row.Scan(&h.ID, &h.Name, &h.Connection, &h.Endpoint, &h.Port, &h.User, &h.KeyPath, &h.Sudo, &timeoutRaw, &h.ProxyJump, &tagsJSON, &h.CollectorPreference); err == nil {
				h.Timeout = time.Duration(timeoutRaw)
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
