package storage

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"time"
)

const tokenBytes = 32

func generateToken() (string, string, error) {
	bytes := make([]byte, tokenBytes)
	if _, err := rand.Read(bytes); err != nil {
		return "", "", err
	}
	token := hex.EncodeToString(bytes)
	hash := hashToken(token)
	return token, hash, nil
}

func hashToken(token string) string {
	// Simple hash for storage - in production use bcrypt or argon2
	bytes := []byte(token)
	hash := make([]byte, 32)
	for i, b := range bytes {
		hash[i%32] ^= b
	}
	return hex.EncodeToString(hash)
}

func scanAPITokenRow(row interface{ Scan(...any) error }, t *APIToken) error {
	var projectID sql.NullInt64
	var expiresAt, lastUsedAt, createdAt, updatedAt sql.NullInt64
	if err := row.Scan(&t.ID, &t.Name, &t.TokenHash, &projectID, &t.Scopes, &expiresAt, &lastUsedAt, &createdAt, &updatedAt); err != nil {
		return err
	}
	if projectID.Valid {
		t.ProjectID = &projectID.Int64
	}
	if expiresAt.Valid {
		t.ExpiresAt = sql.NullTime{Time: time.Unix(expiresAt.Int64, 0), Valid: true}
	}
	if t.LastUsedAt.Valid {
		t.LastUsedAt.Time = time.Unix(t.LastUsedAt.Time.Unix(), 0)
	}
	if createdAt.Valid {
		t.CreatedAt = sql.NullTime{Time: time.Unix(createdAt.Int64, 0), Valid: true}
	}
	if updatedAt.Valid {
		t.UpdatedAt = sql.NullTime{Time: time.Unix(updatedAt.Int64, 0), Valid: true}
	}
	return nil
}

func (db *DB) CreateAPIToken(ctx context.Context, token *APIToken) (string, int64, error) {
	plainToken, hash, err := generateToken()
	if err != nil {
		return "", 0, err
	}

	now := time.Now()
	token.TokenHash = hash
	token.CreatedAt = sql.NullTime{Time: now, Valid: true}
	token.UpdatedAt = sql.NullTime{Time: now, Valid: true}

	var expiresAt sql.NullInt64
	if token.ExpiresAt.Valid {
		expiresAt = sql.NullInt64{Int64: token.ExpiresAt.Time.Unix(), Valid: true}
	}

	res, err := db.ExecContext(ctx, `
		INSERT INTO api_tokens (name, token_hash, project_id, scopes, expires_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, token.Name, hash, token.ProjectID, token.Scopes, expiresAt, now.Unix(), now.Unix())
	if err != nil {
		return "", 0, err
	}
	id, err := res.LastInsertId()
	return plainToken, id, nil
}

func (db *DB) GetAPIToken(ctx context.Context, id int64) (*APIToken, error) {
	row := db.QueryRowContext(ctx, `SELECT id, name, token_hash, project_id, scopes, expires_at, last_used_at, created_at, updated_at FROM api_tokens WHERE id = ?`, id)
	var t APIToken
	if err := scanAPITokenRow(row, &t); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return &t, nil
}

func (db *DB) GetAPITokenByHash(ctx context.Context, hash string) (*APIToken, error) {
	row := db.QueryRowContext(ctx, `SELECT id, name, token_hash, project_id, scopes, expires_at, last_used_at, created_at, updated_at FROM api_tokens WHERE token_hash = ?`, hash)
	var t APIToken
	if err := scanAPITokenRow(row, &t); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return &t, nil
}

func (db *DB) GetAPITokens(ctx context.Context) ([]APIToken, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, name, token_hash, project_id, scopes, expires_at, last_used_at, created_at, updated_at FROM api_tokens ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tokens := make([]APIToken, 0)
	for rows.Next() {
		var t APIToken
		if err := scanAPITokenRow(rows, &t); err != nil {
			return nil, err
		}
		tokens = append(tokens, t)
	}
	return tokens, nil
}

func (db *DB) UpdateAPIToken(ctx context.Context, token *APIToken) error {
	token.UpdatedAt = sql.NullTime{Time: time.Now(), Valid: true}

	var expiresAt sql.NullInt64
	if token.ExpiresAt.Valid {
		expiresAt = sql.NullInt64{Int64: token.ExpiresAt.Time.Unix(), Valid: true}
	}

	var projectID *int64
	if token.ProjectID != nil {
		projectID = token.ProjectID
	}

	_, err := db.ExecContext(ctx, `
		UPDATE api_tokens SET name = ?, project_id = ?, scopes = ?, expires_at = ?, updated_at = ? WHERE id = ?
	`, token.Name, projectID, token.Scopes, expiresAt, time.Now().Unix(), token.ID)
	return err
}

func (db *DB) DeleteAPIToken(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM api_tokens WHERE id = ?`, id)
	return err
}

func (db *DB) RecordTokenUsage(ctx context.Context, id int64) error {
	now := time.Now().Unix()
	_, err := db.ExecContext(ctx, `UPDATE api_tokens SET last_used_at = ? WHERE id = ?`, now, id)
	return err
}

func (db *DB) GetAPITokenByName(ctx context.Context, name string) (*APIToken, error) {
	row := db.QueryRowContext(ctx, `SELECT id, name, token_hash, project_id, scopes, expires_at, last_used_at, created_at, updated_at FROM api_tokens WHERE name = ?`, name)
	var t APIToken
	if err := scanAPITokenRow(row, &t); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return &t, nil
}