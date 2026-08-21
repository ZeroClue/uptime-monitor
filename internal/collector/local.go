package collector

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// LocalProcfsCollector reads metrics directly from the local machine's
// procfs (no SSH). Set HOST_PROC to read a mounted host /proc instead
// (useful when running inside a container with pid namespace access).
type LocalProcfsCollector struct {
	logger   *slog.Logger
	procRoot string
}

type LocalProcfsOption func(*LocalProcfsCollector)

func WithLocalProcRoot(root string) LocalProcfsOption {
	return func(p *LocalProcfsCollector) {
		p.procRoot = root
	}
}

func WithLocalLogger(logger *slog.Logger) LocalProcfsOption {
	return func(p *LocalProcfsCollector) {
		p.logger = logger
	}
}

func NewLocalProcfsCollector(opts ...LocalProcfsOption) *LocalProcfsCollector {
	p := &LocalProcfsCollector{
		logger:   slog.Default(),
		procRoot: "/proc",
	}
	if env := os.Getenv("HOST_PROC"); env != "" {
		p.procRoot = env
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

func (p *LocalProcfsCollector) Name() string {
	return "local"
}

func (p *LocalProcfsCollector) Collect(ctx context.Context, host Host) ([]Sample, error) {
	if host.Connection != "local" {
		return nil, fmt.Errorf("collector %q requires connection=local, host %q has %q", p.Name(), host.Name, host.Connection)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	loadAvg, err := parseLoadAvgFile(p.procPath("loadavg"))
	if err != nil {
		return nil, fmt.Errorf("loadavg: %w", err)
	}
	memInfo, err := parseMemInfoFile(p.procPath("meminfo"))
	if err != nil {
		return nil, fmt.Errorf("meminfo: %w", err)
	}
	cpuStatOut, err := os.ReadFile(p.procPath("stat"))
	if err != nil {
		return nil, fmt.Errorf("stat: %w", err)
	}
	cpuStat, err := parseCPUStat(string(cpuStatOut))
	if err != nil {
		return nil, fmt.Errorf("cpu stat: %w", err)
	}
	diskInfo, err := p.getDiskInfo()
	if err != nil {
		return nil, fmt.Errorf("disk info: %w", err)
	}
	netDevOut, err := os.ReadFile(p.procPath("net", "dev"))
	if err != nil {
		return nil, fmt.Errorf("net dev: %w", err)
	}
	netInfo := parseNetDev(string(netDevOut))
	uptime, err := parseUptimeFile(p.procPath("uptime"))
	if err != nil {
		return nil, fmt.Errorf("uptime: %w", err)
	}

	now := time.Now()
	swapUsed := memInfo.SwapTotal - memInfo.SwapFree
	samples := []Sample{
		{HostID: host.ID, Metric: "cpu.load_1m", Value: loadAvg.Load1, Timestamp: now, Collector: "local"},
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
	}

	for _, mount := range sortedDiskMounts(diskInfo) {
		d := diskInfo[mount]
		samples = append(samples,
			Sample{HostID: host.ID, Metric: "disk." + sanitizeMount(mount) + ".total_bytes", Value: float64(d.Total), Timestamp: now},
			Sample{HostID: host.ID, Metric: "disk." + sanitizeMount(mount) + ".used_bytes", Value: float64(d.Used), Timestamp: now},
			Sample{HostID: host.ID, Metric: "disk." + sanitizeMount(mount) + ".free_bytes", Value: float64(d.Free), Timestamp: now},
		)
		if d.InodeTotal > 0 {
			samples = append(samples, Sample{
				HostID: host.ID, Metric: "disk." + sanitizeMount(mount) + ".inodes_used_pct",
				Value:     float64(d.InodeUsed) / float64(d.InodeTotal) * 100,
				Timestamp: now,
			})
		}
	}

	samples = append(samples, Sample{HostID: host.ID, Metric: "uptime.seconds", Value: uptime, Timestamp: now})

	if pct, ok := swapUsedPct(memInfo.SwapTotal, memInfo.SwapFree); ok {
		samples = append(samples, Sample{HostID: host.ID, Metric: "mem.swap_used_pct", Value: pct, Timestamp: now})
	}

	if procCount, err := processCount(p.procRoot); err == nil {
		samples = append(samples, Sample{HostID: host.ID, Metric: "system.process_count", Value: float64(procCount), Timestamp: now})
	} else {
		p.logger.Debug("no process count", "error", err)
	}

	total := cpuStat.User + cpuStat.System + cpuStat.Idle + cpuStat.Iowait + cpuStat.Nice + cpuStat.Softirq + cpuStat.Steal
	if total > 0 {
		samples = appendCPUSampleGroup(samples, host.ID, "cpu",
			float64(cpuStat.User)/float64(total)*100,
			float64(cpuStat.System)/float64(total)*100,
			float64(cpuStat.Idle)/float64(total)*100,
			float64(cpuStat.Iowait)/float64(total)*100, now)
	}

	perCore, perCoreErr := parsePerCoreCPU(string(cpuStatOut))
	if perCoreErr != nil {
		p.logger.Debug("no per-core cpu data", "error", perCoreErr)
	} else {
		for _, core := range sortedCoreIDs(perCore) {
			stat := perCore[core]
			coreTotal := stat.User + stat.System + stat.Idle + stat.Iowait + stat.Nice + stat.Softirq + stat.Steal
			if coreTotal == 0 {
				continue
			}
			samples = appendCPUSampleGroup(samples, host.ID, fmt.Sprintf("cpu.core.%d", core),
				float64(stat.User)/float64(coreTotal)*100,
				float64(stat.System)/float64(coreTotal)*100,
				float64(stat.Idle)/float64(coreTotal)*100,
				float64(stat.Iowait)/float64(coreTotal)*100, now)
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

	diskIO, err := parseDiskStatsFile(p.procPath("diskstats"))
	if err != nil {
		p.logger.Debug("no disk i/o data", "error", err)
	} else {
		for _, dev := range sortedDiskDevices(diskIO) {
			d := diskIO[dev]
			samples = append(samples,
				Sample{HostID: host.ID, Metric: "diskio." + dev + ".read_bytes", Value: float64(d.ReadBytes), Timestamp: now},
				Sample{HostID: host.ID, Metric: "diskio." + dev + ".write_bytes", Value: float64(d.WriteBytes), Timestamp: now},
				Sample{HostID: host.ID, Metric: "diskio." + dev + ".read_ops", Value: float64(d.ReadOps), Timestamp: now},
				Sample{HostID: host.ID, Metric: "diskio." + dev + ".write_ops", Value: float64(d.WriteOps), Timestamp: now},
			)
		}
	}

	connStates, err := p.getConnectionStates()
	if err != nil {
		p.logger.Debug("no connection states data", "error", err)
	} else {
		for proto, states := range connStates {
			for state, count := range states {
				samples = append(samples,
					Sample{HostID: host.ID, Metric: fmt.Sprintf("net.%s.%s", proto, state), Value: float64(count), Timestamp: now},
				)
			}
		}
	}

	return samples, nil
}

func (p *LocalProcfsCollector) procPath(elem ...string) string {
	return p.procRoot + "/" + strings.Join(elem, "/")
}

// getDiskInfo shells out to df (real filesystem view); /proc has no mount sizes.
func (p *LocalProcfsCollector) getDiskInfo() (map[string]DiskMountInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dfOut, err := exec.CommandContext(ctx, "df", "-B1").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("df -B1: %w: %s", err, dfOut)
	}
	mounts, err := parseDiskStatsMultiMount(string(dfOut))
	if err != nil {
		return nil, err
	}
	dfiOut, _ := exec.CommandContext(ctx, "df", "-i").CombinedOutput()
	if len(dfiOut) > 0 {
		if inodes, err := parseDiskInodes(string(dfiOut)); err == nil {
			for mount, info := range inodes {
				if m, ok := mounts[mount]; ok {
					m.InodeTotal = info.Total
					m.InodeUsed = info.Used
					m.InodeFree = info.Free
					mounts[mount] = m
				}
			}
		}
	}
	return mounts, nil
}

func (p *LocalProcfsCollector) getConnectionStates() (map[string]map[string]int, error) {
	read := func(name string) string {
		data, _ := os.ReadFile(p.procPath("net", name))
		return string(data)
	}
	tcp := read("tcp") + "\n" + read("tcp6")
	udp := read("udp") + "\n" + read("udp6")
	if strings.TrimSpace(tcp) == "" && strings.TrimSpace(udp) == "" {
		return nil, fmt.Errorf("no /proc/net socket tables readable")
	}
	result := make(map[string]map[string]int)
	result["tcp"] = parseConnectionStates(tcp, "tcp")
	result["udp"] = parseConnectionStates(udp, "udp")
	return result, nil
}

func parseLoadAvgFile(path string) (LoadAvg, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return LoadAvg{}, err
	}
	return parseLoadAvg(string(data))
}

func parseMemInfoFile(path string) (MemInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return MemInfo{}, err
	}
	return parseMemInfo(string(data))
}

func parseUptimeFile(path string) (float64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return parseUptime(string(data))
}

func parseDiskStatsFile(path string) (map[string]DiskIOStats, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseDiskStats(string(data))
}

// processCount counts numeric entries (PID directories) under root.
func processCount(root string) (int, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err == nil {
			count++
		}
	}
	if count == 0 {
		return 0, fmt.Errorf("no PID entries under %s", root)
	}
	return count, nil
}
