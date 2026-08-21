package alerting

import (
	"testing"

	"github.com/ZeroClue/uptime-monitor/internal/storage"
)

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

func TestRuleMatchesProject(t *testing.T) {
	projA := int64(1)
	projB := int64(2)
	cases := []struct {
		name  string
		rule  storage.AlertRule
		hostP *int64
		want  bool
	}{
		{"global rule matches unassigned host", storage.AlertRule{}, nil, true},
		{"global rule matches host in project", storage.AlertRule{}, &projA, true},
		{"project rule misses unassigned host", storage.AlertRule{ProjectID: &projA}, nil, false},
		{"project rule hits same project", storage.AlertRule{ProjectID: &projA}, &projA, true},
		{"project rule misses other project", storage.AlertRule{ProjectID: &projA}, &projB, false},
	}
	for _, tc := range cases {
		if got := ruleMatchesProject(tc.rule, tc.hostP); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}
