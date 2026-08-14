package collector

import (
	"context"
)

type TailscaleCollector struct {
	procfs *ProcfsCollector
}

func NewTailscaleCollector() *TailscaleCollector {
	return &TailscaleCollector{procfs: NewProcfsCollector()}
}

func (t *TailscaleCollector) Name() string {
	return "tailscale"
}

func (t *TailscaleCollector) Collect(ctx context.Context, host Host) ([]Sample, error) {
	return t.procfs.Collect(ctx, host)
}