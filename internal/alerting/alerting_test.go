package alerting

import "testing"

func TestExceedsThreshold(t *testing.T) {
	tests := []struct {
		name      string
		value     float64
		threshold float64
		below     bool
		want      bool
	}{
		{"above-over", 95, 90, false, true},
		{"above-exact", 90, 90, false, true},
		{"above-under", 85, 90, false, false},
		{"below-under", 30, 60, true, true},
		{"below-exact", 60, 60, true, true},
		{"below-over", 300, 60, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := exceedsThreshold(tt.value, tt.threshold, tt.below); got != tt.want {
				t.Errorf("exceedsThreshold(%v, %v, %v) = %v, want %v", tt.value, tt.threshold, tt.below, got, tt.want)
			}
		})
	}
}
