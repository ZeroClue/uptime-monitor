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