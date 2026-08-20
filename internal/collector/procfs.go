package collector

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ZeroClue/uptime-monitor/internal/ssh"
)

type ProcfsCollector struct {
	logger    *slog.Logger
	sshClient ssh.SSHClient
}

type ProcfsOption func(*ProcfsCollector)

func WithProcfsSSHClient(client ssh.SSHClient) ProcfsOption {
	return func(p *ProcfsCollector) {
		p.sshClient = client
	}
}

func NewProcfsCollector(opts ...ProcfsOption) *ProcfsCollector {
	p := &ProcfsCollector{logger: slog.Default()}
	for _, opt := range opts {
		opt(p)
	}
	if p.sshClient == nil {
		p.sshClient = ssh.NewSSHClient(p.logger, nil)
	}
	return p
}

func (p *ProcfsCollector) Name() string {
	return "procfs"
}

type NetInfo struct {
	Interfaces map[string]NetInterface
}

type NetInterface struct {
	RxBytes   uint64
	TxBytes   uint64
	RxPackets uint64
	TxPackets uint64
	Errors    uint64
}

func (p *ProcfsCollector) Collect(ctx context.Context, host Host) ([]Sample, error) {
	loadAvg, err := p.getLoadAvg(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("failed to get loadavg: %w", err)
	}

	memInfo, err := p.getMemInfo(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("failed to get meminfo: %w", err)
	}

	cpuStat, err := p.getCPUStat(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("failed to get cpu stat: %w", err)
	}

	diskInfo, err := p.getDiskInfo(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("failed to get disk info: %w", err)
	}

	netInfo, err := p.getNetInfo(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("failed to get net info: %w", err)
	}

	uptime, err := p.getUptime(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("failed to get uptime: %w", err)
	}

	now := time.Now()
	swapUsed := memInfo.SwapTotal - memInfo.SwapFree
	samples := []Sample{
		{HostID: host.ID, Metric: "cpu.load_1m", Value: loadAvg.Load1, Timestamp: now, Collector: "procfs"},
		{HostID: host.ID, Metric: "cpu.load_5m", Value: loadAvg.Load5, Timestamp: now},
		{HostID: host.ID, Metric: "cpu.load_15m", Value: loadAvg.Load15, Timestamp: now},
		{HostID: host.ID, Metric: "mem.total_bytes", Value: float64(memInfo.Total), Timestamp: now},
		{HostID: host.ID, Metric: "mem.free_bytes", Value: float64(memInfo.Free), Timestamp: now},
		{HostID: host.ID, Metric: "mem.available_bytes", Value: float64(memInfo.Available), Timestamp: now},
		{HostID: host.ID, Metric: "mem.used_bytes", Value: float64(memInfo.Total - memInfo.Available), Timestamp: now},
		{HostID: host.ID, Metric: "mem.cached_bytes", Value: float64(memInfo.Cached), Timestamp: now},
		{HostID: host.ID, Metric: "mem.swap_total_bytes", Value: float64(memInfo.SwapTotal), Timestamp: now},
		{HostID: host.ID, Metric: "mem.swap_free_bytes", Value: float64(memInfo.SwapFree), Timestamp: now},
		{HostID: host.ID, Metric: "mem.swap_used_bytes", Value: float64(swapUsed), Timestamp: now},
		{HostID: host.ID, Metric: "disk.total_bytes", Value: float64(diskInfo.Total), Timestamp: now},
		{HostID: host.ID, Metric: "disk.used_bytes", Value: float64(diskInfo.Used), Timestamp: now},
		{HostID: host.ID, Metric: "disk.free_bytes", Value: float64(diskInfo.Free), Timestamp: now},
		{HostID: host.ID, Metric: "uptime.seconds", Value: uptime, Timestamp: now},
	}

	if pct, ok := swapUsedPct(memInfo.SwapTotal, memInfo.SwapFree); ok {
		samples = append(samples, Sample{
			HostID: host.ID, Metric: "mem.swap_used_pct",
			Value:     pct,
			Timestamp: now,
		})
	}

	procCount, err := p.getProcessCount(ctx, host)
	if err == nil {
		samples = append(samples, Sample{
			HostID: host.ID, Metric: "system.process_count",
			Value:     float64(procCount),
			Timestamp: now,
		})
	}

	if cpuStat != nil {
		total := cpuStat.User + cpuStat.System + cpuStat.Idle + cpuStat.Iowait + cpuStat.Nice + cpuStat.Softirq + cpuStat.Steal
		if total > 0 {
			samples = append(samples,
				Sample{HostID: host.ID, Metric: "cpu.user_pct", Value: float64(cpuStat.User) / float64(total) * 100, Timestamp: now},
				Sample{HostID: host.ID, Metric: "cpu.system_pct", Value: float64(cpuStat.System) / float64(total) * 100, Timestamp: now},
				Sample{HostID: host.ID, Metric: "cpu.idle_pct", Value: float64(cpuStat.Idle) / float64(total) * 100, Timestamp: now},
				Sample{HostID: host.ID, Metric: "cpu.iowait_pct", Value: float64(cpuStat.Iowait) / float64(total) * 100, Timestamp: now},
			)
		}
	}

	for iface, net := range netInfo.Interfaces {
		samples = append(samples,
			Sample{HostID: host.ID, Metric: "net." + iface + ".rx_bytes", Value: float64(net.RxBytes), Timestamp: now},
			Sample{HostID: host.ID, Metric: "net." + iface + ".tx_bytes", Value: float64(net.TxBytes), Timestamp: now},
			Sample{HostID: host.ID, Metric: "net." + iface + ".rx_packets", Value: float64(net.RxPackets), Timestamp: now},
			Sample{HostID: host.ID, Metric: "net." + iface + ".tx_packets", Value: float64(net.TxPackets), Timestamp: now},
			Sample{HostID: host.ID, Metric: "net." + iface + ".errors", Value: float64(net.Errors), Timestamp: now},
		)
	}

	return samples, nil
}

type LoadAvg struct {
	Load1, Load5, Load15 float64
}

func (p *ProcfsCollector) getLoadAvg(ctx context.Context, host Host) (LoadAvg, error) {
	output, err := p.execCommand(ctx, host, "cat /proc/loadavg")
	if err != nil {
		return LoadAvg{}, err
	}
	return parseLoadAvg(output)
}

func parseLoadAvg(output string) (LoadAvg, error) {
	fields := strings.Fields(output)
	if len(fields) < 3 {
		return LoadAvg{}, fmt.Errorf("unexpected loadavg output: %s", output)
	}
	nums := make([]float64, 0, 3)
	for _, f := range fields {
		v, err := strconv.ParseFloat(f, 64)
		if err != nil {
			continue
		}
		nums = append(nums, v)
		if len(nums) == 3 {
			break
		}
	}
	if len(nums) < 3 {
		return LoadAvg{}, fmt.Errorf("unexpected loadavg output: %s", output)
	}
	return LoadAvg{Load1: nums[0], Load5: nums[1], Load15: nums[2]}, nil
}

type MemInfo struct {
	Total     uint64
	Free      uint64
	Available uint64
	Cached    uint64
	SwapTotal uint64
	SwapFree  uint64
}

func (p *ProcfsCollector) getMemInfo(ctx context.Context, host Host) (MemInfo, error) {
	output, err := p.execCommand(ctx, host, "cat /proc/meminfo")
	if err != nil {
		return MemInfo{}, err
	}
	return parseMemInfo(output)
}

func parseMemInfo(output string) (MemInfo, error) {
	info := MemInfo{}
	re := regexp.MustCompile(`(\w+):\s+(\d+)\s+kB`)
	for _, match := range re.FindAllStringSubmatch(output, -1) {
		val, _ := strconv.ParseUint(match[2], 10, 64)
		switch match[1] {
		case "MemTotal":
			info.Total = val * 1024
		case "MemFree":
			info.Free = val * 1024
		case "MemAvailable":
			info.Available = val * 1024
		case "Cached":
			info.Cached = val * 1024
		case "SwapTotal":
			info.SwapTotal = val * 1024
		case "SwapFree":
			info.SwapFree = val * 1024
		}
	}
	return info, nil
}

func swapUsedPct(swapTotal, swapFree uint64) (float64, bool) {
	if swapTotal == 0 {
		return 0, false
	}
	return float64(swapTotal-swapFree) / float64(swapTotal) * 100, true
}

func (p *ProcfsCollector) getProcessCount(ctx context.Context, host Host) (int, error) {
	output, err := p.execCommand(ctx, host, "ls /proc")
	if err != nil {
		return 0, err
	}
	return parseProcessCount(output)
}

func parseProcessCount(output string) (int, error) {
	count := 0
	for _, line := range strings.Split(output, "\n") {
		if _, err := strconv.Atoi(strings.TrimSpace(line)); err == nil {
			count++
		}
	}
	if count == 0 {
		return 0, fmt.Errorf("no numeric entries found in /proc listing")
	}
	return count, nil
}

type CPUStat struct {
	User      uint64
	System    uint64
	Idle      uint64
	Iowait    uint64
	Nice      uint64
	Softirq   uint64
	Steal     uint64
	Guest     uint64
	GuestNice uint64
}

func (p *ProcfsCollector) getCPUStat(ctx context.Context, host Host) (*CPUStat, error) {
	output, err := p.execCommand(ctx, host, "cat /proc/stat")
	if err != nil {
		return nil, err
	}
	return parseCPUStat(output)
}

func parseCPUStat(output string) (*CPUStat, error) {
	lines := strings.Split(output, "\n")
	if len(lines) == 0 {
		return nil, fmt.Errorf("empty /proc/stat")
	}

	var cpuLine string
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == "cpu" {
			cpuLine = line
			break
		}
	}
	if cpuLine == "" {
		return nil, fmt.Errorf("no cpu line found in /proc/stat")
	}
	fields := strings.Fields(cpuLine)
	if len(fields) < 8 {
		return nil, fmt.Errorf("unexpected /proc/stat format: fields=%d", len(fields))
	}
	parse := func(i int) uint64 {
		if i >= len(fields) {
			return 0
		}
		v, _ := strconv.ParseUint(fields[i], 10, 64)
		return v
	}
	return &CPUStat{
		User:      parse(1),
		Nice:      parse(2),
		System:    parse(3),
		Idle:      parse(4),
		Iowait:    parse(5),
		Softirq:   parse(6),
		Steal:     parse(7),
		Guest:     parse(8),
		GuestNice: parse(9),
	}, nil
}

type DiskInfo struct {
	Total, Used, Free uint64
}

func (p *ProcfsCollector) getDiskInfo(ctx context.Context, host Host) (DiskInfo, error) {
	sudo := ""
	if host.Sudo {
		sudo = "sudo "
	}
	output, err := p.execCommand(ctx, host, sudo+"df -B1 /")
	if err != nil {
		return DiskInfo{}, err
	}
	lines := strings.Split(output, "\n")
	if len(lines) < 3 {
		return DiskInfo{}, fmt.Errorf("unexpected df output")
	}

	// Skip warning lines and header, find the data line
	var dataLine string
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) >= 4 {
			// Check if this looks like a data line (first field contains a path)
			if strings.HasPrefix(fields[0], "/") {
				dataLine = line
				break
			}
		}
	}
	if dataLine == "" {
		return DiskInfo{}, fmt.Errorf("no data line found in df output")
	}
	fields := strings.Fields(dataLine)
	if len(fields) < 4 {
		return DiskInfo{}, fmt.Errorf("unexpected df format")
	}
	total, _ := strconv.ParseUint(fields[1], 10, 64)
	used, _ := strconv.ParseUint(fields[2], 10, 64)
	free, _ := strconv.ParseUint(fields[3], 10, 64)
	return DiskInfo{Total: total, Used: used, Free: free}, nil
}

func (p *ProcfsCollector) getUptime(ctx context.Context, host Host) (float64, error) {
	output, err := p.execCommand(ctx, host, "cat /proc/uptime")
	if err != nil {
		return 0, err
	}
	return parseUptime(output)
}

func parseUptime(output string) (float64, error) {
	fields := strings.Fields(output)
	for _, f := range fields {
		if v, err := strconv.ParseFloat(f, 64); err == nil {
			return v, nil
		}
	}
	return 0, fmt.Errorf("no numeric uptime in output: %s", output)
}

func (p *ProcfsCollector) getNetInfo(ctx context.Context, host Host) (NetInfo, error) {
	output, err := p.execCommand(ctx, host, "cat /proc/net/dev")
	if err != nil {
		return NetInfo{}, err
	}
	lines := strings.Split(output, "\n")
	if len(lines) < 3 {
		return NetInfo{Interfaces: make(map[string]NetInterface)}, nil
	}
	interfaces := make(map[string]NetInterface)
	for _, line := range lines[2:] {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 17 {
			continue
		}
		name := strings.TrimSuffix(fields[0], ":")
		if name == "lo" {
			continue
		}
		rxBytes, _ := strconv.ParseUint(fields[1], 10, 64)
		rxPackets, _ := strconv.ParseUint(fields[2], 10, 64)
		rxErrors, _ := strconv.ParseUint(fields[3], 10, 64)
		txBytes, _ := strconv.ParseUint(fields[9], 10, 64)
		txPackets, _ := strconv.ParseUint(fields[10], 10, 64)
		txErrors, _ := strconv.ParseUint(fields[11], 10, 64)
		interfaces[name] = NetInterface{
			RxBytes:   rxBytes,
			TxBytes:   txBytes,
			RxPackets: rxPackets,
			TxPackets: txPackets,
			Errors:    rxErrors + txErrors,
		}
	}
	return NetInfo{Interfaces: interfaces}, nil
}

func (p *ProcfsCollector) execCommand(ctx context.Context, host Host, cmd string) (string, error) {
	target := SSHTargetFromHost(host)
	return p.sshClient.Exec(ctx, target, cmd)
}
