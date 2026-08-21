package ssh

import (
	"os"
	"path/filepath"
	"testing"
)

func TestKnownHostsHasEntry(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "known_hosts")

	c := NewSSHClient(nil, nil).(*sshClient)

	if c.knownHostsHasEntry(file, "example.com", 22) {
		t.Fatal("empty file should have no entry")
	}

	// Generate real entries with ssh-keyscan against the local sshd-less
	// environment is unreliable; craft entries via ssh-keygen -F format by
	// scanning a known local host is not possible here. Instead append a
	// syntactically valid entry and confirm lookup.
	entry := "example.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIB1234567890abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ\n"
	if err := os.WriteFile(file, []byte(entry), 0o600); err != nil {
		t.Fatal(err)
	}
	if !c.knownHostsHasEntry(file, "example.com", 22) {
		t.Fatal("expected entry for example.com:22")
	}
	if c.knownHostsHasEntry(file, "other.com", 22) {
		t.Fatal("unexpected entry for other.com")
	}
}
