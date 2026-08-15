package collector

import (
	"context"

	"github.com/ZeroClue/uptime-monitor/internal/ssh"
)

type TailscaleCollector struct {
	procfs *ProcfsCollector
}

type TailscaleOption func(*TailscaleCollector)

func WithTailscaleProcfs(procfs *ProcfsCollector) TailscaleOption {
	return func(t *TailscaleCollector) {
		t.procfs = procfs
	}
}

func WithTailscaleSSHClient(client ssh.SSHClient) TailscaleOption {
	return func(t *TailscaleCollector) {
		if t.procfs == nil {
			t.procfs = NewProcfsCollector()
		}
		t.procfs.sshClient = client
	}
}

func NewTailscaleCollector(opts ...TailscaleOption) *TailscaleCollector {
	t := &TailscaleCollector{procfs: NewProcfsCollector()}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

func (t *TailscaleCollector) Name() string {
	return "tailscale"
}

func (t *TailscaleCollector) Collect(ctx context.Context, host Host) ([]Sample, error) {
	return t.procfs.Collect(ctx, host)
}
