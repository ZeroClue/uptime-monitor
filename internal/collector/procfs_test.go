package collector

import (
	"testing"
)

func TestParseLoadAvg_WithWarningLine(t *testing.T) {
	cases := []struct {
		name   string
		output string
		want   LoadAvg
	}{
		{"clean", "0.55 0.55 0.55 1/559 2388261", LoadAvg{0.55, 0.55, 0.55}},
		{"ssh-warning-prefix", "Warning: Permanently added 'x' to the list of known hosts.\n0.55 0.42 0.38 1/559 1", LoadAvg{0.55, 0.42, 0.38}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseLoadAvg(tt.output)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseLoadAvg_NotEnoughNumbers(t *testing.T) {
	if _, err := parseLoadAvg("Warning: only one 1.0 here"); err == nil {
		t.Fatal("expected error for output with <3 numbers")
	}
}

func TestParseUptime_WithWarningLine(t *testing.T) {
	cases := []struct {
		name   string
		output string
		want   float64
	}{
		{"clean", "1323257.12 2440860.93", 1323257.12},
		{"ssh-warning-prefix", "Warning: Permanently added 'x' to the list of known hosts.\r\n1323257.12 2440860.93", 1323257.12},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseUptime(tt.output)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseCPUStat_ReadsFields(t *testing.T) {
	stat, err := parseCPUStat("cpu  100 20 30 40 10 5 2\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stat.User != 100 || stat.Nice != 20 || stat.System != 30 || stat.Idle != 40 {
		t.Errorf("unexpected stat: %+v", stat)
	}
}

func TestParseCPUStat_NoCpuLine(t *testing.T) {
	if _, err := parseCPUStat("nothing here\n"); err == nil {
		t.Fatal("expected error for missing cpu line")
	}
}

func TestParsePerCoreCPU(t *testing.T) {
	output := "cpu  100 20 30 40 10 5 2\n" +
		"cpu0 50 10 10 20 5 2 1\n" +
		"cpu1 50 10 20 20 5 3 1\n" +
		"intr 12345\n"
	cores, err := parsePerCoreCPU(output)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cores) != 2 {
		t.Fatalf("got %d cores, want 2", len(cores))
	}
	if cores[0].User != 50 || cores[1].Idle != 20 {
		t.Errorf("unexpected cores: %+v", cores)
	}
}

func TestParsePerCoreCPU_NoCores(t *testing.T) {
	if _, err := parsePerCoreCPU("cpu  100 20 30 40\n"); err == nil {
		t.Fatal("expected error when no per-core lines present")
	}
}

func TestSortedCoreIDs(t *testing.T) {
	cores := map[int]CPUStat{2: {}, 0: {}, 1: {}}
	got := sortedCoreIDs(cores)
	if got[0] != 0 || got[1] != 1 || got[2] != 2 {
		t.Errorf("unsorted core ids: %v", got)
	}
}

func TestParseDiskStats(t *testing.T) {
	output := "   8       0 sda 100 50 5000 100 200 30 3000 50 0 100 200\n" +
		"   8       1 sda1 50 25 2500 50 100 15 1500 25 0 50 100\n" +
		" 259       0 nvme0n1 10 0 800 5 20 0 1600 10 0 5 15\n" +
		"   7       0 loop0 5 0 100 1 5 0 100 1 0 1 2\n"
	devs, err := parseDiskStats(output)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(devs) != 3 {
		t.Fatalf("got %d devices, want 3 (loop excluded)", len(devs))
	}
	sda := devs["sda"]
	if sda.ReadBytes != 5000*512 {
		t.Errorf("sda read_bytes = %d, want %d", sda.ReadBytes, 5000*512)
	}
	if sda.WriteBytes != 3000*512 {
		t.Errorf("sda write_bytes = %d, want %d", sda.WriteBytes, 3000*512)
	}
	if sda.ReadOps != 100 || sda.WriteOps != 200 {
		t.Errorf("sda ops = %+v", sda)
	}
	nvme := devs["nvme0n1"]
	if nvme.ReadBytes != 800*512 || nvme.WriteBytes != 1600*512 {
		t.Errorf("nvme bytes = %+v", nvme)
	}
	if _, ok := devs["loop0"]; ok {
		t.Error("loop0 should be excluded")
	}
}

func TestParseDiskStats_Empty(t *testing.T) {
	if _, err := parseDiskStats(""); err == nil {
		t.Fatal("expected error for empty diskstats output")
	}
}

func TestParseConnectionStates(t *testing.T) {
	// /proc/net/tcp format: sl local_address rem_address st tx_queue rx_queue tr tm->when retrnsmt uid timeout inode
	// st: 01=ESTABLISHED, 02=SYN_SENT, 03=SYN_RECV, 04=FIN_WAIT1, 05=FIN_WAIT2, 06=TIME_WAIT, 07=CLOSE, 08=CLOSE_WAIT, 09=LAST_ACK, 0A=LISTEN, 0B=CLOSING
	tcpOutput := "sl local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt uid timeout inode\n" +
		"   0: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 12345\n" +
		"   1: 0100007F:1F91 0A00000A:1F90 01 00000000:00000000 00:00000000 00000000  1000        0 12346\n" +
		"   2: 0100007F:1F92 0A00000A:1F90 06 00000000:00000000 00:00000000 00000000  1000        0 12347\n"
	udpOutput := "sl local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt uid timeout inode\n" +
		"   0: 00000000:0035 00000000:0000 07 00000000:00000000 00:00000000 00000000  1000        0 12348\n"

	states := parseConnectionStates(tcpOutput, "tcp")
	if states["ESTABLISHED"] != 1 || states["LISTEN"] != 1 || states["TIME_WAIT"] != 1 {
		t.Errorf("tcp states = %+v", states)
	}
	if states["total"] != 3 {
		t.Errorf("tcp total = %d", states["total"])
	}

	states = parseConnectionStates(udpOutput, "udp")
	if states["CLOSE"] != 1 || states["total"] != 1 {
		t.Errorf("udp states = %+v", states)
	}
}

func TestParseConnectionStates_Empty(t *testing.T) {
	states := parseConnectionStates("", "tcp")
	if states["total"] != 0 {
		t.Errorf("expected total=0 for empty output, got %+v", states)
	}
}

func TestParseDiskStats_MultiMount(t *testing.T) {
	output := "Filesystem     1B-blocks      Used Available Use% Mounted on\n" +
		"/dev/sda1     1000000000  300000000  700000000  30% /\n" +
		"/dev/sda2     2000000000 1000000000 1000000000  50% /home\n" +
		"tmpfs          100000000       0   100000000   0% /tmp\n"
	devs, err := parseDiskStatsMultiMount(output)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(devs) != 3 {
		t.Fatalf("got %d mounts, want 3", len(devs))
	}
	root := devs["/"]
	if root.Total != 1000000000 || root.Used != 300000000 || root.Free != 700000000 {
		t.Errorf("root = %+v", root)
	}
	home := devs["/home"]
	if home.Total != 2000000000 || home.Used != 1000000000 || home.Free != 1000000000 {
		t.Errorf("home = %+v", home)
	}
}

func TestParseDiskInodes(t *testing.T) {
	output := "Filesystem      Inodes  IUsed   IFree IUse% Mounted on\n" +
		"/dev/sda1     1000000 300000  700000   30% /\n" +
		"/dev/sda2     2000000 500000 1500000   25% /home\n"
	inodes, err := parseDiskInodes(output)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inodes["/"].InodeTotal != 1000000 || inodes["/"].InodeUsed != 300000 || inodes["/"].InodeFree != 700000 {
		t.Errorf("root inodes = %+v", inodes["/"])
	}
	if inodes["/home"].InodeTotal != 2000000 || inodes["/home"].InodeUsed != 500000 || inodes["/home"].InodeFree != 1500000 {
		t.Errorf("home inodes = %+v", inodes["/home"])
	}
}

func TestParseMemInfo_SwapFields(t *testing.T) {
	output := "MemTotal:       16384000 kB\nMemFree:         1024000 kB\nMemAvailable:     8192000 kB\nSwapTotal:       2097152 kB\nSwapFree:        1048576 kB\n"
	info, err := parseMemInfo(output)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.SwapTotal != 2097152*1024 {
		t.Errorf("SwapTotal = %d, want %d", info.SwapTotal, 2097152*1024)
	}
	if info.SwapFree != 1048576*1024 {
		t.Errorf("SwapFree = %d, want %d", info.SwapFree, 1048576*1024)
	}
}

func TestParseMemInfo_MissingSwap(t *testing.T) {
	info, err := parseMemInfo("MemTotal:       1000 kB\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.SwapTotal != 0 || info.SwapFree != 0 {
		t.Errorf("expected zero swap fields, got %+v", info)
	}
}

func TestSwapUsedPct(t *testing.T) {
	pct, ok := swapUsedPct(2097152*1024, 1048576*1024)
	if !ok {
		t.Fatal("expected ok for host with swap")
	}
	if pct != 50 {
		t.Errorf("pct = %v, want 50", pct)
	}
}

func TestSwapUsedPct_NoSwap(t *testing.T) {
	if _, ok := swapUsedPct(0, 0); ok {
		t.Fatal("expected not-ok for host with no swap")
	}
}

func TestParseProcessCount(t *testing.T) {
	output := "1\n10\ncpuinfo\nmeminfo\nself\nthread-self\n1234\n"
	got, err := parseProcessCount(output)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 3 {
		t.Errorf("count = %d, want 3 (pids 1, 10, 1234)", got)
	}
}

func TestParseProcessCount_NoPids(t *testing.T) {
	if _, err := parseProcessCount("cpuinfo\nmeminfo\n"); err == nil {
		t.Fatal("expected error when no pid entries present")
	}
}
