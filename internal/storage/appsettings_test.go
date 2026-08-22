package storage

import (
	"context"
	"testing"
)

func TestAppSettings_RoundtripAndDefaults(t *testing.T) {
	db := newTestProjectDB(t)
	ctx := context.Background()

	v, err := db.GetSetting(ctx, "log_level")
	if err != nil || v != "" {
		t.Fatalf("unset key: want empty, got %q err=%v", v, err)
	}

	if err := db.SetSetting(ctx, "log_level", "debug"); err != nil {
		t.Fatal(err)
	}
	v, err = db.GetSetting(ctx, "log_level")
	if err != nil || v != "debug" {
		t.Fatalf("after set: want debug, got %q err=%v", v, err)
	}

	// Same key upserts instead of duplicating rows.
	if err := db.SetSetting(ctx, "log_level", "warn"); err != nil {
		t.Fatal(err)
	}
	v, _ = db.GetSetting(ctx, "log_level")
	if v != "warn" {
		t.Fatalf("upsert failed: %q", v)
	}

	// Unknown keys are isolated.
	if v, _ := db.GetSetting(ctx, "other"); v != "" {
		t.Errorf("cross-key leak: %q", v)
	}
}
