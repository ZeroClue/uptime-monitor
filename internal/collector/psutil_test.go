package collector

import (
	"testing"
)

func TestConvertToSamples_SwapAndProcess(t *testing.T) {
	data := PsutilOutput{
		Mem: PsutilMem{
			Total:     16 * 1024 * 1024,
			SwapTotal: 2 * 1024 * 1024,
			SwapFree:  1 * 1024 * 1024,
			SwapUsed:  1 * 1024 * 1024,
		},
		ProcessCount: 123,
	}

	p := &PsutilCollector{}
	samples := p.convertToSamples(1, data)

	got := map[string]float64{}
	for _, s := range samples {
		got[s.Metric] = s.Value
	}

	if v := got["mem.swap_total_bytes"]; v != 2*1024*1024 {
		t.Errorf("swap_total_bytes = %v, want %v", v, 2*1024*1024)
	}
	if v := got["mem.swap_used_bytes"]; v != 1*1024*1024 {
		t.Errorf("swap_used_bytes = %v, want %v", v, 1*1024*1024)
	}
	if v := got["mem.swap_used_pct"]; v != 50 {
		t.Errorf("swap_used_pct = %v, want 50", v)
	}
	if v := got["system.process_count"]; v != 123 {
		t.Errorf("process_count = %v, want 123", v)
	}
}

func TestConvertToSamples_NoSwap_OmitsPct(t *testing.T) {
	data := PsutilOutput{
		Mem: PsutilMem{Total: 16 * 1024 * 1024},
	}

	p := &PsutilCollector{}
	samples := p.convertToSamples(1, data)

	for _, s := range samples {
		if s.Metric == "mem.swap_used_pct" {
			t.Fatal("swap_used_pct should be omitted on a no-swap host")
		}
	}
}

func TestConvertToSamples_PerCoreCPU(t *testing.T) {
	data := PsutilOutput{
		Cores: map[string]PsutilCPU{
			"1": {User: 30, System: 10, Idle: 50, Iowait: 10},
			"0": {User: 20, System: 20, Idle: 50, Iowait: 10},
		},
	}

	p := &PsutilCollector{}
	samples := p.convertToSamples(1, data)

	got := map[string]float64{}
	for _, s := range samples {
		got[s.Metric] = s.Value
	}

	if v := got["cpu.core.0.user_pct"]; v != 20 {
		t.Errorf("core 0 user_pct = %v, want 20", v)
	}
	if v := got["cpu.core.1.system_pct"]; v != 10 {
		t.Errorf("core 1 system_pct = %v, want 10", v)
	}
	if v := got["cpu.core.0.idle_pct"]; v != 50 {
		t.Errorf("core 0 idle_pct = %v, want 50", v)
	}
}

func TestConvertToSamples_DiskIO(t *testing.T) {
	data := PsutilOutput{
		DiskIO: map[string]PsutilDiskIO{
			"nvme0n1": {ReadBytes: 1000, WriteBytes: 2000, ReadOps: 10, WriteOps: 20},
			"sda":     {ReadBytes: 3000, WriteBytes: 4000, ReadOps: 30, WriteOps: 40},
		},
	}

	p := &PsutilCollector{}
	samples := p.convertToSamples(1, data)

	got := map[string]float64{}
	for _, s := range samples {
		got[s.Metric] = s.Value
	}

	if v := got["diskio.sda.read_bytes"]; v != 3000 {
		t.Errorf("sda read_bytes = %v, want 3000", v)
	}
	if v := got["diskio.nvme0n1.write_ops"]; v != 20 {
		t.Errorf("nvme write_ops = %v, want 20", v)
	}
	if v := got["diskio.sda.read_ops"]; v != 30 {
		t.Errorf("sda read_ops = %v, want 30", v)
	}
}

func TestConvertToSamples_Connections(t *testing.T) {
	data := PsutilOutput{
		Connections:    map[string]int{"ESTABLISHED": 5, "LISTEN": 2, "TIME_WAIT": 3, "total": 10},
		UDPConnections: map[string]int{"CONN_NONE": 7},
	}

	p := &PsutilCollector{}
	samples := p.convertToSamples(1, data)

	got := map[string]float64{}
	for _, s := range samples {
		got[s.Metric] = s.Value
	}

	if v := got["net.tcp.ESTABLISHED"]; v != 5 {
		t.Errorf("tcp established = %v, want 5", v)
	}
	if v := got["net.tcp.LISTEN"]; v != 2 {
		t.Errorf("tcp listen = %v, want 2", v)
	}
	if v := got["net.udp.CONN_NONE"]; v != 7 {
		t.Errorf("udp conns = %v, want 7", v)
	}
}
