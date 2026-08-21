package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hosts.yaml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DASHBOARD_PASSWORD", "test")
	return dir
}

func TestLoad_HostKeyPolicy(t *testing.T) {
	t.Run("defaults to strict", func(t *testing.T) {
		dir := writeConfig(t, "hosts:\n  - name: h\n    endpoint: x\n")
		cfg, err := Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.SSHHostKeyPolicy != "strict" || cfg.SSHKnownHostsFile != "/config/known_hosts" {
			t.Fatalf("got %q %q", cfg.SSHHostKeyPolicy, cfg.SSHKnownHostsFile)
		}
	})

	t.Run("accepts auto", func(t *testing.T) {
		dir := writeConfig(t, "ssh_host_key_policy: auto\nhosts: []\n")
		cfg, err := Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.SSHHostKeyPolicy != "auto" {
			t.Fatalf("got %q", cfg.SSHHostKeyPolicy)
		}
	})

	t.Run("rejects unknown policy", func(t *testing.T) {
		dir := writeConfig(t, "ssh_host_key_policy: yolo\nhosts: []\n")
		if _, err := Load(dir); err == nil {
			t.Fatal("expected error for invalid policy")
		}
	})
}
