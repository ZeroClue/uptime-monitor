package collector

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gosnmp/gosnmp"
)

// Standard-MIB OIDs polled by the SNMP collector.
const (
	oidIfDescr      = "1.3.6.1.2.1.2.2.1.2"    // IF-MIB ifDescr
	oidIfName       = "1.3.6.1.2.1.31.1.1.1.1" // IF-MIB ifName (unique per port)
	oidIfOperStatus = "1.3.6.1.2.1.2.2.1.8"    // ifOperStatus (1 = up)
	oidIfInOctets   = "1.3.6.1.2.1.2.2.1.10"   // ifInOctets
	oidIfOutOctets  = "1.3.6.1.2.1.2.2.1.16"   // ifOutOctets
	oidIfInErrors   = "1.3.6.1.2.1.2.2.1.14"   // ifInErrors
	oidIfOutErrors  = "1.3.6.1.2.1.2.2.1.20"   // ifOutErrors

	oidHrStorageEntry        = "1.3.6.1.2.1.25.2.3.1" // HOST-RESOURCES-MIB hrStorageEntry
	oidHrStorageType         = oidHrStorageEntry + ".2"
	oidHrStorageDescr        = oidHrStorageEntry + ".3"
	oidHrStorageUnit         = oidHrStorageEntry + ".4"
	oidHrStorageSize         = oidHrStorageEntry + ".5"
	oidHrStorageUsed         = oidHrStorageEntry + ".6"
	oidHrProcessorLoadPrefix = "1.3.6.1.2.1.25.3.3.1.2" // hrProcessorLoad.<idx>
	oidHrStorageRam          = "1.3.6.1.2.1.25.2.1.2"
	oidHrStorageDisk         = "1.3.6.1.2.1.25.2.1.4"

	oidUcdLoad         = "1.3.6.1.4.1.2021.10.1.3" // UCD-SNMP-MIB laLoad
	oidUcdLoad1        = oidUcdLoad + ".1"
	oidUcdLoad5        = oidUcdLoad + ".2"
	oidUcdLoad15       = oidUcdLoad + ".3"
	oidUcdMemAvailReal = "1.3.6.1.4.1.2021.4.6.0"
	oidUcdMemTotalReal = "1.3.6.1.4.1.2021.4.5.0"
)

// SnmpSession is the slice of gosnmp the collector needs; fakes implement it
// in tests.
type SnmpSession interface {
	Get(oids []string) ([]gosnmp.SnmpPDU, error)
	BulkWalkAll(root string) ([]gosnmp.SnmpPDU, error)
	Close() error
}

// SNMPCollector polls network devices over SNMP v2c/v3 and maps standard
// MIBs onto snmp.* metrics.
type SNMPCollector struct {
	logger *slog.Logger
	dialer func(host Host) (SnmpSession, error)
}

type SNMPOption func(*SNMPCollector)

func WithSNMPDialer(d func(host Host) (SnmpSession, error)) SNMPOption {
	return func(c *SNMPCollector) { c.dialer = d }
}

func NewSNMPCollector(opts ...SNMPOption) *SNMPCollector {
	c := &SNMPCollector{logger: slog.Default(), dialer: dialSNMP}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *SNMPCollector) Name() string { return "snmp" }

func (c *SNMPCollector) Collect(ctx context.Context, host Host) ([]Sample, error) {
	switch {
	case host.Connection != "snmp":
		return nil, fmt.Errorf("collector %q requires connection=snmp, host %q has %q", c.Name(), host.Name, host.Connection)
	case host.SNMPVersion != "2c" && host.SNMPVersion != "3":
		return nil, fmt.Errorf("collector %q requires snmp_version 2c or 3, host %q has %q", c.Name(), host.Name, host.SNMPVersion)
	}

	session, err := c.dialer(host)
	if err != nil {
		return nil, fmt.Errorf("snmp connect failed: %w", err)
	}
	defer session.Close()

	now := time.Now()
	var samples []Sample
	emit := func(metric string, value float64) {
		samples = append(samples, Sample{
			HostID: host.ID, Metric: metric, Value: value, Timestamp: now, Collector: c.Name(),
		})
	}

	c.collectInterfaces(session, emit)
	c.collectHostResources(session, emit)
	c.collectUCD(session, emit)
	c.collectExtraOIDs(session, host.SNMPExtraOIDs, emit)

	if len(samples) == 0 {
		return nil, fmt.Errorf("snmp poll of %q returned no usable values", host.Name)
	}
	c.logger.Debug("snmp collect detail",
		"host", host.Name, "endpoint", host.Endpoint,
		"samples_by_prefix", countPrefixes(samples))
	return samples, nil
}

func countPrefixes(samples []Sample) map[string]int {
	out := map[string]int{}
	for _, s := range samples {
		parts := strings.SplitN(s.Metric, ".", 3)
		key := strings.Join(parts[:min(2, len(parts))], ".")
		out[key]++
	}
	return out
}

func (c *SNMPCollector) collectInterfaces(session SnmpSession, emit func(string, float64)) {
	// ifName is unique per port; fall back to ifDescr text when absent.
	names := walkIndexed(c, session, oidIfName, pduString)
	if len(names) == 0 {
		names = walkIndexed(c, session, oidIfDescr, pduString)
	}
	if len(names) == 0 {
		return
	}

	// Deterministic display names: sanitize, then disambiguate duplicates
	// (two ports sharing a description) with the ifIndex suffix.
	idxs := make([]string, 0, len(names))
	for idx := range names {
		idxs = append(idxs, idx)
	}
	sort.Slice(idxs, func(i, j int) bool {
		a, ea := strconv.Atoi(idxs[i])
		b, eb := strconv.Atoi(idxs[j])
		if ea != nil || eb != nil {
			return idxs[i] < idxs[j]
		}
		return a < b
	})
	display := make(map[string]string, len(idxs))
	used := map[string]bool{}
	for _, idx := range idxs {
		name := sanitizeMetricPart(names[idx])
		if used[name] {
			name = name + "_" + idx
		}
		used[name] = true
		display[idx] = name
	}

	counters := map[string]struct{ metric string }{
		oidIfInOctets:  {"in_octets"},
		oidIfOutOctets: {"out_octets"},
		oidIfInErrors:  {"in_errors"},
		oidIfOutErrors: {"out_errors"},
	}
	for base, col := range counters {
		for idx, value := range walkIndexed(c, session, base, pduToFloat) {
			if name, ok := display[idx]; ok {
				emit("snmp.iface."+name+"."+col.metric, value)
			}
		}
	}
	for idx, raw := range walkIndexed(c, session, oidIfOperStatus, pduToFloat) {
		if name, ok := display[idx]; ok {
			up := 0.0
			if int(raw) == 1 {
				up = 1
			}
			emit("snmp.iface."+name+".up", up)
		}
	}
}

func (c *SNMPCollector) collectHostResources(session SnmpSession, emit func(string, float64)) {
	// hrProcessorLoad is per-CPU percentage; average it into one gauge.
	loads := walkIndexed(c, session, oidHrProcessorLoadPrefix, pduToFloat)
	if len(loads) > 0 {
		total := 0.0
		for _, v := range loads {
			total += v
		}
		emit("snmp.cpu.user_pct", total/float64(len(loads)))
	}
	c.collectStorageTable(session, emit)
}

func (c *SNMPCollector) collectUCD(session SnmpSession, emit func(string, float64)) {
	gets := getValues(session, oidUcdLoad1, oidUcdLoad5, oidUcdLoad15, oidUcdMemAvailReal, oidUcdMemTotalReal)

	loadNames := map[string]string{oidUcdLoad1: "snmp.load.1m", oidUcdLoad5: "snmp.load.5m", oidUcdLoad15: "snmp.load.15m"}
	for oid, metric := range loadNames {
		if v, ok := gets[oid]; ok {
			emit(metric, v)
		}
	}
	total, hasTotal := gets[oidUcdMemTotalReal]
	avail, hasAvail := gets[oidUcdMemAvailReal]
	if hasTotal && hasAvail && total > 0 {
		emit("snmp.mem.total_bytes", total*1024)
		emit("snmp.mem.used_bytes", (total-avail)*1024)
	}
}

func (c *SNMPCollector) collectStorageTable(session SnmpSession, emit func(string, float64)) {
	types := walkIndexed(c, session, oidHrStorageType, rawOID)
	descrs := walkIndexed(c, session, oidHrStorageDescr, pduString)
	units := walkIndexed(c, session, oidHrStorageUnit, pduToFloat)
	sizes := walkIndexed(c, session, oidHrStorageSize, pduToFloat)
	useds := walkIndexed(c, session, oidHrStorageUsed, pduToFloat)

	for idx, typeOID := range types {
		descr, ok := descrs[idx]
		if !ok || descr == "" {
			continue
		}
		unit, size, used := units[idx], sizes[idx], useds[idx]
		switch typeOID {
		case oidHrStorageRam:
			emit("snmp.mem.total_bytes", size*unit)
			emit("snmp.mem.used_bytes", used*unit)
		case oidHrStorageDisk:
			emit("snmp.disk."+sanitizeMetricPart(descr)+".total_bytes", size*unit)
			emit("snmp.disk."+sanitizeMetricPart(descr)+".used_bytes", used*unit)
		}
	}
}

func (c *SNMPCollector) collectExtraOIDs(session SnmpSession, spec string, emit func(string, float64)) {
	extra := parseExtraOIDs(spec)
	if len(extra) == 0 {
		return
	}
	oids := make([]string, 0, len(extra))
	for oid := range extra {
		oids = append(oids, oid)
	}
	values := getValues(session, oids...)
	for oid, v := range values {
		emit("snmp.custom."+sanitizeMetricPart(extra[oid]), v)
	}
}

// parseExtraOIDs reads "<oid> <metric_name>" lines; blank lines and #-comments
// are ignored, malformed lines skipped.
func parseExtraOIDs(spec string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(spec, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 || !strings.Contains(fields[0], ".") {
			continue
		}
		out[fields[0]] = fields[1]
	}
	return out
}

func pduToFloat(pdu gosnmp.SnmpPDU) (float64, bool) {
	switch v := pdu.Value.(type) {
	case int:
		return float64(v), true
	case uint:
		return float64(v), true
	case uint32:
		return float64(v), true
	case uint64:
		return float64(v), true
	case float64:
		return v, true
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return f, true
		}
	case []byte:
		if f, err := strconv.ParseFloat(strings.TrimSpace(string(v)), 64); err == nil {
			return f, true
		}
	}
	return 0, false
}

// indexSuffix extracts the row index portion of a walked OID. gosnmp may
// render PDU names with or without a leading dot depending on version;
// normalize both sides before matching.
func indexSuffix(oid, base string) string {
	oid = strings.TrimPrefix(oid, ".")
	base = strings.TrimPrefix(base, ".")
	return strings.TrimPrefix(oid, base+".")
}

// pduString extracts interface descriptions/names.
func pduString(p gosnmp.SnmpPDU) (string, bool) {
	switch v := p.Value.(type) {
	case []byte:
		return string(v), true
	case string:
		return v, true
	default:
		return "", false
	}
}

// walkIndexed BulkWalks base and keys results by the index suffix under it,
// logging (not swallowing) transport errors so partial MIB breakage shows up.
func walkIndexed[T any](c *SNMPCollector, session SnmpSession, base string, extract func(gosnmp.SnmpPDU) (T, bool)) map[string]T {
	pdus, err := session.BulkWalkAll(base)
	if err != nil {
		c.logger.Warn("snmp walk failed", "oid", base, "error", err)
		return nil
	}
	out := make(map[string]T, len(pdus))
	for _, p := range pdus {
		if v, ok := extract(p); ok {
			out[indexSuffix(p.Name, base)] = v
		}
	}
	c.logger.Debug("snmp walk detail", "oid", base, "pdus", len(pdus), "kept", len(out))
	return out
}

func getValues(session SnmpSession, oids ...string) map[string]float64 {
	if len(oids) == 0 {
		return nil
	}
	pdus, err := session.Get(oids)
	if err != nil {
		slog.Warn("snmp get failed", "oids", strings.Join(oids, ","), "error", err)
		return nil
	}
	out := map[string]float64{}
	for _, p := range pdus {
		if p.Type == gosnmp.NoSuchObject || p.Type == gosnmp.NoSuchInstance {
			continue
		}
		if v, ok := pduToFloat(p); ok {
			out[strings.TrimPrefix(p.Name, ".")] = v
		}
	}
	slog.Debug("snmp get detail", "oids", strings.Join(oids, ","), "vars", len(pdus), "kept", len(out))
	return out
}

// rawOID extracts an OID-typed PDU's dotted string (e.g. hrStorageType).
func rawOID(p gosnmp.SnmpPDU) (string, bool) {
	if p.Type == gosnmp.ObjectIdentifier {
		v, ok := p.Value.(string)
		return v, ok
	}
	return "", false
}
