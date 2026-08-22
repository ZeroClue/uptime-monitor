package collector

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ZeroClue/uptime-monitor/internal/ssh"
)

func scriptTestHost() Host {
	return Host{
		ID: 7, Name: "web-01", Connection: "ssh", Endpoint: "10.0.0.5", Port: 2222,
		User: "monitor", Timeout: 10 * time.Second,
		CollectorPreference: "custom",
		ScriptName:          "queue depth",
		ScriptCommand:       `/usr/local/bin/stats --host {{.Host}} --port {{.Port}}`,
	}
}

func TestParseScriptOutput_JSON(t *testing.T) {
	now := time.Date(2026, 8, 22, 10, 0, 0, 0, time.UTC)
	output := `[
		{"metric": "depth", "value": 12},
		{"metric": "lag_ms", "value": 3.5, "timestamp": 1779000000},
		{"metric": "rate", "value": 0.25, "timestamp": "2026-08-22T09:59:00Z"},
		{"metric": "custom.queue_depth.already_prefixed", "value": 1}
	]`

	metrics, err := parseScriptOutput("queue depth", "json", output, now)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(metrics) != 4 {
		t.Fatalf("want 4 metrics, got %d", len(metrics))
	}

	prefix := "custom.queue_depth."
	want := []struct {
		metric string
		value  float64
		ts     time.Time
	}{
		{prefix + "depth", 12, now},
		{prefix + "lag_ms", 3.5, time.Unix(1779000000, 0)},
		{prefix + "rate", 0.25, time.Date(2026, 8, 22, 9, 59, 0, 0, time.UTC)},
		{prefix + "already_prefixed", 1, now},
	}
	for i, w := range want {
		got := metrics[i]
		if got.Metric != w.metric {
			t.Errorf("metrics[%d].Metric: want %q got %q", i, w.metric, got.Metric)
		}
		if got.Value != w.value {
			t.Errorf("metrics[%d].Value: want %v got %v", i, w.value, got.Value)
		}
		if !got.Timestamp.Equal(w.ts) {
			t.Errorf("metrics[%d].Timestamp: want %v got %v", i, w.ts, got.Timestamp)
		}
	}
}

func TestParseScriptOutput_JSON_Errors(t *testing.T) {
	cases := map[string]string{
		"not an array":       `{"metric": "x", "value": 1}`,
		"invalid value type": `[{"metric": "x", "value": "high"}]`,
		"missing metric":     `[{"value": 1}]`,
		"bad timestamp type": `[{"metric": "x", "value": 1, "timestamp": true}]`,
		"malformed json":     `[{"metric": "x", `,
		"empty metric":       `[{"metric": "", "value": 1}]`,
		"unparsable rfc3339": `[{"metric": "x", "value": 1, "timestamp": "yesterday"}]`,
	}
	now := time.Now()
	for name, output := range cases {
		if _, err := parseScriptOutput("s", "json", output, now); err == nil {
			t.Errorf("%s: expected error, got none", name)
		}
	}
}

func TestParseScriptOutput_SanitizesUnsafeChars(t *testing.T) {
	now := time.Now()
	metrics, err := parseScriptOutput("My Script!", "json",
		`[{"metric": "Temp (C) / core#1", "value": 42}]`, now)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := "custom.my_script_.temp__c____core_1"
	if metrics[0].Metric != want {
		t.Errorf("metric: want %q got %q", want, metrics[0].Metric)
	}
}

func TestParseScriptOutput_CSV(t *testing.T) {
	now := time.Date(2026, 8, 22, 10, 0, 0, 0, time.UTC)
	unix := now.Add(-time.Minute).Unix()
	output := fmt.Sprintf("# comment line\nmetric,value\nrooms_full,3\nwait_seconds,45.5,%d\n\n", unix)

	metrics, err := parseScriptOutput("helpdesk", "csv", output, now)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(metrics) != 2 {
		t.Fatalf("want 2 metrics, got %d: %+v", len(metrics), metrics)
	}
	if metrics[0].Metric != "custom.helpdesk.rooms_full" || metrics[0].Value != 3 {
		t.Errorf("first: %+v", metrics[0])
	}
	if metrics[1].Metric != "custom.helpdesk.wait_seconds" || metrics[1].Value != 45.5 {
		t.Errorf("second: %+v", metrics[1])
	}
	if !metrics[1].Timestamp.Equal(time.Unix(unix, 0)) {
		t.Errorf("csv timestamp: want %v got %v", time.Unix(unix, 0), metrics[1].Timestamp)
	}

	if _, err := parseScriptOutput("helpdesk", "csv", "only_one_field\n", now); err == nil {
		t.Error("expected error for field-count mismatch")
	}
	if _, err := parseScriptOutput("helpdesk", "csv", "a,b,c,d\n", now); err == nil {
		t.Error("expected error for too many columns")
	}
	if _, err := parseScriptOutput("helpdesk", "csv", "a,notanumber\n", now); err == nil {
		t.Error("expected error for unparsable value")
	}
}

func TestParseScriptOutput_Plain(t *testing.T) {
	now := time.Now()
	metrics, err := parseScriptOutput("temp", "plain", "  41.5\n", now)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(metrics) != 1 || metrics[0].Metric != "custom.temp.value" || metrics[0].Value != 41.5 {
		t.Fatalf("unexpected: %+v", metrics)
	}
	if _, err := parseScriptOutput("temp", "plain", "hot\n", now); err == nil {
		t.Error("expected error for non-numeric output")
	}
}

func TestParseScriptOutput_EmptyScriptName(t *testing.T) {
	if _, err := parseScriptOutput("", "plain", "1\n", time.Now()); err == nil {
		t.Error("expected error for empty script name")
	}
}

func TestParseScriptOutput_SampleCap(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("[")
	for i := 0; i < maxScriptSamples+1; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		fmt.Fprintf(&sb, `{"metric": "m%d", "value": %d}`, i, i)
	}
	sb.WriteString("]")
	if _, err := parseScriptOutput("s", "json", sb.String(), time.Now()); err == nil {
		t.Error("expected error exceeding sample cap")
	}
}

type stubSSH struct {
	exec func(ctx context.Context, target *ssh.SSHTarget, cmd string) (string, error)
}

func (s *stubSSH) Exec(ctx context.Context, target *ssh.SSHTarget, cmd string) (string, error) {
	return s.exec(ctx, target, cmd)
}

func TestCustomCollector_Gating(t *testing.T) {
	c := NewCustomCollector(WithCustomSSHClient(&stubSSH{}))
	ctx := context.Background()

	host := scriptTestHost()
	host.ScriptCommand = ""
	if _, err := c.Collect(ctx, host); err == nil {
		t.Error("expected error without script command")
	}

	host = scriptTestHost()
	host.Connection = "local"
	if _, err := c.Collect(ctx, host); err == nil {
		t.Error("expected error for non-ssh connection")
	}

	host = scriptTestHost()
	host.ScriptName = ""
	if _, err := c.Collect(ctx, host); err == nil {
		t.Error("expected error without script name")
	}
}

func TestCustomCollector_TemplateRenderAndMapping(t *testing.T) {
	var gotCmd, gotTarget string
	c := NewCustomCollector(WithCustomSSHClient(&stubSSH{
		exec: func(_ context.Context, target *ssh.SSHTarget, cmd string) (string, error) {
			gotCmd = cmd
			gotTarget = fmt.Sprintf("%s@%s:%d", target.User, target.Endpoint, target.Port)
			return `[{"metric":"depth","value":9,"timestamp":"2026-08-22T10:00:00Z"}]`, nil
		},
	}))

	samples, err := c.Collect(context.Background(), scriptTestHost())
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	wantCmd := `/usr/local/bin/stats --host 10.0.0.5 --port 2222`
	if gotCmd != wantCmd {
		t.Errorf("rendered cmd: want %q got %q", wantCmd, gotCmd)
	}
	if gotTarget != "monitor@10.0.0.5:2222" {
		t.Errorf("ssh target: %q", gotTarget)
	}
	if len(samples) != 1 {
		t.Fatalf("want 1 sample, got %d", len(samples))
	}
	s := samples[0]
	if s.HostID != 7 || s.Collector != "custom" || s.Metric != "custom.queue_depth.depth" || s.Value != 9 {
		t.Errorf("sample mapping: %+v", s)
	}
}

func TestCustomCollector_TemplateError(t *testing.T) {
	c := NewCustomCollector(WithCustomSSHClient(&stubSSH{
		exec: func(_ context.Context, _ *ssh.SSHTarget, _ string) (string, error) {
			return "", nil
		},
	}))
	host := scriptTestHost()
	host.ScriptCommand = `echo {{.NoSuchField}}`
	if _, err := c.Collect(context.Background(), host); err == nil {
		t.Error("expected template error")
	}
}

func TestCustomCollector_SSHErrorPropagates(t *testing.T) {
	boom := errors.New("connection refused")
	c := NewCustomCollector(WithCustomSSHClient(&stubSSH{
		exec: func(_ context.Context, _ *ssh.SSHTarget, _ string) (string, error) {
			return "", boom
		},
	}))
	_, err := c.Collect(context.Background(), scriptTestHost())
	if !errors.Is(err, boom) {
		t.Errorf("want wrapped ssh error, got %v", err)
	}
}

func TestCustomCollector_PrefersLimitedExec(t *testing.T) {
	stub := &limitedStubSSH{
		maxBytesSeen: -1,
		out:          `[{"metric":"x","value":1}]`,
	}
	c := NewCustomCollector(WithCustomSSHClient(stub))
	if _, err := c.Collect(context.Background(), scriptTestHost()); err != nil {
		t.Fatalf("collect: %v", err)
	}
	if stub.maxBytesSeen != maxScriptOutputBytes {
		t.Errorf("ExecLimited maxBytes: want %d got %d", maxScriptOutputBytes, stub.maxBytesSeen)
	}
}

type limitedStubSSH struct {
	maxBytesSeen int64
	out          string
}

func (s *limitedStubSSH) Exec(ctx context.Context, target *ssh.SSHTarget, cmd string) (string, error) {
	return "", errors.New("plain Exec should not be used when ExecLimited exists")
}

func (s *limitedStubSSH) ExecLimited(ctx context.Context, target *ssh.SSHTarget, cmd string, maxBytes int64) (string, error) {
	s.maxBytesSeen = maxBytes
	return s.out, nil
}
