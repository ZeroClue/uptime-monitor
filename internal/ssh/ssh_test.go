package ssh

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLimitedWriter_BoundsOutput(t *testing.T) {
	var buf bytes.Buffer
	lw := &limitedWriter{w: &buf, n: 5}

	n, err := lw.Write([]byte("hello world"))
	if err == nil {
		t.Fatal("expected error once limit exceeded")
	}
	if n != 11 {
		t.Errorf("Write should report full len even when truncated, got %d", n)
	}
	if buf.String() != "hello" {
		t.Errorf("buffer: want %q got %q", "hello", buf.String())
	}

	// After tripping, further writes fail without growing the buffer.
	if _, err := lw.Write([]byte("more")); err == nil {
		t.Error("expected error on write past tripped limit")
	}
	if buf.Len() != 5 {
		t.Errorf("buffer grew after limit: %d bytes", buf.Len())
	}
}

func TestLimitedWriter_UnderLimit(t *testing.T) {
	var buf bytes.Buffer
	lw := &limitedWriter{w: &buf, n: 100}
	for _, chunk := range []string{"a", "bb", "ccc"} {
		if _, err := lw.Write([]byte(chunk)); err != nil {
			t.Fatalf("write %q: %v", chunk, err)
		}
	}
	if buf.String() != strings.Repeat("", 0)+"abbccc" {
		t.Errorf("buffer: %q", buf.String())
	}
}

func TestLimitedWriter_ZeroLimitFailsImmediately(t *testing.T) {
	var buf bytes.Buffer
	lw := &limitedWriter{w: &buf, n: 0}
	if _, err := lw.Write([]byte("x")); err == nil {
		t.Error("expected error with zero limit")
	}
	if buf.Len() != 0 {
		t.Errorf("buffer should stay empty, got %q", buf.String())
	}
}

func TestKnownHostsHasEntry(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "known_hosts")

	c := NewSSHClient(nil, nil).(*sshClient)

	if c.knownHostsHasEntry(file, "example.com", 22) {
		t.Fatal("empty file should have no entry")
	}

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
