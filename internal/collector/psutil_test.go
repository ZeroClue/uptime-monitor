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
