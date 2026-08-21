package storage

import (
	"context"
	"testing"
)

func TestRecordTokenUsage_VisibleAfterRead(t *testing.T) {
	db := newTestProjectDB(t)
	ctx := context.Background()
	plain, id, err := db.CreateAPIToken(ctx, &APIToken{Name: "dbg", Scopes: "read"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_ = plain
	if err := db.RecordTokenUsage(ctx, id); err != nil {
		t.Fatalf("record: %v", err)
	}
	var raw *int64
	if err := db.QueryRowContext(ctx, `SELECT last_used_at FROM api_tokens WHERE id=?`, id).Scan(&raw); err != nil {
		t.Fatalf("raw scan: %v", err)
	}
	t.Logf("raw last_used_at: %v", raw)

	tok, err := db.GetAPIToken(ctx, id)
	if err != nil {
		t.Fatalf("get after record: %v", err)
	}
	t.Logf("scanned LastUsedAt valid=%v time=%v", tok.LastUsedAt.Valid, tok.LastUsedAt.Time)
	if !tok.LastUsedAt.Valid {
		t.Fatal("last_used_at not visible after record")
	}
}
