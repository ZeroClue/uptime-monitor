package collector

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFakeProc(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mustWrite := func(rel, content string) {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	mustWrite("loadavg", "0.10 0.20 0.30 1/100 1234\n")
	mustWrite("meminfo", strings.Join([]string{
		"MemTotal:       1000000 kB",
		"MemFree:         400000 kB",
		"MemAvailable:    600000 kB",
		"Cached:          100000 kB",
		"SwapTotal:             0 kB",
		"SwapFree:              0 kB",
	}, "\n")+"\n")
	mustWrite("stat", "cpu  10 0 5 80 5 0 0 0 0 0\n"+
		"cpu0 6 0 3 40 5 0 0 0 0 0\n"+
		"cpu1 4 0 2 40 0 0 0 0 0 0\n"+
		"intr 0\n")
	mustWrite("uptime", "3600.25 7200.50\n")
	mustWrite("diskstats", "   8       0 sda 100 0 2000 0 50 0 1000 0 0 0 0 0 0 0 0\n"+
		"   7       0 loop0 1 0 2 0 1 0 2 0 0 0 0 0 0 0 0\n")
	mustWrite("net/dev",
		"Inter-|   Receive                                                |  Transmit\n"+
			" face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed\n"+
			"    lo: 1000 10 0 0 0 0 0 0 1000 10 0 0 0 0 0 0\n"+
			"  eth0: 5000 50 1 0 0 0 0 0 4000 40 2 0 0 0 0 0\n")
	mustWrite("net/tcp", "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"+
		"   0: 00000000:0016 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 0\n"+
		"   1: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 0\n")
	if err := os.MkdirAll(filepath.Join(root, "1234", "fd"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestLocalProcfsCollector_FakeProcTree(t *testing.T) {
	p := NewLocalProcfsCollector(WithLocalProcRoot(writeFakeProc(t)))
	host := Host{ID: 42, Name: "self", Connection: "local"}

	samples, err := p.Collect(context.Background(), host)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	got := map[string]float64{}
	for _, s := range samples {
		got[s.Metric] = s.Value
	}

	expect := map[string]float64{
		"cpu.load_1m":            0.10,
		"cpu.load_15m":           0.30,
		"uptime.seconds":         3600.25,
		"mem.total_bytes":        1024000000,
		"mem.used_bytes":         409600000, // total - available
		"system.process_count":   0,         // asserted separately below (dynamic)
		"net.eth0.rx_bytes":      5000,
		"net.eth0.tx_packets":    40,
		"net.tcp.LISTEN":         2,
		"diskio.sda.read_ops":    100,
		"diskio.sda.write_bytes": 1000 * 512, // sectors * 512
	}
	for metric, want := range expect {
		if metric == "system.process_count" {
			continue // PID count depends on the test process itself
		}
		v, ok := got[metric]
		if !ok {
			t.Errorf("missing sample %q", metric)
			continue
		}
		if v != want {
			t.Errorf("%s = %v, want %v", metric, v, want)
		}
	}

	if got["system.process_count"] <= 0 {
		t.Errorf("expected positive process_count, got %v", got["system.process_count"])
	}
	if _, ok := got["net.lo.rx_bytes"]; ok {
		t.Error("loopback interface should be skipped")
	}
	if _, ok := got["diskio.loop0.read_ops"]; ok {
		t.Error("virtual disk loop0 should be filtered")
	}

	// CPU percentages derived from stat fixture (total = 100)
	if got["cpu.user_pct"] != 10 || got["cpu.idle_pct"] != 80 || got["cpu.iowait_pct"] != 5 {
		t.Errorf("aggregate cpu pcts wrong: %+v", got)
	}
	// Per-core pcts: core0 total = 6+3+40+5 = 54, core1 total = 4+2+40 = 46
	if got["cpu.core.0.user_pct"] != 6.0/54*100 || got["cpu.core.1.user_pct"] != 4.0/46*100 {
		t.Errorf("per-core cpu pcts wrong: %v, %v", got["cpu.core.0.user_pct"], got["cpu.core.1.user_pct"])
	}
}

func TestLocalProcfsCollector_RejectsNonLocalHost(t *testing.T) {
	p := NewLocalProcfsCollector(WithLocalProcRoot(writeFakeProc(t)))
	host := Host{ID: 1, Name: "remote", Connection: "ssh"}

	if _, err := p.Collect(context.Background(), host); err == nil {
		t.Fatal("expected error for connection=ssh, got nil")
	}
}

func TestLocalProcfsCollector_MissingFilesFails(t *testing.T) {
	p := NewLocalProcfsCollector(WithLocalProcRoot(t.TempDir()))
	host := Host{ID: 1, Name: "self", Connection: "local"}

	if _, err := p.Collect(context.Background(), host); err == nil {
		t.Fatal("expected error when /proc files missing, got nil")
	}
}

func TestParseNetDev_ShortOutputDoesNotPanic(t *testing.T) {
	for _, out := range []string{"", "header only", "h1\nh2"} {
		info := parseNetDev(out)
		if len(info.Interfaces) != 0 {
			t.Errorf("expected no interfaces for %q, got %v", out, info.Interfaces)
		}
	}
}

func TestChain_LocalHostPrefersLocalCollector(t *testing.T) {
	local := NewLocalProcfsCollector(WithLocalProcRoot(writeFakeProc(t)))
	chain := NewChain(
		local,
		&failingCollector{name: "psutil"},
		&mockCollector{name: "ssh-fallback", samples: []Sample{{Metric: "from-ssh", Value: 1, Timestamp: time.Now()}}},
	)

	samples, err := chain.Collect(context.Background(), Host{ID: 7, Name: "self", Connection: "local"})
	if err != nil {
		t.Fatalf("expected local collector to satisfy a connection=local host, got error: %v", err)
	}
	for _, s := range samples {
		if s.Metric == "from-ssh" {
			t.Fatal("SSH fallback should not have run for a local host")
		}
	}
}
