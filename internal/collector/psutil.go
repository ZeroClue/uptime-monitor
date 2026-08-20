package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/ZeroClue/uptime-monitor/internal/ssh"
)

type PsutilCollector struct {
	logger    *slog.Logger
	sshClient ssh.SSHClient
}

type PsutilOption func(*PsutilCollector)

func WithPsutilSSHClient(client ssh.SSHClient) PsutilOption {
	return func(p *PsutilCollector) {
		p.sshClient = client
	}
}

func NewPsutilCollector(opts ...PsutilOption) *PsutilCollector {
	p := &PsutilCollector{logger: slog.Default()}
	for _, opt := range opts {
		opt(p)
	}
	if p.sshClient == nil {
		p.sshClient = ssh.NewSSHClient(p.logger, nil)
	}
	return p
}

func (p *PsutilCollector) Name() string {
	return "psutil"
}

func (p *PsutilCollector) Collect(ctx context.Context, host Host) ([]Sample, error) {
	cmd := buildPsutilCommand(host)
	target := SSHTargetFromHost(host)
	output, err := p.sshClient.Exec(ctx, target, cmd)
	if err != nil {
		return nil, fmt.Errorf("ssh exec failed: %w", err)
	}

	var data PsutilOutput
	if err := json.Unmarshal([]byte(output), &data); err != nil {
		return nil, fmt.Errorf("failed to parse psutil JSON: %w", err)
	}

	return p.convertToSamples(host.ID, data), nil
}

func buildPsutilCommand(host Host) string {
	sudo := ""
	if host.Sudo {
		sudo = "sudo "
	}
	return fmt.Sprintf("%spython3 -c \"import psutil, json, time; cpu = psutil.cpu_times_percent(interval=0.1); mem = psutil.virtual_memory(); swap = psutil.swap_memory(); disk = psutil.disk_usage('/'); net = psutil.net_io_counters(pernic=True); load = psutil.getloadavg(); procs = len(psutil.pids()); uptime = psutil.boot_time(); print(json.dumps({'cpu': {'user': cpu.user, 'system': cpu.system, 'idle': cpu.idle, 'iowait': cpu.iowait, 'load1': load[0], 'load5': load[1], 'load15': load[2]}, 'mem': {'total': mem.total, 'used': mem.used, 'free': mem.free, 'available': mem.available, 'cached': mem.cached, 'swap_total': swap.total, 'swap_free': swap.free, 'swap_used': swap.used}, 'disk': {'total': disk.total, 'used': disk.used, 'free': disk.free}, 'net': {k: {'rx_bytes': v.bytes_recv, 'tx_bytes': v.bytes_sent, 'rx_packets': v.packets_recv, 'tx_packets': v.packets_sent, 'errors': v.errin + v.errout} for k, v in net.items()}, 'uptime': int(time.time() - uptime), 'process_count': procs}))\"", sudo)
}

type PsutilOutput struct {
	CPU          PsutilCPU            `json:"cpu"`
	Mem          PsutilMem            `json:"mem"`
	Disk         PsutilDisk           `json:"disk"`
	Net          map[string]PsutilNet `json:"net"`
	Uptime       int                  `json:"uptime"`
	ProcessCount int                  `json:"process_count"`
}

type PsutilCPU struct {
	User   float64 `json:"user"`
	System float64 `json:"system"`
	Idle   float64 `json:"idle"`
	Iowait float64 `json:"iowait"`
	Load1  float64 `json:"load1"`
	Load5  float64 `json:"load5"`
	Load15 float64 `json:"load15"`
}

type PsutilMem struct {
	Total     uint64 `json:"total"`
	Used      uint64 `json:"used"`
	Free      uint64 `json:"free"`
	Available uint64 `json:"available"`
	Cached    uint64 `json:"cached"`
	SwapTotal uint64 `json:"swap_total"`
	SwapFree  uint64 `json:"swap_free"`
	SwapUsed  uint64 `json:"swap_used"`
}

type PsutilDisk struct {
	Total uint64 `json:"total"`
	Used  uint64 `json:"used"`
	Free  uint64 `json:"free"`
}

type PsutilNet struct {
	RxBytes   uint64 `json:"rx_bytes"`
	TxBytes   uint64 `json:"tx_bytes"`
	RxPackets uint64 `json:"rx_packets"`
	TxPackets uint64 `json:"tx_packets"`
	Errors    uint64 `json:"errors"`
}

func (p *PsutilCollector) convertToSamples(hostID int64, data PsutilOutput) []Sample {
	now := time.Now()
	samples := []Sample{
		{HostID: hostID, Metric: "cpu.user_pct", Value: data.CPU.User, Timestamp: now, Collector: "psutil"},
		{HostID: hostID, Metric: "cpu.system_pct", Value: data.CPU.System, Timestamp: now},
		{HostID: hostID, Metric: "cpu.idle_pct", Value: data.CPU.Idle, Timestamp: now},
		{HostID: hostID, Metric: "cpu.iowait_pct", Value: data.CPU.Iowait, Timestamp: now},
		{HostID: hostID, Metric: "cpu.load_1m", Value: data.CPU.Load1, Timestamp: now},
		{HostID: hostID, Metric: "cpu.load_5m", Value: data.CPU.Load5, Timestamp: now},
		{HostID: hostID, Metric: "cpu.load_15m", Value: data.CPU.Load15, Timestamp: now},
		{HostID: hostID, Metric: "mem.total_bytes", Value: float64(data.Mem.Total), Timestamp: now},
		{HostID: hostID, Metric: "mem.used_bytes", Value: float64(data.Mem.Used), Timestamp: now},
		{HostID: hostID, Metric: "mem.free_bytes", Value: float64(data.Mem.Free), Timestamp: now},
		{HostID: hostID, Metric: "mem.available_bytes", Value: float64(data.Mem.Available), Timestamp: now},
		{HostID: hostID, Metric: "mem.cached_bytes", Value: float64(data.Mem.Cached), Timestamp: now},
		{HostID: hostID, Metric: "mem.swap_total_bytes", Value: float64(data.Mem.SwapTotal), Timestamp: now},
		{HostID: hostID, Metric: "mem.swap_free_bytes", Value: float64(data.Mem.SwapFree), Timestamp: now},
		{HostID: hostID, Metric: "mem.swap_used_bytes", Value: float64(data.Mem.SwapUsed), Timestamp: now},
		{HostID: hostID, Metric: "disk.total_bytes", Value: float64(data.Disk.Total), Timestamp: now},
		{HostID: hostID, Metric: "disk.used_bytes", Value: float64(data.Disk.Used), Timestamp: now},
		{HostID: hostID, Metric: "disk.free_bytes", Value: float64(data.Disk.Free), Timestamp: now},
		{HostID: hostID, Metric: "uptime.seconds", Value: float64(data.Uptime), Timestamp: now},
	}

	if pct, ok := swapUsedPct(data.Mem.SwapTotal, data.Mem.SwapFree); ok {
		samples = append(samples, Sample{
			HostID: hostID, Metric: "mem.swap_used_pct",
			Value:     pct,
			Timestamp: now,
		})
	}

	if data.ProcessCount > 0 {
		samples = append(samples, Sample{
			HostID: hostID, Metric: "system.process_count",
			Value:     float64(data.ProcessCount),
			Timestamp: now,
		})
	}

	for iface, net := range data.Net {
		samples = append(samples,
			Sample{HostID: hostID, Metric: "net." + iface + ".rx_bytes", Value: float64(net.RxBytes), Timestamp: now},
			Sample{HostID: hostID, Metric: "net." + iface + ".tx_bytes", Value: float64(net.TxBytes), Timestamp: now},
			Sample{HostID: hostID, Metric: "net." + iface + ".rx_packets", Value: float64(net.RxPackets), Timestamp: now},
			Sample{HostID: hostID, Metric: "net." + iface + ".tx_packets", Value: float64(net.TxPackets), Timestamp: now},
			Sample{HostID: hostID, Metric: "net." + iface + ".errors", Value: float64(net.Errors), Timestamp: now},
		)
	}

	return samples
}
