package collector

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type ProcfsCollector struct {
	logger *slog.Logger
}

func NewProcfsCollector() *ProcfsCollector {
	return &ProcfsCollector{logger: slog.Default()}
}

func (p *ProcfsCollector) Name() string {
	return "procfs"
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

	uptime, err := p.getUptime(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("failed to get uptime: %w", err)
	}

	now := time.Now()
	samples := []Sample{
		{HostID: host.ID, Metric: "cpu.load_1m", Value: loadAvg.Load1, Timestamp: now},
		{HostID: host.ID, Metric: "cpu.load_5m", Value: loadAvg.Load5, Timestamp: now},
		{HostID: host.ID, Metric: "cpu.load_15m", Value: loadAvg.Load15, Timestamp: now},
		{HostID: host.ID, Metric: "mem.total_bytes", Value: float64(memInfo.Total), Timestamp: now},
		{HostID: host.ID, Metric: "mem.free_bytes", Value: float64(memInfo.Free), Timestamp: now},
		{HostID: host.ID, Metric: "mem.available_bytes", Value: float64(memInfo.Available), Timestamp: now},
		{HostID: host.ID, Metric: "mem.cached_bytes", Value: float64(memInfo.Cached), Timestamp: now},
		{HostID: host.ID, Metric: "disk.total_bytes", Value: float64(diskInfo.Total), Timestamp: now},
		{HostID: host.ID, Metric: "disk.used_bytes", Value: float64(diskInfo.Used), Timestamp: now},
		{HostID: host.ID, Metric: "disk.free_bytes", Value: float64(diskInfo.Free), Timestamp: now},
		{HostID: host.ID, Metric: "uptime.seconds", Value: uptime, Timestamp: now},
	}

	if cpuStat != nil {
		total := cpuStat.User + cpuStat.System + cpuStat.Idle + cpuStat.Iowait + cpuStat.Nice + cpuStat.Softirq + cpuStat.Steal + cpuStat.Guest + cpuStat.GuestNice
		if total > 0 {
			samples = append(samples,
				Sample{HostID: host.ID, Metric: "cpu.user_pct", Value: float64(cpuStat.User) / float64(total) * 100, Timestamp: now},
				Sample{HostID: host.ID, Metric: "cpu.system_pct", Value: float64(cpuStat.System) / float64(total) * 100, Timestamp: now},
				Sample{HostID: host.ID, Metric: "cpu.idle_pct", Value: float64(cpuStat.Idle) / float64(total) * 100, Timestamp: now},
				Sample{HostID: host.ID, Metric: "cpu.iowait_pct", Value: float64(cpuStat.Iowait) / float64(total) * 100, Timestamp: now},
			)
		}
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
	fields := strings.Fields(output)
	if len(fields) < 3 {
		return LoadAvg{}, fmt.Errorf("unexpected loadavg output: %s", output)
	}
	load1, _ := strconv.ParseFloat(fields[0], 64)
	load5, _ := strconv.ParseFloat(fields[1], 64)
	load15, _ := strconv.ParseFloat(fields[2], 64)
	return LoadAvg{Load1: load1, Load5: load5, Load15: load15}, nil
}

type MemInfo struct {
	Total     uint64
	Free      uint64
	Available uint64
	Cached    uint64
}

func (p *ProcfsCollector) getMemInfo(ctx context.Context, host Host) (MemInfo, error) {
	output, err := p.execCommand(ctx, host, "cat /proc/meminfo")
	if err != nil {
		return MemInfo{}, err
	}
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
		}
	}
	return info, nil
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
	lines := strings.Split(output, "\n")
	if len(lines) == 0 {
		return nil, fmt.Errorf("empty /proc/stat")
	}
	fields := strings.Fields(lines[0])
	if len(fields) < 8 || fields[0] != "cpu" {
		return nil, fmt.Errorf("unexpected /proc/stat format")
	}
	parse := func(i int) uint64 {
		v, _ := strconv.ParseUint(fields[i], 10, 64)
		return v
	}
	return &CPUStat{
		User: parse(1), Nice: parse(2), System: parse(3), Idle: parse(4),
		Iowait: parse(5), Softirq: parse(6), Steal: parse(7),
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
	if len(lines) < 2 {
		return DiskInfo{}, fmt.Errorf("unexpected df output")
	}
	fields := strings.Fields(lines[1])
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
	fields := strings.Fields(output)
	if len(fields) == 0 {
		return 0, fmt.Errorf("empty uptime")
	}
	uptime, _ := strconv.ParseFloat(fields[0], 64)
	return uptime, nil
}

func (p *ProcfsCollector) execCommand(ctx context.Context, host Host, cmd string) (string, error) {
	return execSSH(ctx, p.logger, host, cmd, host.Timeout)
}