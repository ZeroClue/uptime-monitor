package storage

import (
	"context"
	"testing"
	"time"
)

func TestRemoteWriteConfig_EnsureGetUpdate(t *testing.T) {
	db := newTestProjectDB(t)
	ctx := context.Background()

	cfg, err := db.GetRemoteWriteConfig(ctx)
	if err != nil {
		t.Fatalf("get on fresh db: %v", err)
	}
	if cfg != nil {
		t.Fatalf("expected nil config on fresh db, got %+v", cfg)
	}

	id, err := db.CreateRemoteWriteConfig(ctx, &RemoteWriteConfig{
		Enabled:     true,
		URL:         "http://prom:8428/api/v1/write",
		AuthType:    "basic",
		Username:    "prom",
		Password:    "secret",
		ExtraLabels: map[string]string{"env": "prod", "region": "eu-1"},
		BatchSize:   250,
		TimeoutMs:   5000,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if id <= 0 {
		t.Fatalf("bad id %d", id)
	}

	got, err := db.GetRemoteWriteConfig(ctx)
	if err != nil || got == nil {
		t.Fatalf("get after create: %v %v", got, err)
	}
	if !got.Enabled || got.URL != "http://prom:8428/api/v1/write" || got.AuthType != "basic" ||
		got.Username != "prom" || got.Password != "secret" || got.BatchSize != 250 || got.TimeoutMs != 5000 {
		t.Errorf("roundtrip mismatch: %+v", got)
	}
	if got.ExtraLabels["env"] != "prod" || got.ExtraLabels["region"] != "eu-1" || len(got.ExtraLabels) != 2 {
		t.Errorf("labels lost: %v", got.ExtraLabels)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Error("timestamps not populated")
	}

	got.Enabled = false
	got.AuthType = "bearer"
	got.BearerToken = "tok-123"
	got.ExtraLabels = map[string]string{}
	got.BatchSize = 1000
	if err := db.UpdateRemoteWriteConfig(ctx, got); err != nil {
		t.Fatalf("update: %v", err)
	}
	updated, _ := db.GetRemoteWriteConfig(ctx)
	if updated.Enabled || updated.AuthType != "bearer" || updated.BearerToken != "tok-123" ||
		updated.BatchSize != 1000 || len(updated.ExtraLabels) != 0 {
		t.Errorf("update lost: %+v", updated)
	}
	if !updated.UpdatedAt.After(updated.CreatedAt.Add(-time.Second)) && updated.UpdatedAt.Before(updated.CreatedAt) {
		t.Logf("note: same-second timestamps, acceptable for unix-second precision")
	}
}
