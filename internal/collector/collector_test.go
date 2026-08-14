package collector

import (
	"context"
	"testing"
	"time"
)

func TestChain_Fallback(t *testing.T) {
	chain := NewChain(
		&failingCollector{name: "first"},
		&mockCollector{name: "second", samples: []Sample{{HostID: 1, Metric: "test", Value: 42, Timestamp: time.Now()}}},
	)

	samples, err := chain.Collect(context.Background(), Host{ID: 1})
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if len(samples) != 1 || samples[0].Value != 42 {
		t.Fatalf("unexpected samples: %+v", samples)
	}
}

func TestChain_CollectorPreference(t *testing.T) {
	chain := NewChain(
		&mockCollector{name: "preferred", samples: []Sample{{HostID: 1, Metric: "pref", Value: 1, Timestamp: time.Now()}}},
		&mockCollector{name: "other", samples: []Sample{{HostID: 1, Metric: "other", Value: 2, Timestamp: time.Now()}}},
	)

	host := Host{ID: 1, CollectorPreference: "preferred"}
	samples, err := chain.Collect(context.Background(), host)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if len(samples) != 1 || samples[0].Metric != "pref" {
		t.Fatalf("expected preferred collector, got: %+v", samples)
	}
}

type failingCollector struct {
	name string
}

func (f *failingCollector) Name() string { return f.name }
func (f *failingCollector) Collect(ctx context.Context, host Host) ([]Sample, error) {
	return nil, ErrCollectorFailed
}

type mockCollector struct {
	name    string
	samples []Sample
}

func (m *mockCollector) Name() string { return m.name }
func (m *mockCollector) Collect(ctx context.Context, host Host) ([]Sample, error) {
	return m.samples, nil
}
