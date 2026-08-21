package storage

import (
	"context"
	"database/sql"
	"time"
)

func scanUserRow(row interface{ Scan(...any) error }, u *User) error {
	var createdAt, updatedAt int64
	if err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Email, &u.Role, &createdAt, &updatedAt); err != nil {
		return err
	}
	u.CreatedAt = time.Unix(createdAt, 0)
	u.UpdatedAt = time.Unix(updatedAt, 0)
	return nil
}

func (db *DB) GetUser(ctx context.Context, id int64) (*User, error) {
	row := db.QueryRowContext(ctx, `SELECT id, username, password_hash, email, role, created_at, updated_at FROM users WHERE id = ?`, id)
	var u User
	if err := scanUserRow(row, &u); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return &u, nil
}

func (db *DB) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	row := db.QueryRowContext(ctx, `SELECT id, username, password_hash, email, role, created_at, updated_at FROM users WHERE username = ?`, username)
	var u User
	if err := scanUserRow(row, &u); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return &u, nil
}

func (db *DB) CreateUser(ctx context.Context, user *User) (int64, error) {
	now := time.Now().Unix()
	res, err := db.ExecContext(ctx, `INSERT INTO users (username, password_hash, email, role, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		user.Username, user.PasswordHash, user.Email, user.Role, now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (db *DB) UpdateUser(ctx context.Context, user *User) error {
	now := time.Now().Unix()
	_, err := db.ExecContext(ctx, `UPDATE users SET username = ?, password_hash = ?, email = ?, role = ?, updated_at = ? WHERE id = ?`,
		user.Username, user.PasswordHash, user.Email, user.Role, now, user.ID)
	return err
}

func (db *DB) DeleteUser(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	return err
}

func (db *DB) GetUsers(ctx context.Context) ([]User, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, username, password_hash, email, role, created_at, updated_at FROM users ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []User
	for rows.Next() {
		var u User
		if err := scanUserRow(rows, &u); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, nil
}

func scanProjectMemberRow(row interface{ Scan(...any) error }, m *ProjectMember) error {
	var createdAt int64
	if err := row.Scan(&m.ID, &m.ProjectID, &m.UserID, &m.Role, &createdAt); err != nil {
		return err
	}
	m.CreatedAt = time.Unix(createdAt, 0)
	return nil
}

func (db *DB) GetProjectMembers(ctx context.Context, projectID int64) ([]ProjectMember, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, project_id, user_id, role, created_at FROM project_members WHERE project_id = ?`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var members []ProjectMember
	for rows.Next() {
		var m ProjectMember
		if err := scanProjectMemberRow(rows, &m); err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	return members, nil
}

func (db *DB) AddProjectMember(ctx context.Context, projectID, userID int64, role string) (int64, error) {
	now := time.Now().Unix()
	res, err := db.ExecContext(ctx, `INSERT INTO project_members (project_id, user_id, role, created_at) VALUES (?, ?, ?, ?)`,
		projectID, userID, role, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (db *DB) RemoveProjectMember(ctx context.Context, projectID, userID int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM project_members WHERE project_id = ? AND user_id = ?`, projectID, userID)
	return err
}

func (db *DB) UpdateProjectMemberRole(ctx context.Context, projectID, userID int64, role string) error {
	_, err := db.ExecContext(ctx, `UPDATE project_members SET role = ? WHERE project_id = ? AND user_id = ?`, role, projectID, userID)
	return err
}

func (db *DB) IsProjectMember(ctx context.Context, projectID, userID int64) (bool, error) {
	var count int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM project_members WHERE project_id = ? AND user_id = ?`, projectID, userID).Scan(&count)
	return count > 0, err
}
