package dashboard

import "testing"

func TestToRateSeries(t *testing.T) {
	data := [][2]float64{
		{1000, 10},
		{1060, 20},
		{1120, 30},
	}
	got := toRateSeries(data)
	if len(got) != 2 {
		t.Fatalf("got %d points, want 2", len(got))
	}
	if got[0][0] != 1060 || got[0][1] != 10.0/60.0 {
		t.Errorf("got %v, want rate 10/60 at t=1060", got[0])
	}
	if got[1][1] != 10.0/60.0 {
		t.Errorf("got %v, want 10/60 at t=1120", got[1])
	}
}

func TestToRateSeries_Empty(t *testing.T) {
	if got := toRateSeries(nil); len(got) != 0 {
		t.Errorf("expected empty result, got %v", got)
	}
}