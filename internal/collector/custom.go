package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/ZeroClue/uptime-monitor/internal/ssh"
)

const (
	maxScriptOutputBytes int64 = 1 << 20 // 1 MiB cap on script stdout
	maxScriptSamples           = 1000
)

// CustomCollector runs a user-defined command over SSH and parses its stdout
// into metrics under custom.<script_name>.*. Selected via host collector
// preference "custom".
type CustomCollector struct {
	logger    *slog.Logger
	sshClient ssh.SSHClient
}

type CustomOption func(*CustomCollector)

func WithCustomSSHClient(client ssh.SSHClient) CustomOption {
	return func(c *CustomCollector) {
		c.sshClient = client
	}
}

func WithCustomLogger(logger *slog.Logger) CustomOption {
	return func(c *CustomCollector) {
		c.logger = logger
	}
}

func NewCustomCollector(opts ...CustomOption) *CustomCollector {
	c := &CustomCollector{logger: slog.Default()}
	for _, opt := range opts {
		opt(c)
	}
	if c.sshClient == nil {
		c.sshClient = ssh.NewSSHClient(c.logger, nil)
	}
	return c
}

func (c *CustomCollector) Name() string {
	return "custom"
}

// limitedExecer is implemented by SSH clients that enforce an output-size
// ceiling; the custom collector prefers it so a runaway script cannot
// exhaust memory.
type limitedExecer interface {
	ExecLimited(ctx context.Context, target *ssh.SSHTarget, cmd string, maxBytes int64) (string, error)
}

func (c *CustomCollector) Collect(ctx context.Context, host Host) ([]Sample, error) {
	switch {
	case host.ScriptCommand == "":
		return nil, fmt.Errorf("collector %q requires a script command, host %q has none", c.Name(), host.Name)
	case host.ScriptName == "":
		return nil, fmt.Errorf("collector %q requires a script name, host %q has none", c.Name(), host.Name)
	case host.Connection != "ssh" && host.Connection != "tailscale":
		return nil, fmt.Errorf("collector %q requires an ssh-based connection, host %q has %q", c.Name(), host.Name, host.Connection)
	}

	cmd, err := renderScriptCommand(host)
	if err != nil {
		return nil, fmt.Errorf("script template: %w", err)
	}

	target := SSHTargetFromHost(host)
	var output string
	if le, ok := c.sshClient.(limitedExecer); ok {
		output, err = le.ExecLimited(ctx, target, cmd, maxScriptOutputBytes)
	} else {
		output, err = c.sshClient.Exec(ctx, target, cmd)
	}
	if err != nil {
		return nil, fmt.Errorf("script exec failed: %w", err)
	}

	metrics, err := parseScriptOutput(host.ScriptName, host.ScriptParse, output, time.Now())
	if err != nil {
		return nil, err
	}

	samples := make([]Sample, len(metrics))
	for i, m := range metrics {
		samples[i] = Sample{
			HostID:    host.ID,
			Metric:    m.Metric,
			Value:     m.Value,
			Timestamp: m.Timestamp,
			Collector: c.Name(),
		}
	}
	c.logger.Debug("custom script collected", "host", host.Name, "script", host.ScriptName, "samples", len(samples))
	return samples, nil
}

func renderScriptCommand(host Host) (string, error) {
	tmpl, err := template.New("script").Parse(host.ScriptCommand)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	err = tmpl.Execute(&sb, struct {
		Host string
		Port int
	}{host.Endpoint, host.Port})
	return sb.String(), err
}

// scriptMetric is one parsed data point before binding to a host.
type scriptMetric struct {
	Metric    string
	Value     float64
	Timestamp time.Time
}

// parseScriptOutput converts raw script stdout into metrics under the
// custom.<script_name>.* namespace. mode is json (default), csv or plain.
func parseScriptOutput(scriptName, mode, output string, now time.Time) ([]scriptMetric, error) {
	name := sanitizeMetricPart(scriptName)
	if name == "" {
		return nil, fmt.Errorf("script name %q normalizes to empty metric prefix", scriptName)
	}
	prefix := "custom." + name + "."
	switch mode {
	case "", "json":
		return parseJSONScriptOutput(prefix, output, now)
	case "csv":
		return parseCSVScriptOutput(prefix, output, now)
	case "plain":
		return parsePlainScriptOutput(prefix, output, now)
	default:
		return nil, fmt.Errorf("unknown script parse mode %q (want json, csv or plain)", mode)
	}
}

func parseJSONScriptOutput(prefix, output string, now time.Time) ([]scriptMetric, error) {
	var raw []struct {
		Metric    string          `json:"metric"`
		Value     float64         `json:"value"`
		Timestamp json.RawMessage `json:"timestamp"`
	}
	if err := json.Unmarshal([]byte(output), &raw); err != nil {
		return nil, fmt.Errorf("parse script JSON: %w", err)
	}
	if len(raw) > maxScriptSamples {
		return nil, fmt.Errorf("script returned %d samples, cap is %d", len(raw), maxScriptSamples)
	}
	metrics := make([]scriptMetric, 0, len(raw))
	for i, r := range raw {
		if r.Metric == "" {
			return nil, fmt.Errorf("sample %d: empty metric", i)
		}
		ts, err := decodeScriptTimestamp(r.Timestamp, now)
		if err != nil {
			return nil, fmt.Errorf("sample %d (%s): %w", i, r.Metric, err)
		}
		metrics = append(metrics, scriptMetric{
			Metric:    joinPrefix(prefix, r.Metric),
			Value:     r.Value,
			Timestamp: ts,
		})
	}
	return metrics, nil
}

func decodeScriptTimestamp(raw json.RawMessage, now time.Time) (time.Time, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return now, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return now, fmt.Errorf("timestamp %q: %w", s, err)
		}
		return t, nil
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err == nil {
		return time.Unix(n, 0), nil
	}
	return now, errors.New("timestamp must be RFC3339 string or unix seconds")
}

func parseCSVScriptOutput(prefix, output string, now time.Time) ([]scriptMetric, error) {
	lines := strings.Split(output, "\n")
	metrics := make([]scriptMetric, 0, len(lines))
	lineNo := 0
	for _, line := range lines {
		lineNo++
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || line == "metric,value" {
			continue
		}
		fields := strings.Split(line, ",")
		if len(fields) < 2 || len(fields) > 3 {
			return nil, fmt.Errorf("line %d: want 2-3 comma-separated fields, got %d", lineNo, len(fields))
		}
		value, err := strconv.ParseFloat(strings.TrimSpace(fields[1]), 64)
		if err != nil {
			return nil, fmt.Errorf("line %d: value %q: %w", lineNo, fields[1], err)
		}
		ts := now
		if len(fields) == 3 {
			unixSec, err := strconv.ParseInt(strings.TrimSpace(fields[2]), 10, 64)
			if err != nil {
				return nil, fmt.Errorf("line %d: timestamp %q: %w", lineNo, fields[2], err)
			}
			ts = time.Unix(unixSec, 0)
		}
		if len(metrics) >= maxScriptSamples {
			return nil, fmt.Errorf("script exceeded %d samples, cap is %d", len(metrics), maxScriptSamples)
		}
		metrics = append(metrics, scriptMetric{
			Metric:    joinPrefix(prefix, fields[0]),
			Value:     value,
			Timestamp: ts,
		})
	}
	return metrics, nil
}

func parsePlainScriptOutput(prefix, output string, now time.Time) ([]scriptMetric, error) {
	v, err := strconv.ParseFloat(strings.TrimSpace(output), 64)
	if err != nil {
		return nil, fmt.Errorf("plain output %q: %w", strings.TrimSpace(output), err)
	}
	return []scriptMetric{{Metric: prefix + "value", Value: v, Timestamp: now}}, nil
}

// joinPrefix namespaces a metric under prefix unless it already carries it,
// sanitizing everything outside [a-z0-9_.-].
func joinPrefix(prefix, metric string) string {
	metric = sanitizeMetricPart(metric)
	if strings.HasPrefix(metric, strings.TrimSuffix(prefix, ".")) {
		return metric
	}
	return prefix + metric
}

func sanitizeMetricPart(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}
